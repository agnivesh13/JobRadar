# Phase 10: twenty more companies for a 2027 CSE graduate

**Not in PLAN.md.** Asked for after Phase 9, from a list: NVIDIA, Adobe, Salesforce, Uber, Atlassian, LinkedIn, Walmart Global Tech, Cisco, Qualcomm, Oracle, ServiceNow, Intuit, Flipkart, PhonePe, PayPal, Rubrik, SAP Labs, J.P. Morgan, Goldman Sachs, D. E. Shaw.
**Done when:** each one is crawled, or there is a clear reason it cannot be.

## Outcome (17 Nov 2026)

| Company | How it is read | Result |
| --- | --- | --- |
| NVIDIA, Cisco, Rubrik | Already in the list (Workday, Workday, Greenhouse) | Unchanged |
| Adobe, PayPal, Salesforce | Found by name on Workday | Salesforce has 9 career sites (including Futureforce new-grad and internships); all were added |
| ServiceNow | Found by name on SmartRecruiters | |
| PhonePe | SmartRecruiters as `PHONEPELIMITED`, found through its own careers page (below) | |
| Qualcomm | **Eightfold**, the same API as Microsoft (careers.qualcomm.com) | Its Workday site still exists but is empty |
| Atlassian | **New board**: one JSON feed with every opening | 266 worldwide, 25 in India |
| Oracle | **New board**: Oracle Recruiting Cloud REST API | 328 India postings |
| D. E. Shaw | Site crawl of **deshawindia.com** (the India arm) | deshaw.com lists mostly New York and London roles without locations |
| Intuit | Site crawl of its India page on jobs.intuit.com | 28 India postings |
| Walmart | Site crawl of careers.walmart.com | Its search pages and API are disallowed by robots.txt; only what the allowed pages link to is found |
| Uber | **Not crawlable** | robots.txt disallows `*/api/`, which serves its job search |
| Flipkart | **Not crawlable** | Jobs are on TurboHire, whose robots.txt disallows every bot except Googlebot |
| J.P. Morgan | **Manual** | Its Oracle host answers "W4S-402: Blocked by WAF4SaaS" |
| SAP | **Not crawlable** | jobs.sap.com answers with a Cloudflare challenge page |
| Goldman Sachs | **Not crawlable** | robots.txt allows only `/roles/`; the list is built by scripts under `/_next/`, which are disallowed |
| LinkedIn | **Never crawled** | Banned by the project's crawling rules |

## Problems I hit and how I solved them

### 1. "Uber" matched someone's test account
- **Symptom:** Uber "found on smartrecruiters as uber (1 jobs listed)"; the one job was "Test UAT".
- **Cause:** guessing a slug from a name finds whatever account has that slug.
- **Fix:** a guessed board whose every posting title looks like a test ("test", "UAT", "dummy", "sample") is ignored (`onlyPlaceholders` in resolve.go, with a test).

### 2. Qualcomm on Workday returned 0 jobs
- **Seen:** `{"total":0,"jobPostings":[]}` from qualcomm.wd12.
- **Cause:** Qualcomm moved to Eightfold. careers.qualcomm.com has the same robots.txt (`Allow: /api/pcsx`) and the same responses as Microsoft's.
- **Fix:** Eightfold hosts are now a small table (host → domain, name) instead of Microsoft hard-coded.

### 3. Oracle's API puts `;` in the query string
- **Seen:** `finder=findReqs;siteNumber=CX_45001,limit=25,offset=0`.
- **Cause:** since Go 1.17, `url.Query()` drops any pair containing `;`, so the finder would vanish.
- **Fix:** `oracleFinder` reads the raw query. Nothing in the pipeline re-encodes board URLs (checked: the only rewrite is in the site-page link cleaner).

### 4. Oracle's "India" search is a text search
- `keyword=India` gave 47 hits. The LOCATIONS facet on the first page has an "India" entry (328) whose `Id` is the real filter, used the same way as Workday's country facet.

### 5. A site crawl that found the wrong things
- Intuit's and JPMorgan's careers pages yielded only "Recruitment fraud", "Hiring process" and programme pages; one programme page even matched.
- **Fix:** Intuit now starts from its India job list page. JPMorgan is manual, and its 7 page entries were closed.
- **Lesson:** a site crawl's first results must be read by title before trusting the count.

### 6. PhonePe's careers page has no jobs in its HTML
- The page is a Gatsby app; its script fetches `/apollo/job-postings/latest.json`, keeps rows with `status: "PUBLIC"`, and every apply link points to `jobs.smartrecruiters.com/PHONEPELIMITED/…`. So PhonePe is read through the existing SmartRecruiters board.

### 7. Adding a board meant editing four lists
- The pipeline, the API and the board package each kept their own list of board types.
- **Fix:** `boards.Feed` and `boards.Known`; `TaskFor` and the API ask the board package.

### 8. Oracle's first live crawl: "0 jobs, 0 links", and the tests passed
- **Cause:** my probes asked for `expand=requisitionList…`; the reader did not. Without it Oracle returns the count and an empty facet but no postings. The fixtures came from the probes, so the tests could not see it.
- **Fix:** the list URL asks for `expand=requisitionList.secondaryLocations,locationsFacet`, and a page that reports postings but holds none is now an error instead of a silent empty list.
- **Lesson:** a fixture must come from the exact URL the code builds. Replaying the crawler's own logged URL found the difference in one step.

### 9. Switching a company's source leaves the old rows open
- Atlassian's earlier site crawl had stored the same posting pages without locations, so they had other fingerprints: 525 open jobs for 266 postings.
- **Why:** by design a job closes only after missing two crawls in a row. A second feed crawl closes them. For D. E. Shaw and Intuit, rows from the old site (deshaw.com, intuit.com/careers) were closed directly.

### 10. "(4 to 8 Years)" in a title was not read
- PhonePe puts the experience in the title. The years rule read only description lines with a word like "experience", so these matched as level unknown.
- **Fix:** years in the title decide the level (lower bound). Rescoring now also re-runs the experience rules, because paged boards never fetch a known posting again, so a rule change would otherwise never reach it. "intern" is kept, as it can come from the employment type, which is not stored.

### 11. I wiped the saved settings, including the resume
- **What I did:** to trigger a rescore I sent GET /api/settings' response back to PUT. GET wraps the settings (`{"settings": …, "defaults": …}`); PUT decoded the wrapper as empty settings and saved them: no keywords, no resume. Matches went from 232 to 4,010.
- **Recovered:** the resume text was still in the session's scratch file (1,871 characters, the length logged when it was first saved), and the earlier save had used the built-in keywords (`set-resume.mjs --reset-keywords`), so the same command restored both.
- **Guard:** PUT now refuses a body with a `settings` key (test: `TestPutSettingsRefusesWrapper`). The web UI already sent the inner object.
- **Lesson:** never round-trip an API response into a write without checking its shape; and a write that can erase user data should refuse what it does not recognise.

### 12. Locations: India only
- Asked for during this phase. The location filter, which was "anywhere", now lists India and its main tech cities. Matches went from 3,561 to 269, all in India. A posting with no stated location still passes (many Indian company sites leave it blank).

## Known gaps

1. Uber, Flipkart, SAP and Goldman Sachs cannot be crawled under the project's rules; visit their careers pages yourself. J.P. Morgan is marked manual.
2. Oracle's display name is known only for Oracle's own site; other companies on Oracle Recruiting Cloud show their host's first label until renamed.
3. Atlassian's date is the posting's last update, which is the only date in the feed.

## How to verify

```
go test ./internal/boards/ ./internal/resolve/
```
