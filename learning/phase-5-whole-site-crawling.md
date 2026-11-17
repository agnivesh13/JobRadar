# Phase 5: Whole-site crawling

**Goal (from PLAN.md):** generic parser (structured data, sitemap, link-following), headless renderer, company resolver, deep search box.
**Done when:** typing a company not in the list returns its jobs within two minutes.

## What I built, step by step

1. **Generic page parser (`internal/extract/page.go`).** One pass over the HTML collects: the title and first heading, visible text (navigation and footer left out), every link made absolute with tracking parameters and fragments removed, every schema.org `JobPosting` found in JSON-LD (top level, arrays or `@graph`), and any reference to a known job board anywhere in the source.
2. **Rules (`internal/extract/rules.go`).**
   - `Follow`: which links a site crawl may fetch next. Same site only, job-looking path or link text, never blog, docs, press, login and similar; depth limit 4.
   - `HeuristicJob`: recognises a posting with no structured data. The URL must look like one posting, there must be a heading, and the body must read like a job description with a way to apply.
   - `CareersLinks`, `LooksEmpty`, `HostedCareers`, `SitemapURLs`.
3. **Frontier in Postgres (`crawl_pages`, `store.FinishSitePage`).** See "Design decisions".
4. **Site-page stage (`internal/pipeline/site.go`).** For each page, in order: board detected -> switch the company to that board's feed and stop; empty JavaScript shell -> send to the renderer; otherwise extract postings (structured data, then heuristics, then the optional LLM) and claim links to follow.
5. **Renderer (`internal/render`, `services.RunRenderer`).** Headless Chromium via chromedp, one tab per page, at most two tabs. It runs the same checks as the fetcher before opening a page: never-crawl list, robots.txt, the per-domain rate limit. Optional container: `docker compose --profile render up -d`.
6. **Resolver (`internal/resolve`).** Name -> board under its own name -> domain -> careers page -> board. If two websites look equally plausible, it returns both and the interface asks.
7. **Deep search (`internal/api/companies.go`).** `POST /api/search` resolves, saves the company and publishes a task on the **high-priority** subject, tagged with a search id. Every event of that crawl carries the id, so the panel streams just that crawl.
8. **Companies screen (`web/src/Companies.jsx`).** The list (board, last crawl, open roles, roles matching you, pay tier), the search box, the streaming panel, crawl now, try again, remove.
9. **`scripts/deep-search.mjs`**, the same flow from the command line.

## Design decisions worth remembering

- **The frontier is a table, and "claim a URL" is an insert.** `crawl_pages (crawl_id, url)` has a primary key, so `INSERT ... ON CONFLICT DO NOTHING` lets two extractors discover the same link and exactly one of them schedule it. No in-memory visited set, so a restarted worker loses nothing.
- **Knowing when a multi-page crawl is finished.** Each page is marked processed in the same transaction that claims its links. The crawl is done when no claimed page is unprocessed. An advisory lock on the crawl id serialises these transactions, so exactly one page observes "zero remaining" and emits the crawl-done marker.
- **Redelivery-safe page completion.** If the extractor dies after committing but before publishing the follow-up tasks, the redelivered page finds itself already processed, changes nothing, and gets back its still-unprocessed children to publish again.
- **Priority without priority queues.** JetStream has no message priorities. There are two subjects, `crawl.fetch.high` and `crawl.fetch.normal`; a fetcher always asks the high one first. A deep search started while 276 background tasks were queued had its first page fetched within seconds.
- **Board first, always.** If a careers page loads or links to Greenhouse, Lever or Ashby, the company is switched to the board feed: one request instead of a crawl, with better data.
- **Same rules for Chrome.** The renderer goes through `Client.Allow` (ban list, robots.txt, rate-limit slot) before every page. A headless browser is still a crawler.
- **Limits from the plan are constants in code:** depth 4, 500 pages per crawl.

## Problems I hit and how I solved them

### 1. chromedp's API was not the one I remembered
- **Symptom:** `not enough arguments in call to chromedp.Run`, `undefined: chromedp.ByQuery`.
- **Cause:** the installed version (v0.23) has a generics-based API: `Run` takes one action and returns its value; `Do` runs steps that return nothing; selectors are typed (`chromedp.CSS("body")`).
- **How I fixed it:** I did not guess. I listed the exported functions in the module source under `~/go/pkg/mod/github.com/chromedp/chromedp@v0.23.1` and read the doc comments of `Run` and `Do`.
- **Lesson:** when the compiler disagrees with memory, read the source of the exact version in `go.mod`.

