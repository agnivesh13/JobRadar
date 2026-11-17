# Phase 8: Keka, Darwinbox and SmartRecruiters

**Not in PLAN.md.** Added on request after Phase 7: three more hiring products that companies host their careers pages on. Keka and Darwinbox are common among Indian companies; SmartRecruiters among large ones.
**Done when:** a company on each product is crawled from a pasted link, with descriptions, like any other board.

**Outcome: two of three.** Keka and SmartRecruiters work live. Darwinbox is built and tested but refuses the crawler (problem 2).

## What I checked before writing code (17 Nov 2026)

| | Keka | Darwinbox | SmartRecruiters |
| --- | --- | --- | --- |
| robots.txt | Disallows `/`, allows `/careers` (the feed is under it) | None (redirects to the login page) | The JSON API host disallows everyone but one named bot. The careers pages have none. |
| Job list | One GET returns every job with its description | Pages of 50, total on every page | HTML fragments: 49 postings, then 24 per page, empty past the end, no total |
| Description | In the list | One request per posting | One request per posting |
| Country filter | Not needed | Not needed | `search=India` (a text search that also matches the location) |
| Unknown company | Redirect to "TenantNotFound" | HTTP 500 "Invalid subdomain" | HTTP 404 |

Trimmed real responses are in `internal/boards/testdata/` (`keka.json`, `darwinbox_*.json`, `smartrecruiters_*`).

## What I built

1. **`internal/boards/keka.go`**: a plain feed, like Greenhouse. The job link is built from the feed's own address, so `boards.ParseFeed(board, feedURL, body)` was added next to `Parse`.
2. **`internal/boards/paged.go`**: Phase 7's Workday crawl (list pages claim posting pages, through the site frontier) made general. `Paged`, `IsListPage`, `ParseListPage` and `ParseJobPage` pick the parser by board type; the pipeline function is unchanged apart from calling these.
3. **`internal/boards/darwinbox.go`** and **`smartrecruiters.go`**: the list and posting parsers for the two paged boards.
4. **Detection**: links to all three are recognised anywhere a board link is (pasted into deep search, on a careers page, as an outbound link).
5. **By name**: typing a company name also tries Keka (`{name}.keka.com`) and SmartRecruiters, after Greenhouse, Lever and Ashby.

## Problems I hit and how I solved them

### 1. SmartRecruiters' API is closed to crawlers
- **Seen in the live check:** `api.smartrecruiters.com/robots.txt` is `Disallow: /` for every agent except one. That API is what I had planned to use.
- **Fix:** it is not used. The public careers page loads its list from `careers.smartrecruiters.com/{Company}/api/more`, and that host publishes no robots.txt. The crawler reads what a visitor's browser reads, at one request per second.

### 2. Darwinbox answers 403 to a client that says it is a crawler
- **Symptom:** the first live crawl: `site refused the crawler (http 403)`, although the same address had answered my probe a minute earlier.
- **Cause:** the only difference was the User-Agent. JobRadar's says what it is; Darwinbox's firewall turns that away.
- **Decision:** nothing was changed to get past it. The project rule is an honest User-Agent, and a site that blocks the crawler is marked `manual`. The parser stays (tested against the saved responses) in case Darwinbox opens up; Darwinbox is left out of the by-name guessing because it would only collect refusals.
- **Lesson:** a probe with a different User-Agent than the crawler's proves less than it seems to. Probe with the crawler's own identity.

### 3. The same address answered HTML to the probe and JSON to the crawler
- **Symptom:** SmartRecruiters listed 29 postings, then every posting failed with "no job title on the page".
- **Cause:** the posting page answers by the `Accept` header. The crawler asks for JSON first, so it got a JSON document; my parser was written for the HTML I had saved.
- **Fix:** both shapes are parsed (the JSON one is simpler), each with its own fixture.
- **Lesson:** same as problem 2. Two of three boards behaved differently for the real client than for the probe.

### 4. A Darwinbox location with a carriage return in it
- **Seen in the fixture:** `"Gurgaon, Haryana\r, India"`. Caught by the test, which expected `Gurgaon, Haryana, India`.
- **Fix:** collapse whitespace, then remove the space left before the comma.

## Result (17 Nov 2026)

| Check | Outcome |
| --- | --- |
| Keka: `https://inito.keka.com/careers/` | 1 request, 13 jobs, 1 matching the profile, 3 s. Inito was already in the list as a site crawl; it was switched to the feed in place. |
| SmartRecruiters: `https://careers.smartrecruiters.com/Freshworks` | 31 requests, 28 India postings (of 116 worldwide), 0 matching, 41 s |
| Darwinbox: `https://cars24.darwinbox.in/ms/candidate/careers` | Refused (403); company marked `manual` |

## Known gaps

1. **Darwinbox does not work** while it refuses declared crawlers (problem 2).
2. **Keka: only the default portal.** Named portals (`/careers/{portal}/`) are not read.
3. **SmartRecruiters' country filter is a text search.** It can include a posting elsewhere that mentions India. If it finds nothing at all, the whole list is read instead.
4. **SmartRecruiters has no total**, so a very large company is read until an empty page or the 2200-page cap.
5. **By name is a guess** at the company's address on each product, as with Workday (Phase 7, problem 9).
6. **No test covers the paged crawl itself** (it needs a database); it was verified by the live runs above.

## How to verify

```
node scripts/deep-search.mjs "https://inito.keka.com/careers/"
node scripts/deep-search.mjs "https://careers.smartrecruiters.com/Freshworks"
go test ./internal/boards/ ./internal/resolve/
```
