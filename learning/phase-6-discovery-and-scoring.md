# Phase 6: Discovery and scoring

**Goal (from PLAN.md):** funding RSS, Hacker News thread, search queries, auto-add companies, fit score, optional LLM and Telegram.
**Done when:** the company list grows each session without manual additions.

## What I built, step by step

1. **Checked every source against a live response first** (same rule as the board feeds), and saved trimmed copies in `internal/discover/testdata/`.

   | Source | URL | Result of the check |
   | --- | --- | --- |
   | Inc42 | `inc42.com/feed/` | RSS, 24 items |
   | YourStory | `yourstory.com/feed` | RSS, 20 items |
   | TechCrunch venture | `techcrunch.com/category/venture/feed/` | RSS, 20 items |
   | Entrackr | `entrackr.com/feed` | **404**. Kept in the list under `/rss`; a failing feed is reported in the Live view and skipped |
   | Hacker News | `hn.algolia.com/api/v1/` | JSON; the monthly thread is found by author `whoishiring` |
   | Y Combinator | `yc-oss.github.io/api/companies/hiring.json` | JSON export of the public directory, 1,487 hiring companies |

2. **Sources (`internal/discover/sources.go`).** Each source only returns *candidates* (a name, sometimes a website or a board link, the headline it came from, a funding stage).
   - Funding news: `NameFromHeadline` pulls the company out of "X raises/bags/secures $N ...".
   - Hacker News: top-level comments start "Company | Role | Location"; only postings mentioning India or worldwide remote are kept; a board link in the comment is used directly.
   - YC: companies marked hiring with India in their locations.
   - Search (only if SearXNG is configured): queries built from your own settings, such as "software engineer intern hyderabad careers".
   - Outbound links: while crawling a company's site, any link to *another* company's job board becomes a candidate. Free by-product.
3. **A `candidates` table** (migration 0006). Sources insert; a name is queued once, ever.
4. **The discovery loop (`internal/services/discovery.go`)**, run by the leader scheduler:
   - it waits until the polling queue for known companies is nearly empty (known companies come first, as the plan says)
   - every 6 hours it reads the sources
   - every round it resolves 3 candidates through **the same resolver as deep search** and adds the ones that resolve
5. **Fit score.** Already rule-based since Phase 2. This phase added the two optional extras, both off unless configured:
   - **LLM second opinion (`internal/llm`)**: any OpenAI-compatible endpoint. For a new matching posting that the rules already score 50+, it returns a score and one line of reasoning; the final score is the average of the two.
   - **Telegram alert**: one message per new posting at or above the chosen fit score.

## Design decisions worth remembering

- **Sources produce names; one resolver turns names into companies.** Five sources, one code path for "find the site, find the careers page, detect the board". A new source is about 30 lines.
- **Discovery is slow on purpose.** Three candidates per round, only when the queue is quiet. It fills the idle part of a session and never competes with polling.
- **No guessing when unsure.** If two websites could be the company, deep search asks you. Discovery has nobody to ask, so it marks the candidate failed with that reason rather than add the wrong company.
- **The LLM score is stored separately** (`jobs.llm_score`), not baked into `fit_score`. Changing a setting re-scores every job by rule and re-blends the stored LLM score; no second LLM call.
- **Optional means optional.** `llm.New` returns nil when no endpoint is configured and every caller treats nil as "off". Tests run without any network or key.
- **A Telegram bot token is part of the request URL**, so the raw HTTP error is never logged; only "telegram request failed".

## Problems I hit and how I solved them

### 1. Headlines about investors look just like headlines about startups
- **Symptom:** "Norwest" was added as a company, from "Norwest closes $4 Bn global fund, takes AUM to $18 Bn".
- **Cause:** "X closes $N" matches a startup closing a round and a VC closing a fund equally.
- **Fix:** headlines that mention a "fund" or "AUM" are skipped; names containing "Ventures", "Capital", "Partners" were already rejected. Tests hold both the false positive and a true positive that contains "funds" ("led by Amazon funds").
- **Lesson:** I found it by reading the first real batch of candidates, not by thinking harder beforehand. Budget time to look at real output.

### 2. Descriptive words in front of the name
- "Credit-led fintech startup Olyv raises $6M" must give "Olyv". A lead-in pattern strips everything up to words like *startup, firm, platform, -based, -backed*. After that, a name longer than four words or not starting with a capital is rejected as "probably a sentence".

### 3. Hacker News comments that ignore the convention
- **Symptom in the fixture test:** a candidate named "Location: REMOTE".
- **Fix:** names containing `:` `/` `@`, or starting with words like remote, hiring, location, are dropped.

### 4. "Remote" is not a useful filter for YC
- **Measured on the fixture:** most of the directory lists "Remote / Partly Remote" as a region, so "India or remote" would have queued over a thousand companies.
- **Fix:** YC candidates must list India.

### 5. The plan named "Y Combinator company directory, public pages"
- The directory website loads its data through a search API with a key embedded in the page, which is fragile to scrape. I used the community JSON export of the same public data instead, and wrote the choice down here because it is a deviation from the plan's wording.

### 6. Most discovered companies need whole-site crawling
- **Observed in the first run:** of 23 companies added, 4 were on a known board; the rest became "custom site" crawls. Several of those found no jobs because their careers pages are built by JavaScript.
- **What to do about it:** run the renderer (`docker compose --profile render up -d`), and add parsers for the boards that keep appearing (Keka, Darwinbox, Recruitee, Workable), exactly as the plan's risk table says: "add parsers one at a time as they show up in the data".

## How to verify

```
docker compose up -d --build
```
Leave it running. In the **Live** screen, `discover` lines appear once known companies are polled:
"funding news: inc42.com · 24 names read, 3 new candidates", then one line per resolved candidate.

```
docker compose exec postgres psql -U jobradar -d jobradar -c \
  "select source, count(*) from companies group by 1 order by 2 desc"
docker compose exec postgres psql -U jobradar -d jobradar -c \
  "select name, source, status, note from candidates order by id desc limit 20"
```

`go test ./internal/discover/ ./internal/llm/` covers headline parsing, each source's parser on its saved fixture, and the LLM client against a fake server.

## Result (17 Nov 2026)

First session after deployment, with no manual additions, discovery added 35 companies to the 481 seeds: 18 from the YC directory, 10 from Hacker News, 6 from funding news (one of those, a job aggregator, I later removed by hand, and one investor slipped in before the fix in problem 1). 24 candidates failed with a recorded reason (no website found, no careers page, or ambiguous), 1 was already known, and 17 were still queued when I stopped.

Not exercised: the LLM pass and the Telegram alert, because no key or token is configured on this machine. Their code paths are covered by unit tests only (the LLM client against a fake HTTP server; Telegram has no test).
