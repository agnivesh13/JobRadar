# Phase 11: banks that hire in India

**Not in PLAN.md.** Asked for after Phase 10: "ICICI, Axis, IDFC, HDFC, and all banks in or outside India that hire in India".
**Done when:** each bank is crawled, or marked manual with a reason.

## Outcome (21 Nov 2026)

50 banks and card companies were looked up through the app's own search, then by domain, then by hand.

| How it is read | Banks |
| --- | --- |
| Workday | Barclays, Citi, Visa, Mastercard, BlackRock, DBS, Mizuho, Truist, ING, Capital One, U.S. Bank, State Street, Bank of America, Lloyds, Commonwealth Bank, Synchrony, Morgan Stanley, Fidelity, Northern Trust, MUFG |
| Oracle Recruiting Cloud | BNY, Kotak Mahindra Bank |
| Eightfold (older API, **new**) | HSBC |
| Site crawl of the job search page | ICICI Bank, Nomura (SuccessFactors search), Macquarie (Avature search) |
| **Manual**: blocks the crawler (403 or no answer) | IndusInd, Yes Bank, AU Small Finance Bank, BNP Paribas, Wells Fargo, American Express (its Eightfold API answers with an HTML page) |
| **Manual**: job list built by scripts or a system with no reader | HDFC Bank, Axis Bank, IDFC FIRST (Phenom), Bandhan, RBL, Federal Bank (Zappyhire), Standard Chartered (SuccessFactors, scripted), NatWest, Deutsche Bank, UBS, ANZ, Société Générale (Taleo), Charles Schwab |
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

## Known gaps

1. Phenom, SuccessFactors (scripted pages), Taleo and BrassRing have no reader. Building one would bring back IDFC FIRST, Standard Chartered, Société Générale and UBS.
2. ICICI's site crawl finds only a few roles, with no clear location.
3. Most bank roles in India are sales or branch roles, and senior; expect few matches for a fresher outside Barclays, Visa, Citi, BlackRock and HSBC's technology roles.

## How to verify

```
go test ./internal/boards/ -run Eightfold
```
