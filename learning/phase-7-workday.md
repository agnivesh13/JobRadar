# Phase 7: Workday career sites

**Not in PLAN.md.** Added on request after Phase 6, because many large companies host their careers on `*.myworkdayjobs.com` and those all returned 0 jobs (Phase 5, known gap 1).
**Done when:** a company on Workday is crawled like any other board, and a repeat crawl does not download everything again.

## What I checked before writing code (17 Nov 2026)

Same rule as the other boards: live responses first, trimmed copies in `internal/boards/testdata/workday_*.json`.

| Question | Answer from the live site |
| --- | --- |
| Does robots.txt allow it? | Yes. Two hosts checked; both allow the site path and disallow only `/refreshFacet/` and a few named sites. `/wday/cxs/` is not disallowed. The crawler still checks robots.txt per host on every crawl. |
| Where is the job list? | `POST https://{host}/wday/cxs/{tenant}/{site}/jobs` with a small JSON body. It is the same request the public page makes; no login, no key. |
| How big is a page? | 20. Asking for 50 returns HTTP 400. |
| Where is the description? | Not in the list. `GET https://{host}/wday/cxs/{tenant}/{site}/job/...` returns one posting as JSON. |
| How many postings? | `total` is capped at 2000, and is only filled in on the first page. Later pages say `total: 0`. |
| What happens past the end? | An offset beyond the total returns the **first page again**. Paging until an empty page would never stop. |

## What I built

1. **`internal/boards/workday.go`**: `Detect` recognises a Workday link in any form (site URL, with a locale prefix, a posting URL, the API URL). The company token is `host/tenant/site`. `ParseWorkdayList` and `ParseWorkdayJob` read the two response shapes.
2. **POST support in the fetch client** (`crawl.Client.PostJSON`). It goes through exactly the same checks as a GET: never-crawl list, robots.txt, the per-domain rate limit, the honest User-Agent, back-off on 429 and 5xx.
3. **`internal/pipeline/workday.go`**: the crawl itself, on top of the Phase 5 frontier (`crawl_pages`):
   - the first list page claims every other list page
   - each list page claims the postings it does not have yet
   - each posting page yields one job
   - the crawl is done when no claimed page is unprocessed
4. **`store.TouchJobs`**: marks postings that are already stored as "seen by this crawl" without downloading them.
5. **`WORKDAY_COUNTRY`** (default `India`): if the site offers a location filter for that country, only that part is crawled.

## Design decisions worth remembering

- **A list page is a URL, even though it is a POST.** The frontier is keyed by URL, so each page is named `.../jobs?offset=40&f=<filter>`. The fetcher turns that name into the POST body. This kept the frontier, the page store and the message ids unchanged.
- **Reuse the frontier instead of inventing "a feed with pages".** Workday is really a small site crawl: pages that point at other pages. Redelivery safety, the page cap and "when is it finished" were already solved there.
- **Do not download what is already stored.** Without this, NVIDIA's Indian postings would cost about 250 requests every six hours. With it, an unchanged site costs only its list pages. The price: an edited description is not noticed until the posting is re-read (use "Crawl now", which forces a full re-read).
- **Narrow by country at the source.** NVIDIA lists 2000+ postings worldwide and 240 in India. The site's own filter is used rather than downloading everything and throwing most of it away. Sites name their filters differently, so the first page's filter list is read and searched for the country; if there is no such filter the whole site is crawled.

## Problems I hit and how I solved them

### 1. `total` is zero on every page but the first
- **Seen in the live check**, before any code: page 2 answered `"total":0` with 20 postings.
- **Fix:** only the first page decides how many pages exist. A test feeds the same fixture as "page 2" and asserts it creates no further pages.

### 2. Paging past the end wraps around
- **Seen in the live check:** offset 2000 returned the first page.
- **Fix:** pages are computed from the total (capped at 2000), never by "continue until empty".

### 3. Not every site has a country filter
- **Seen on the second site I checked:** BrowserStack has only a list of locations, with "India Remote" among them.
- **Fix:** look for a filter value that is exactly the country; otherwise select every value that starts with the country name within one filter.

### 4. Sixteen postings became five jobs
- **Seen in the first live crawl:** BrowserStack lists the same title at the same location under several requisition numbers.
- **Not a bug:** the fingerprint (company, title, location) from Phase 1 merges them on purpose, and the other URLs are kept in `alt_urls`. `TouchJobs` therefore matches on `alt_urls` too, or the merged postings would be downloaded again on every crawl.

### 5. "Crawl now" downloaded everything again
- **Expected once I looked:** "Crawl now" sets the force flag, and force means "do not trust what is stored". The skip was verified with a scheduled crawl instead.

## Result (17 Nov 2026)

| Check | Outcome |
| --- | --- |
| BrowserStack, first crawl | 18 requests, 16 postings (5 after merging duplicates), 24 s |
| BrowserStack, scheduled repeat | 2 requests, "16 jobs", nothing closed |
| NVIDIA, first crawl | 253 requests (13 list pages, 240 postings), about 4.5 minutes at one request per second; 202 jobs after merging duplicates, 22 matching the profile |

BrowserStack was one of the companies that returned 0 jobs in Phase 5.

## Known gaps

1. **The older address form `wd1.myworkdaysite.com/recruiting/{tenant}/{site}` is not recognised.** I did not check it against a live response, so I did not write it.
2. **More than 2000 postings after filtering cannot be read**; Workday itself stops there.
3. **One large first crawl occupies the fetchers.** They all wait on the same domain's one-per-second slot. Other companies are delayed for those minutes, not skipped.
4. **Workday companies cannot be found by name.** The site name (`NVIDIAExternalCareerSite`, `External`) cannot be guessed, so a company is picked up when its careers page links to Workday, or when you paste the Workday link into deep search.
5. **No test covers `workdayPage` or `TouchJobs`** (they need a database); they were verified by the live runs above.

## How to verify

```
node scripts/deep-search.mjs "https://browserstack.wd3.myworkdayjobs.com/External"
go test ./internal/boards/
```
Or paste a Workday link into **Companies -> Deep search**.
