# Phase 11: banks that hire in India

**Not in PLAN.md.** Asked for after Phase 10: "ICICI, Axis, IDFC, HDFC, and all banks in or outside India that hire in India".
**Done when:** each bank is crawled, or marked manual with a reason.

## Outcome (21 Nov 2026)

46 banks and card companies were looked up through the app's own search, then by domain, then by hand.

| How it is read | Banks |
| --- | --- |
| Workday | Barclays, Citi, Visa, Mastercard, BlackRock, DBS, Mizuho, Truist, ING, Capital One, U.S. Bank, State Street, Bank of America, Lloyds, Commonwealth Bank, Synchrony, Morgan Stanley, Fidelity, Northern Trust, MUFG |
| Oracle Recruiting Cloud | BNY, Kotak Mahindra Bank |
| Eightfold (older API, **new**) | HSBC |
| Phenom (**new reader**) | IDFC FIRST Bank |
| Sitemap + job pages (**new reader**) | Standard Chartered, Nomura (SuccessFactors), Société Générale (JSON-LD) |
| Site crawl of the careers page | ICICI Bank |
| **Manual**: blocks the crawler (403 or no answer) | IndusInd, Yes Bank, AU Small Finance Bank, BNP Paribas, Wells Fargo, American Express (its Eightfold API answers with an HTML page) |
| **Manual**: job list built by scripts, with no public data to read | HDFC Bank, Axis Bank, Bandhan, RBL, Federal Bank (Zappyhire), NatWest, Deutsche Bank, ANZ, Charles Schwab |
| **Manual**: needs a session cookie to page | UBS (BrassRing: only the newest 50 jobs come without one) |
| **Manual**: job pages need one more layout reader | Macquarie (Avature) |
| **Manual**: recruits by exam notices, not job postings | State Bank of India |
| Already manual | J.P. Morgan (Phase 10) |

## Problems I hit and how I solved them

### 1. Forty-six searches at once
- **Seen:** 23 of 46 name searches failed with "could not find a website".
- **Cause:** the web search behind name lookup was flooded. Retried by domain (kotak.com, db.com, ...), almost all resolved.
- **Lesson:** batch lookups of an outside service in small groups.

### 2. A bank's careers page is not its job list
- Site crawls of hsbc.com/careers, ubs.com, macquarie.com, nomura.com, anz.com.au, axis.bank.in gave programme pages and staff stories ("Graduate programmes", "Careers FAQs"), stored as jobs.
- **Fix:** found each bank's real job system by scanning its careers pages for known platform links (myworkdayjobs, eightfold, oraclecloud, successfactors, taleo, phenom, ...). Where a system has a reader, the company points at it; where the job search page is plain HTML (Nomura, Macquarie), the site crawl starts there; otherwise manual. The 43 wrong rows were closed.
- Same lesson as Phase 10 problem 5: read a site crawl's first titles before trusting its count.

### 3. HSBC's Eightfold has PCSX turned off
- **Seen:** `{"message": "PCSX is not enabled for this user."}` from /api/pcsx/search.
- **Cause:** the tenant serves Eightfold's older API, `/api/apply/v2/jobs` (allowed by its robots.txt), with the list at the top level (`count`, `positions`) and the description in `job_description`.
- **Fix:** an Eightfold host can be marked `applyV2`; list and detail URLs and parsing follow it. A page that reports positions but holds none is now an error for both APIs. Fixtures are from the exact URLs the code builds.

### 4. Names from domain searches
- A search by domain names the company after the domain ("Sc", "Db", "Jobs"). Renamed by SQL.

### 5. Three more readers, asked for after the first pass
- **Phenom (IDFC FIRST):** the page is built in the browser, but the data it shows is in the HTML as `phApp.ddo = {...}`. The reader takes that object (string-aware brace matching, not a regex) from the list page (`search-results?from=N`, 10 a page) and from each job page (`/job/{jobSeqNo}`, which works without the slug).
- **Sitemap + job pages (Standard Chartered, Nomura, Société Générale):** their search pages call paths robots.txt disallows (`/services/`, `/search`), but their sitemaps list every job page and the pages are plain HTML. A job page is read from schema.org JobPosting data when present, otherwise from SuccessFactors' `itemprop` title and description and its labelled fields.
- The JobPosting reader moved from the site crawler into the boards package (`boards.JobPostings`, `CleanURL`, `SitemapURLs`) so both use one copy.
- **UBS (BrassRing)** was checked and left manual: page 2 of its search needs the session cookie of page 1, and it has no job sitemap.

### 6. The fixtures caught one shape difference
- **Seen:** `cannot unmarshal object into .jobDetail.data.job.multi_location.0 of type string`.
- **Cause:** Phenom's `multi_location` is a list of strings on the list page and a list of objects on the job page. Fixed; the test reads the real job page.
- Phenom's job data also carries recruiter names and e-mail addresses; the reader takes only the posting's fields and the fixtures are trimmed to those.

### 7. Live checks after the readers
- Société Générale HTML-escapes titles inside JSON ("Reporting &amp; Data"); titles are now unescaped (for site crawls too), and the 47 escaped rows were closed so the next crawl stores them again.
- Standard Chartered gives the country as an ISO code ("Bangalore, IND"), Nomura as "Country: IN"; both are spelled out so the location filter sees "India".

## Known gaps

1. Taleo has no reader yet (no bank needs it now); Avature (Macquarie) has none either.
2. ICICI's site crawl finds only a few roles, with no clear location.
3. Most bank roles in India are sales or branch roles, and senior; expect few matches for a fresher outside Barclays, Visa, Citi, BlackRock and HSBC's technology roles.

## How to verify

```
go test ./internal/boards/ -run "Eightfold|Phenom|Sitemap|JobPostings"
```
