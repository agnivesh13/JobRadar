# Phase 9: Swiggy, Eternal (Zomato, District), Amazon, Microsoft, Apple, Google, Meta

**Not in PLAN.md.** Asked for after Phase 8: "district, zomato, swiggy, google, microsoft, amazon, meta, apple". None of them was in the list.
**Done when:** each one is crawled, or there is a clear reason it cannot be.

## Outcome (17 Nov 2026)

| Company | How | Result |
| --- | --- | --- |
| Swiggy | Found by name on SmartRecruiters (Phase 8) | 189 India postings, 157 jobs, 0 matching |
| Zomato, Blinkit, District | All part of **Eternal** (Zomato Ltd was renamed). One careers page: eternal.com/careers | No listings: "we only accept applications through employee referrals". Kept in the list at 0 jobs |
| Amazon | New board: amazon.jobs JSON search | 2,065 India postings in 21 requests, 1,558 jobs, 37 matching |
| Microsoft | New board: Eightfold "PCSX" API (apply.careers.microsoft.com) | 219 India postings; first crawl slow because of 429s |
| Apple | New board: data embedded in jobs.apple.com pages | 179 postings read, 159 jobs, 11 matching |
| Google | Not crawled | robots.txt: `Disallow: /about/careers/applications/jobs/results` |
| Meta | Not crawled | metacareers.com robots.txt states that automated collection needs written permission |

## What I checked before writing code

| | Amazon | Microsoft | Apple |
| --- | --- | --- | --- |
| robots.txt | Disallows only `/internal` and one named bot | Disallows `/`, allows `/careers` and `/api/pcsx` | None (redirects to "page not found") |
| List | `/en/search.json?country=IND&result_limit=100&offset=N`, description included | `/api/pcsx/search?domain=microsoft.com&location=India&start=N`, 10 per page, `count` on every page | `/en-in/search?location=india-INDC&page=N`, 20 per page, data as JSON inside the HTML |
| Posting | Not needed | `/api/pcsx/position_details?position_id=…` | `/en-in/details/{id}/{slug}?team=…`, data as JSON inside the HTML |
| Country | ISO alpha-3 code (`IND`); `loc_query=India` is a text match and returns China too | `location=India` | Apple's own id `india-INDC` |

Trimmed real responses: `internal/boards/testdata/amazon_list.json`, `eightfold_*.json`, `apple_*.html` (the Apple pages cut down to their embedded JSON: 320 KB → 1.5 KB).

## What I built

1. **`boards/amazon.go`**: list pages that carry whole postings. `ListPage` gained `Jobs`, and the paged crawl stores them directly, so Amazon costs one request per 100 postings.
2. **`boards/eightfold.go`**: list plus one request per posting, like Darwinbox. Only Microsoft's host is detected; other companies on Eightfold were not checked.
3. **`boards/apple.go`**: reads `window.__staticRouterHydrationData` from both page types.
4. All three plug into `boards/paged.go` and the Phase 7 frontier crawl, with the country taken from `WORKDAY_COUNTRY`.
5. The **renderer** (headless Chrome) is now running; Eternal's page needed it to be read at all.

## Problems I hit and how I solved them

### 1. "Zomato" could not be found by name
- **Symptom:** deep search: "could not find a website".
- **Cause:** zomato.com did not answer the crawler at all (no response, not even for robots.txt). The company is now Eternal, and all its brands hire through eternal.com/careers.
- **Fix:** none needed in code; Eternal was added by its domain. "District" by name was ambiguous (district.in and an unrelated district.ai); no pick was made, as District is covered by Eternal.

### 2. A careers page with no jobs on it, even after rendering
- **Seen:** Eternal's page is empty over plain HTTP; the renderer was not running. After starting it, the rendered page still had no postings.
- **Cause:** not a bug. The rendered text says Eternal hires only through employee referrals.
- **Lesson:** "0 jobs" needs a reason. Reading the rendered text settled it in one step.

### 3. A whole-site crawl of Apple found 0 jobs
- **Symptom:** the generic crawler fetched country pages, "life at Apple" pages and location pickers.
- **Cause:** the links on the search page are not to the postings; the postings exist only in the JSON embedded in the page.
- **Fix:** an Apple board that reads that JSON. My first wait-for-result also matched the old crawl's completion and reported 0; the events showed the new crawl was working.

### 4. Amazon's location search is a text search
- **Seen in the live check:** `loc_query=India` returned 10,000 hits starting in China. `country=IND` returned 2,066, all in India.
- **Fix:** a small country-name → ISO code table; a country not in it reads the whole list (capped at 10,000 by Amazon).

### 5. A detector pattern with no capture group crashed `Detect`
- **Symptom:** panic in the Amazon test: `Detect` reads `m[1]` before calling the token function.
- **Fix:** the Amazon and Apple patterns capture the host.

### 6. Microsoft answers 429 now and then
- **Seen:** in the probe, and on most list pages of the first crawl.
- **Handled by existing code:** 429 is retryable and the client backs off. The crawl is slower, not lost.

## Known gaps

1. **Google and Meta are not crawled**, by their own robots.txt. Visit their careers pages yourself.
2. **Eternal (Zomato, Blinkit, District) posts no jobs**; it hires by referral.
3. **Apple and Amazon country filters** only know the countries in their small tables (Apple: India only).
4. **Eightfold** is detected only for Microsoft.
5. **By name**, these three are not found ("Amazon" typed alone is not tried against amazon.jobs). Paste the careers link: `https://www.amazon.jobs/`, `https://apply.careers.microsoft.com/careers?domain=microsoft.com`, `https://jobs.apple.com/en-in/search`.

## How to verify

```
go test ./internal/boards/
node scripts/deep-search.mjs "https://www.amazon.jobs/"
```