### 2. A dead fetch would have left a site crawl hanging forever
- **Reasoning while designing:** the crawl completes when every claimed page is processed. A page whose fetch is dead-lettered is never processed, so the crawl never completes.
- **Fix:** a dead-lettered fetch (or render) publishes a "failed page" result to the extractor, which marks the page processed like any other. The first version did this only for board feeds.

### 3. The same page crawled twice: `https://site.com` and `https://site.com/`
- **Seen in the first deep search.** Two fetches, two renders.
- **Fix:** URL normalisation gives an empty path a `/`. Also, redirects that include a default port (`example.com:443`) are cleaned, so one page is not stored under two addresses.

### 4. "0 jobs" with no explanation
- **Symptom:** deep search on a JavaScript-only careers site finished with "0 jobs, 0 links" and nothing else, because the optional renderer was not running.
- **Fix:** the event now says "page needs JavaScript and the renderer is not running (docker compose --profile render up -d)". A silent zero is the worst output; it looks the same as "this company has no openings".

### 5. Server-rendered marketing page, job list filled in by JavaScript
- **Symptom:** a careers page with plenty of text (so not an "empty shell") but no posting and no job link.
- **Fix:** a careers page that yields neither a posting nor a link over plain HTTP is rendered once.

### 6. A fixed 3 second wait returned empty shells
- **Fix:** the renderer polls the page's visible text length every 1.5 s and returns when it has stopped growing, up to 15 s.

### 7. Many career pages hand off to hiring software with no parser
- **Found by querying the stored pages of every empty crawl** for known hiring-product hostnames: Workday, Kula, Dover, and Keka twice.
- **Fix (general):** when the careers page yields nothing but its source links to the company's site on such a product, the crawl continues there and the company's careers URL is updated to it.
- **Still not working:** the Keka app rendered as an empty shell even after the longer wait. See "Known gaps".

## Result (17 Nov 2026)

| Deep search | Outcome | Time |
| --- | --- | --- |
| Atlan (not in the list) | Found on Ashby under its own name; 7 jobs | 20 s |
| Vahan (custom site) | 65 pages crawled, 51 jobs | 115 s |
| Zerodha | Resolved, careers page rendered in Chrome; the page exposes no posting links; 0 jobs | 17 s |
| Juspay, BrowserStack, Chargebee | Resolved and crawled; job lists live in Workday or behind interaction; 0 jobs | 49 to 85 s |
| Postman | Resolved to the wrong domain (`postman.ai`), then no careers page | failed in 42 s |
| Inito | Hand-off to its Keka site followed; Keka app did not render; 0 jobs | 61 s |

So the "done when" holds for companies on a supported board and for custom sites whose postings are ordinary linked pages. It does **not** hold for every company.

Whole-site crawls started by discovery in the same session: Vahan 33 jobs, GroMo 13, Search Atlas 7, Proofline 4, Desible.ai 4, Cityfurnish 2; about 15 others returned nothing.

## Known gaps (honest list)

1. **Hiring products without a parser.** Keka, Darwinbox, Workday, Kula, Zoho Recruit and similar. The plan's answer is the right one: add a parser per product as it shows up. Keka is first in line (two of the first twenty custom companies). Workday was added later: see [phase-7-workday.md](phase-7-workday.md).
2. **Name -> domain without a search engine is guesswork.** It tries `name.com/.in/.io/.ai/.co`, which found `postman.ai` instead of `postman.com`. Typing the domain works, and so does configuring SearXNG (`SEARXNG_URL`), which the resolver already uses when present.
3. **Aggregator sites look like companies.** One discovered "company" was a job marketplace whose 500 crawled pages were listings of other firms' jobs. I removed it by hand from the Companies screen. A rule for this is not built.
4. **The heuristic posting detector is unproven at scale.** It has unit tests and worked on the sites above, but "log misses and add patterns weekly" from the plan's risk table is still the process.
5. **No automated test covers the frontier logic** (`FinishSitePage`); it was exercised only by real crawls, including during the chaos test.

## How to verify

```
docker compose --profile render up -d --build
node scripts/deep-search.mjs "Atlan"
node scripts/deep-search.mjs "vahan.ai"
```
Or in the browser: **Companies**, type a name, press **Deep search this company**, and watch the panel: domain resolved, careers page found, board detected or pages crawled, jobs found.

`go test ./internal/extract/ ./internal/resolve/` covers JSON-LD parsing, link selection, the posting heuristic, and the resolver against a fake web.
