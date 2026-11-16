# Phase 2: Usable tool

**Goal (from PLAN.md):** salary parsing and three states, filters, Jobs screen, Tracker screen, Settings screen.
**Done when:** you can filter to 10 LPA and above, mark a job applied, and see it in the tracker.

## What I built, step by step

1. **Salary parser (`internal/salary`).** Two entry points:
   - `Normalize` for structured pay (Ashby's compensation, Lever's `salaryRange`, later `baseSalary` on web pages): currency x period -> rupees per year.
   - `Parse` for free text. It reads the description **line by line** and returns the first believable figure plus the line it came from (shown in the UI as "Salary read from: ...").
2. **Table-driven tests (`salary_test.go`).** 31 cases: the plan's five examples, Indian formats (LPA, lakh, crore, `₹12,00,000`, monthly stipends), foreign currency (yearly, monthly, hourly), and 11 lines that look like money but are not pay (funding, revenue, customer counts, perks).
3. **Scoring (`internal/score`).**
   - `Classify`: experience level from the title first (intern, senior, II, new grad), then from "N years" in the description.
   - `Evaluate`: one pure function that returns salary state, whether the posting passes the filters, flags, and a 0 to 100 fit score with a one-line reason.
   - `Judge`: wraps those for the pipeline, caches settings and company signals for 20 seconds, and can re-score every stored job.
4. **Settings (`score.Settings`)**, stored as one JSON document in the `settings` table. Defaults are tuned to the owner's profile: a 2027 graduate looking for fresher roles (see "Profile defaults" below).
5. **REST API (`internal/api`).** `GET /api/jobs` (tabs, filters, sort, paging), `GET /api/jobs/{id}`, `PUT /api/jobs/{id}/application`, `GET /api/tracker`, `GET/PUT /api/settings`, `GET /api/stats`. Saving settings starts a background re-score.
6. **Screens (`web/src`).** Jobs (tabs "Salary stated / Salary unknown / All matches", filters, a status picker in every row, a detail drawer with the description and tracker fields), Tracker (grouped by status, follow-up dates, closed postings still shown), Settings.
7. **`scripts/set-resume.mjs`**, which loads a resume text file into Settings through the API. The resume lives only in the database, never in the repo.

## Design decisions worth remembering

- **Three salary states, decided by the top of the range.** `above`, `below`, `unknown`. Internships are compared against the monthly stipend floor (x12), everything else against the LPA floor. A ₹50,000 a month internship is "above" even though it is under 10 LPA.
- **What is stored versus what is derived.** The parsed salary (rupees per year) and the experience level are facts about the posting, saved once at ingest. `salary_state`, `matched`, `flags` and `fit_score` depend on *settings*, so they are recomputed by `RescoreAll` whenever settings change. Because `Evaluate` is a pure function, re-scoring is just calling it again; no page is re-fetched.
- **Fit score = skills 40 + experience 25 + location 15 + salary 20.** Skills are the overlap between a curated skill list found in the posting and the same list found in the resume. For salary-unknown postings the salary part uses the company signal from the plan: hand-set pay tier, another posting of the same company that states pay above the floor, funding stage. The score comes with its reason, so it can be argued with.
- **Line-by-line salary parsing with context words.** A figure counts only if its own line also contains a pay word (salary, CTC, stipend, compensation, ...), unless the unit is "LPA", which is pay by definition. Lines with funding or revenue words are skipped. This is what separates "CTC 12-18 LPA" from "trusted by 5 lakh customers".
- **Whole-word keyword matching on the title.** `sr` must not match `SRE`; `lead` must not match `leading`. Titles are normalised and padded with spaces, and keywords are searched as ` keyword `.

## Profile defaults (the "match my profile" request)

The owner asked that results match a fresher graduating in 2027. That became:

| Setting | Default | Why |
| --- | --- | --- |
| Experience | intern, new grad, 0 to 2 years | Mid and senior roles are filtered out |
| Graduation year | 2027, hide other batches | A posting open only to the 2026 batch cannot be applied to |
| Locations | Hyderabad, Bangalore, remote India | From the plan; editable |
| Role keywords | software, backend, full stack, ML, data, quant, devops and similar | From the resume's direction |
| Excluded | senior, staff, lead, manager, sales, design, support, operations and similar | Level and function filters |
| Salary floor | 10 LPA; stipend floor ₹40,000 a month | The plan's figures |

## Problems I hit and how I solved them

