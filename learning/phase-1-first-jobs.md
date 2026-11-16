# Phase 1: First jobs

**Goal (from PLAN.md):** seed CSV loader; Greenhouse, Lever and Ashby parsers; single-process fetch, extract, save.
**Done when:** 300 seed companies crawled; jobs visible with a SQL query.

## What I built, step by step

1. **Confirmed the feeds against live responses** before writing any parser (the plan forbids coding them from memory). I downloaded one real feed per board with `curl`, printed the shape of the first job and the union of all keys, and saved trimmed copies into `internal/boards/testdata/`.

   | Board | Feed URL | Shape |
   | --- | --- | --- |
   | Greenhouse | `boards-api.greenhouse.io/v1/boards/{token}/jobs?content=true` | `{"jobs":[...],"meta":{...}}` |
   | Lever | `api.lever.co/v0/postings/{token}?mode=json` | bare array |
   | Ashby | `api.ashbyhq.com/posting-api/job-board/{token}?includeCompensation=true` | `{"jobs":[...],"apiVersion":...}` |

2. **Parsers (`internal/boards`).** One file per board. Each maps the board's JSON into one shared `model.RawJob` (URL, title, location, remote, department, employment type, plain-text description, posted date, optional structured pay). `boards.Detect` recognises a board from any URL (used later by deep search).
3. **Tests that never touch the network.** `boards_test.go` parses the saved fixtures and checks concrete values (titles, locations, Ashby's salary numbers, Lever's millisecond timestamps).
4. **Polite fetch client (`internal/crawl`).** One `Client.Fetch` that applies, in order: the never-crawl list (LinkedIn, Naukri, Indeed, Glassdoor, Wellfound), robots.txt (cached 24 h), the per-domain rate limit, an honest User-Agent, a body size cap. 429 and 5xx come back as a `RetryableError` and slow the whole domain down.
5. **Shared rate limiter in Postgres (`store.Limiter`).** See "Design decisions".
6. **Pipeline as three plain functions (`internal/pipeline/stages.go`).** `Fetcher.Handle`, `Extractor.Handle`, `Scorer.HandleJob/HandleDone`. They do not know how messages travel. `pipeline.Local` chains them in one process; Phase 3 puts NATS between the very same functions.
7. **Seed loader (`jobradar seed`).** CSV with a header row, columns matched by name, safe to run twice.
8. **Seed list (`scripts/probe-seeds.mjs`).** See problem 3.
9. **`jobradar crawl`.** Single-process run over every due company.

## Design decisions worth remembering

- **The rate limiter is one SQL statement.** Each fetcher runs:
  `INSERT ... ON CONFLICT DO UPDATE SET next_free_at = GREATEST(next_free_at, now()) + interval RETURNING next_free_at - interval`.
  That atomically *reserves the next free slot* for the domain and tells the caller when its slot starts; the caller sleeps until then. Ten workers hitting one domain get ten consecutive slots one second apart, with no locks held and no coordination between them. It works identically for one process and for five containers, which is why I built it now instead of in Phase 3.
- **Fingerprint = hash(company, normalised title, normalised location).** The `jobs.fingerprint` column is UNIQUE, and saving is `INSERT ... ON CONFLICT (fingerprint) DO UPDATE`. So saving the same job twice is harmless by construction, and the same job reached through two URLs becomes one row with the second URL in `alt_urls`.
- **"Crawl done" is its own step.** After a company's feed is processed, `FinishCrawl` gives every open job that the crawl did not see one "miss"; two misses in a row close the job. It runs in one transaction guarded by the crawl id, so running it twice for the same crawl changes nothing.
- **A feed that suddenly returns zero jobs is treated as a broken parser, not as "everything closed".** Nothing is closed and a warning is logged. This is the plan's "alert when a company that had jobs drops to zero".
- **robots.txt follows RFC 9309.** A 4xx for robots.txt means "no rules" (Ashby's API host answers 401). A 5xx or network error means "stay away for now".

## Problems I hit and how I solved them

### 1. A guessed Greenhouse token returned 404
- **Symptom:** my first sample fetch used the token `postman` and got `{"status":404,"error":"Job not found"}`.
- **Cause:** board tokens are chosen by each company and are not always the company name.
- **Fix:** tried other tokens until one answered, and from then on treated every token as unverified until probed (which led to the seed probe script).

### 2. Greenhouse descriptions are HTML that is itself HTML-escaped
- **Symptom:** the `content` field looked like `&lt;p class=&quot;p1&quot;&gt;...`. Stripping tags did nothing, because there were no real tags yet.
- **Fix:** `html.UnescapeString` first, then strip tags. The test asserts that the cleaned text contains neither `<` nor `&lt;`.

### 3. Greenhouse's bulk feed has no structured salary
- **What I expected:** a `pay_input_ranges` field when `pay_transparency=true` is added.
- **What I measured:** 0 of 787 Stripe jobs carried it in the bulk feed.
- **Decision:** for Greenhouse, salary always comes from the description text (Phase 2's parser). Ashby and Lever do carry structured pay, and those parsers use it.

### 4. A failing test caught trailing spaces in my HTML-to-text function
- **Symptom:** `got "Pay: 12–18 LPA \n- Go"`, wanted no space before the newline.
- **Cause:** replacing a closing `</b>` with a space left a space just before the line break.
- **Fix:** trim spaces around newlines after collapsing whitespace. Small, but salary parsing reads line by line, so clean lines matter.

### 5. Building a seed list of 300 companies without inventing tokens
- **Problem:** I cannot know from memory which company uses which board under which token. Writing 300 rows by hand would produce a file full of dead entries.
- **Fix:** `scripts/probe-seeds.mjs` takes about 740 candidate names, asks each of the three boards "do you have a board called this?", and writes only the hits. It runs at one request per second per domain, like the crawler itself.
- **Caveat I found afterwards:** a slug can belong to a *different* company with the same name. `slice`, `river`, `bounce` and `porter` resolved to US companies, not the Indian startups I meant. They are harmless (their jobs fail the location filter) but the names in `seeds/companies.csv` are best guesses. Remove or fix rows as you notice them.
- **Hit rate:** roughly 15% for Indian startups (most use Keka, Darwinbox, Zoho Recruit or their own page, which Phases 5 and 6 cover) and about 50% for global companies.

### 6. The probe was much slower than planned
- **Symptom:** about 7 seconds per candidate instead of 1.1.
- **Cause:** the three requests per candidate run in parallel, but the loop waits for the slowest one, and misses on some hosts were slow to answer.
- **Decision:** left it running in the background and kept building. A one-off script did not deserve tuning.

## How to verify

```
docker compose up -d --build
docker compose run --rm migrate seed          # loads seeds/companies.csv
docker compose run --rm migrate crawl         # single-process crawl
docker compose exec postgres psql -U jobradar -d jobradar -c \
  "select c.name, count(*) from jobs j join companies c on c.id = j.company_id group by 1 order by 2 desc limit 10"
go test ./internal/boards/
```

Result: the single-process path was verified on 16 Nov 2026 with the first 15 probed companies (540 postings saved; an immediate second run reported every company unchanged and added nothing). The full seed list, 481 verified companies (282 Greenhouse, 160 Ashby, 39 Lever) from two probe batches, was finished later and crawled through the queue: about 39,600 postings. Those runs are in [phase-3-distributed-pipeline.md](phase-3-distributed-pipeline.md).