### 1. Sales targets were read as salaries
- **Symptom:** an Account Executive posting showed a salary of ₹2.2 Cr. The quoted line: "Land 5 or more new logos per year at a target ACV of $250K or more."
- **Cause:** "per year" is a pay-context word and "$250K" is a plausible yearly figure.
- **Fix:** added deal words (ACV, quota, logos, pipeline, bookings, ...) to the not-pay list, plus a test with that exact sentence.
- **Lesson:** I found this only by looking at real parsed output sorted by salary. Synthetic tests would never have invented that sentence.

### 2. "$150k-$175k CAD" was converted as US dollars
- **Fix:** a line that names a currency with no configured rate (CAD, AUD, SGD, ...) is skipped rather than guessed. Wrong numbers are worse than "unknown".

### 3. The role filter let non-engineering jobs through
- **Symptom:** top results for my profile included "UX Design - Intern", "Product Management Intern", "Deal Desk Analyst".
- **Cause:** my include list contained broad words (`intern`, `analyst`, `research`), so any internship matched.
- **Fix:** removed the level words from the *role* list (level is the experience filter's job), narrowed `analyst` and `research` to `product analyst`, `research engineer`, `researcher`, and added design, product management, operations, support and consulting to the exclusions.
- **Lesson:** keep two questions apart. "Is this my kind of role?" and "Is this my level?" are different filters; mixing them makes one leak into the other.

### 4. "Remote" from a US company passed as "remote India"
- **Symptom:** postings located simply "Remote" or "Anywhere in France" matched.
- **Fix:** a remote posting passes the "remote india" location only if its location names India or a region containing it (APAC, Asia, global, worldwide). Tests cover bare "Remote", "Remote (US)" and "Remote - NA, APAC, EMEA".

### 5. Jobs saved in Phase 1 had no experience or salary
- **Cause:** they were saved before scoring existed, and a re-crawl said "unchanged, skip extraction".
- **Fix:** `jobradar crawl -force` (and later `jobradar recrawl`): crawl now and extract again even if the page hash is unchanged. Needed after any parser improvement.

### 6. A settings save during a running re-score would have been lost
- **Risk I noticed while writing it:** "start a re-score unless one is running" silently drops the second save.
- **Fix:** a `pending` flag. A call that arrives mid-run makes the running pass repeat once it ends.

### 7. Truncating a string cut a character in half (found in Phase 3's run)
- **Symptom:** the scorer kept failing one posting with `invalid byte sequence for encoding "UTF8": 0xe2`.
- **Cause:** `text[:300]` cuts **bytes**. The 300th byte was the middle of a three-byte "–". Postgres rejects invalid UTF-8.
- **Fix:** `strings.ToValidUTF8(text[:300], "")` wherever text is truncated, and all web text is sanitised once more at the database boundary (invalid bytes and NUL removed).
- **Lesson:** in Go, slicing a string is slicing bytes. Any truncation of text from the web needs a validity repair.

## How to verify

```
docker compose up -d --build
node scripts/set-resume.mjs path/to/resume.txt
```
Then open http://localhost:8080:
1. **Jobs -> "Salary stated"** lists only postings whose stated pay reaches the floor. Change the floor in **Settings**, save, and the list changes after the re-score.
2. Set any row's status to **Applied**.
3. **Tracker** shows it under "Applied" with the applied date; open it to add a contact, notes and a follow-up date.

`go test ./internal/salary/ ./internal/score/` runs the 31 salary cases and the filter tests.

## Result

Checked on 17 Nov 2026 through the API and in the browser: marking a job applied stamped the applied date and it appeared in the tracker with notes and a follow-up date; saving settings re-scored about 40,000 postings.

With the defaults for a 2027 fresher and the resume loaded, 84 of about 40,700 open postings matched: 2 with stated salary above the floor and 82 with salary unknown. The top of the list was SDE I, SDE intern and 0 to 2 year software roles in Bangalore and Hyderabad. The small "salary stated" count is the plan's first risk showing up in real data: Indian postings rarely state pay.

### 8. Re-scoring took minutes (found at the very end)
- **Symptom:** after a settings save, "Re-scoring jobs" stayed on for over three minutes with one CPU core at 100%.
- **Causes:** the loop scored one job at a time, and every keyword was re-normalised with a regular expression for every job (about 80 regex calls per posting).
- **Fix:** each batch of 500 is scored across all cores, and normalised keywords are cached. 40,000 postings now take 37 seconds.
