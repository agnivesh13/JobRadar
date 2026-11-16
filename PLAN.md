# JobRadar: build plan

Nov 16, 2026 · @Agnivesh

## Scope

JobRadar is a Go crawler that runs in Docker on a laptop, finds job postings on company career sites, keeps those at or above a salary floor (default 10 LPA), and shows them in a web interface with a live crawl view and an application tracker.

**What "entire internet" means here.** No laptop can crawl the whole web, and most of it has no jobs. The tool instead covers the places jobs are published, and keeps widening that set on its own:

- **Known companies:** a seed list of a few hundred startups and companies, polled every session.
- **Discovered companies:** new ones found each session from funding news, hiring threads and search queries, then added to the list automatically.
- **On-demand:** type any company name and it crawls that company's whole site for jobs.

Coverage grows every session. After a month the company list should be in the thousands.

**In scope**

- Crawl career pages and job-board software (Greenhouse, Lever, Ashby and similar) directly
- Salary floor, role, location and experience filters
- Live view of what is being crawled right now
- Results list and application tracker (not applied, applied, interview, offer, rejected)
- Deep search of one named company

**Out of scope**

- Scraping LinkedIn, Naukri or Indeed. Their terms forbid it and they block crawlers; use their own alerts alongside this tool.
- Auto-applying or auto-emailing. The tool finds and tracks; you apply.

## Architecture

Six small Go services talk through a message queue and share one Postgres database, all started by one Docker Compose file. Fetching, parsing and scoring are separate stages so each can fail, retry and scale independently.

| Service | Job | Notes |
| --- | --- | --- |
| `api` | Serves the web interface, REST endpoints and the live event stream | Go `net/http`; React + Tailwind front end; Server-Sent Events for live updates |
| `scheduler` | Decides what to crawl next and publishes fetch tasks | Only one active at a time (leader lock in Postgres); respects per-domain crawl intervals |
| `fetcher` (x3 to x5) | Downloads pages and API responses | Plain HTTP; per-domain rate limit shared across all fetchers; honours robots.txt |
| `renderer` (x1) | Loads JavaScript-heavy pages in headless Chrome | `chromedp`; used only when plain HTTP returns no job content |
| `extractor` (x2) | Turns raw pages into structured job records | Board-specific parsers first, generic parser second, LLM last |
| `scorer` (x1) | Applies filters, salary logic and fit score | Writes final jobs; emits "new match" events |
| NATS JetStream | Queue between stages | Durable streams, acknowledgements, retries, dead-letter subject |
| Postgres | All state | Companies, pages, jobs, applications, crawl log |

**Flow of one page**

1. `scheduler` publishes a fetch task: URL, company, priority.
2. A `fetcher` takes it, waits for its domain's rate-limit token, downloads, stores the raw body, publishes "fetched".
3. An `extractor` parses it. Job links found on a listing page become new fetch tasks. Job detail pages become job records.
4. `scorer` filters and scores each record, saves it, and emits an event.
5. `api` pushes every stage's events to the browser, which is the live view.

**The distributed-systems parts worth getting right**

- **At-least-once delivery with idempotent handlers:** a task may be delivered twice; a content hash makes the second run a no-op.
- **Leases:** a fetcher that dies mid-task loses its lease and the task is redelivered.
- **Distributed rate limiting:** five fetchers must together stay under one request per second per domain. Token bucket held in NATS key-value or Postgres.
- **Back-pressure:** if extractors fall behind, the scheduler slows down instead of filling the queue.
- **Leader election:** two schedulers can run; only the lock holder publishes.
- **Crash recovery:** kill any container mid-crawl and no job is lost or duplicated. This is the demo for interviews.

## Discovery

Jobs come from two loops: polling companies already known, and finding companies not yet known. The second loop is what makes coverage keep growing.

**Loop 1: poll known companies**

Most startups host their careers page on a handful of job-board products, and each exposes a public JSON feed per company. One parser per product covers thousands of companies.

| Source type | How it is read | Salary present |
| --- | --- | --- |
| Greenhouse boards | Public JSON feed per company | Sometimes |
| Lever boards | Public JSON feed per company | Sometimes |
| Ashby boards | Public JSON feed per company | Often |
| Other board software (Workable, SmartRecruiters, Zoho Recruit, Keka, Darwinbox) | Add parsers one at a time as they show up in the data | Varies |
| Custom career pages | Generic parser: `JobPosting` structured data embedded in the page, then sitemap, then link-following | Rare |

Confirm each feed's exact URL and response shape against its current documentation in Phase 1; do not code them from memory.

**Loop 2: find new companies**

| Source | What it gives | Access |
| --- | --- | --- |
| Funding news (Inc42, Entrackr, YourStory, TechCrunch) | Companies that just raised and will hire | RSS |
| Hacker News "Who is hiring" monthly thread | Company names, roles, often a contact | Public search API |
| Y Combinator company directory | Startups by batch and location | Public pages |
| Search queries ("software engineer intern Hyderabad careers") | Career pages of companies nobody listed | Self-hosted SearXNG container, or a search API free tier |
| Outbound links | Every crawled job page that links to another company's board | Free by-product of crawling |

Each discovered name goes through the same resolver as the company deep search below: find the site, find the careers page, detect the board software, save it. From then on Loop 1 polls it.

**Seed list.** Start with about 300 companies in a CSV: the ones you want, plus public lists of YC and Indian funded startups. Everything else is discovered.

## Company deep search

Typing a company name starts a high-priority crawl of that company's whole site, and results appear in the live view within a minute or two.

1. **Resolve the name to a domain.** Search for "\<name> careers"; take the official site. If two candidates look plausible, the interface asks you to pick.
2. **Find the careers entry point.** Try `/careers`, `/jobs`, `/join-us`, the sitemap, and footer links on the home page.
3. **Detect the board software.** If the careers page loads or links to a known board, switch to that board's JSON feed and finish in one request.
4. **Otherwise crawl the site.** Breadth-first from the careers page, same domain only, depth limit 4, page limit 500, skipping blog, docs and press paths.
5. **Render if needed.** Pages that return no job content over plain HTTP go to the headless Chrome `renderer`.
6. **Extract.** Structured data first, then HTML patterns, then an LLM on the page text as the last resort.
7. **Save the company.** It joins the known list, so later sessions poll it automatically.

Deep-search tasks carry a higher priority than background polling, so they jump the queue without stopping it.

## Filtering

The salary floor is the hardest filter, because most Indian postings do not state pay. Every job therefore lands in one of three salary states, and a setting decides whether unknowns are shown.

| State | Meaning | Default treatment |
| --- | --- | --- |
| Stated, at or above floor | Posting gives a figure or range whose top is at least the floor | Shown |
| Stated, below floor | Posting gives a figure below the floor | Hidden |
| Unknown | No pay in the posting | Shown in a separate "salary unknown" tab, ranked by company signal |

Hiding unknowns entirely would throw away most good roles, so the default keeps them visible but separate.

**Salary parsing**

- Normalise everything to rupees per year: "12-18 LPA", "₹12,00,000", "18L CTC", "$90k" (converted at a configurable rate), monthly figures multiplied by 12.
- Read the structured `baseSalary` field when a page has one; otherwise regex over the text; otherwise ask the LLM to extract a figure and quote the sentence it came from.
- Internships are tracked as monthly stipend with their own floor (for example ₹40,000 a month), since "10 LPA" does not apply.

**Company signal for unknown-salary jobs**

- A `pay_tier` field per company that you set by hand as you learn (high, medium, low, unknown).
- Any other posting from the same company that did state pay.
- Funding stage from the discovery source.

**Other filters, all editable in the interface**

- Role keywords to include (software engineer, backend, SDE, ML, quant, data, product analyst) and exclude (senior, staff, lead, 5+ years)
- Experience: intern, new graduate, 0 to 2 years
- Location: Hyderabad, Bangalore, remote India, anywhere
- Graduation year: flag postings that name a batch other than 2027
- Posted date: last 1, 3, 7 or 30 days

**Fit score (0 to 100).** A weighted sum of keyword overlap with your resume, experience match, location match and salary state. Optionally add an LLM pass (Groq free tier, or a local model through Ollama) that reads the posting against your resume and returns a score plus one line of reasoning. The tool must work with the LLM switched off.

## Data model

Six Postgres tables hold everything; the queue holds only work in flight, so wiping the queue loses nothing.

| Table | Key columns | Purpose |
| --- | --- | --- |
| `companies` | id, name, domain, careers\_url, board\_type, board\_token, pay\_tier, source, last\_crawled\_at, crawl\_interval | One row per company; drives the scheduler |
| `pages` | id, company\_id, url, content\_hash, http\_status, fetched\_at, rendered | Every URL fetched; the hash skips unchanged pages |
| `jobs` | id, company\_id, url, title, location, experience, salary\_min, salary\_max, salary\_state, description, posted\_at, first\_seen\_at, last\_seen\_at, closed\_at, fit\_score, fingerprint | One row per posting |
| `applications` | job\_id, status, applied\_at, contact\_name, contact\_email, notes, follow\_up\_on | Your tracker; one row per job you act on |
| `crawl_events` | id, ts, service, company\_id, url, stage, outcome, duration\_ms | Feeds the live view and the history page |
| `settings` | key, value | Salary floor, filters, resume text, LLM on or off |

**Dedup rules**

- **Same page twice:** `content_hash` matches, so extraction is skipped.
- **Same job, different URL** (board link and company-site link): `fingerprint` = hash of company, normalised title and location. One row, both URLs kept.
- **Job disappears:** not seen for two consecutive crawls of that company sets `closed_at`; it stays in the tracker but leaves the results list.
- **Job changes:** same fingerprint, new description hash updates the row and flags "updated".

## Interface

A single-page React + Tailwind app served by `api` at `localhost:8080`, with five screens.

| Screen | Shows | Actions |
| --- | --- | --- |
| Live | Scrolling feed of crawl events (company, URL, stage, result), counters for pages fetched, jobs found, matches, errors, queue depth and active workers | Pause, resume, change worker speed |
| Jobs | Matching postings, newest first, with tabs for "salary stated" and "salary unknown"; columns for company, title, location, salary, fit score, first seen | Filter, sort, open posting, mark applied, skip, save for later |
| Tracker | Every job you acted on, grouped by status | Change status, add contact and notes, set a follow-up date |
| Companies | Known companies with board type, last crawl, open matching roles, pay tier | Search box for deep search, add or remove company, set pay tier, crawl now |
| Settings | Salary floor, stipend floor, keywords, locations, resume text, LLM switch | Edit and save; re-scores existing jobs |

**Live view mechanics.** Each service writes events to a NATS subject; `api` relays them to the browser over Server-Sent Events. The feed is capped at the last 500 lines in the browser and the full log lives in `crawl_events`.

**Tracker statuses.** Not applied, Saved, Applied, Replied, Interview, Offer, Rejected, Skipped. A job's status badge shows everywhere the job appears, so the Jobs screen always tells you applied or not applied at a glance.

**Deep search in the interface.** Typing a name in the Companies search box opens a panel that streams that crawl only: domain resolved, careers page found, board detected, pages crawled, jobs found.

**Optional.** Telegram message for any new job with fit score above 80, so you need not keep the tab open.

## Running it

`docker compose up` starts everything, and a 3 to 5 hour session is enough to re-check every known company and run discovery.

**Session behaviour**

- **On start:** the scheduler orders companies by how overdue they are and works through the list. Board feeds take one request each, so 1,000 companies on known boards finish in under 20 minutes at polite rates.
- **Then:** discovery sources run, new companies are resolved, and custom career sites are crawled in the remaining time.
- **On stop:** `docker compose stop` lets workers finish their current task. Unfinished tasks stay in the durable queue and resume next session.
- **All-day mode:** the same setup simply keeps cycling; each company has a crawl interval (6 hours for boards, 24 hours for custom sites), so nothing is hammered.

**Compose layout**

| Container | Image | Volume |
| --- | --- | --- |
| `postgres` | postgres:16 | `pgdata` (the only state that matters) |
| `nats` | nats with JetStream on | `natsdata` |
| `api`, `scheduler`, `fetcher`, `extractor`, `scorer` | One Go image, different start command | none |
| `renderer` | Go image plus headless Chromium | none |
| `searxng` (optional) | searxng/searxng | config only |

**Laptop budget.** Roughly 1.5 to 2 GB of RAM with the renderer running, under 1 GB without it. Cap the renderer at two tabs. Disk grows by raw pages, so keep bodies for 7 days and then delete them, leaving the hashes.

**Config.** Everything in a `.env` file: salary floor, worker counts, rate limits, LLM key, Telegram token. No secrets in the repo. The same Compose file runs unchanged on a cloud VM if you ever want it on around the clock.

## Build phases

Seven phases, each ending in something that runs; the tool is useful for your job search from the end of Phase 2.

| Phase | Build | Done when |
| --- | --- | --- |
| 0. Skeleton | Repo, Compose file with Postgres and NATS, migrations, one Go binary with subcommands, health endpoints | `docker compose up` starts clean and `api` answers `/healthz` |
| 1. First jobs | Seed CSV loader; Greenhouse, Lever and Ashby parsers; single-process fetch, extract, save | 300 seed companies crawled; jobs visible with a SQL query |
| 2. Usable tool | Salary parsing and three states, filters, Jobs screen, Tracker screen, Settings screen | You can filter to 10 LPA and above, mark a job applied, and see it in the tracker |
| 3. Distributed pipeline | Split into scheduler, fetcher, extractor, scorer over NATS; leases, retries, dead-letter, idempotency, shared rate limiter, leader lock | Killing any container mid-crawl loses no job and creates no duplicate; test script proves it |
| 4. Live view | `crawl_events`, Server-Sent Events relay, Live screen with counters and pause | You watch pages move through stages in real time |
| 5. Whole-site crawling | Generic parser (structured data, sitemap, link-following), headless renderer, company resolver, deep search box | Typing a company not in the list returns its jobs within two minutes |
| 6. Discovery and scoring | Funding RSS, Hacker News thread, search queries, auto-add companies, fit score, optional LLM and Telegram | Company list grows each session without manual additions |

**Repo layout**

```
jobradar/
  cmd/jobradar/          main.go with subcommands: api, scheduler, fetcher, extractor, scorer, renderer
  internal/
    boards/              greenhouse.go, lever.go, ashby.go, generic.go
    crawl/               frontier, robots, rate limiter, fetch client
    extract/             structured data, html patterns, llm fallback
    salary/              parser and normaliser, with table-driven tests
    score/               filters and fit score
    queue/               NATS wrappers: publish, consume, ack, dead-letter
    store/               Postgres queries and migrations
    events/              crawl event types and SSE relay
    resolve/             company name to domain to careers page to board
  web/                   React + Tailwind app
  deploy/                docker-compose.yml, Dockerfile, .env.example
  seeds/companies.csv
  scripts/chaos.sh       kills containers during a crawl and checks invariants
  PLAN.md                this document
  CLAUDE.md              conventions and commands for Claude Code
```

**Working in Claude Code**

1. Save this file as `PLAN.md` in an empty repo.
2. Ask Claude Code to write a short `CLAUDE.md` from it: stack, layout, how to run tests, the rules in the last section.
3. Work one phase per session: "Implement Phase 0 from PLAN.md, then stop and show me how to verify it."
4. Check the "done when" yourself before starting the next phase.
5. Have it write tests alongside each parser. Save real sample responses under `testdata/` so tests never hit the network.
6. Commit at the end of every phase.

**Resume line once Phase 3 passes:** "Distributed job-crawling pipeline in Go (NATS JetStream, Postgres, Docker): N companies, M pages per hour, zero lost or duplicated jobs under container failure." Fill N and M from your own runs.

## Rules and risks

The crawler stays polite and stops short of contacting anyone, which keeps your IP unblocked and your name clean with the companies you want to join.

**Crawling rules, enforced in code**

- Obey `robots.txt` for every domain; cache it for 24 hours.
- At most one request per second per domain across all workers, with backoff on 429 and 5xx responses.
- Identify the crawler honestly in the User-Agent string, with a contact address.
- No logins, no CAPTCHA solving, no proxy rotation. If a site blocks the crawler, mark it "manual" and move on.
- Never crawl LinkedIn, Naukri, Indeed, Glassdoor or Wellfound.

**Not automated, by design**

- Applications and emails. You send them yourself.
- Harvesting personal email addresses. The tracker stores a contact only when you type it in.

**Risks**

| Risk | Effect | Mitigation |
| --- | --- | --- |
| Most postings omit salary | The 10 LPA filter alone hides good roles | Three salary states, "unknown" tab, company pay tier |
| Custom career pages vary endlessly | Generic parser misses jobs | Board parsers cover the majority; log misses and add patterns weekly; LLM fallback |
| JavaScript-only sites | Empty pages over plain HTTP | Headless renderer, capped at two tabs |
| Board feeds change format | A parser silently returns nothing | Alert when a company that had jobs drops to zero; sample-response tests |
| Laptop only on a few hours | Roles posted overnight seen late | Overdue-first scheduling on start; optional move to a cloud VM |
| Scope creep | Never finishes | Phases 0 to 2 first; do not start Phase 3 until you are using the tool daily |

**Open questions**

- Which roles go in the default keyword list: SDE only, or also quant and product analyst?
- Internship stipend floor: what monthly figure?
- LLM scoring through Groq, a local model, or off for now?

## Existing projects to learn from

Several open-source tools cover parts of this plan, but none found covers the salary floor in LPA, whole-site deep search, a live crawl view, or a distributed pipeline. Use them as references and as a stopgap, not as the base.

| Project | What it is | Use it for |
| --- | --- | --- |
| [go-jobs](https://github.com/justEstif/go-jobs) | Self-hosted aggregator for Greenhouse, Lever and Ashby with web UI, CLI and pipeline tracker; runs in Docker | Closest match. Run it now for your own search; read its board parsers and company list before writing Phase 1 |
| [JobOps](https://github.com/DaKheera47/job-ops) | Self-hosted TypeScript pipeline: many boards, 0 to 100 fit score, CV tailoring, tracker; AGPLv3 with Commons Clause | Ideas for scoring and tracker screens. It scrapes LinkedIn and Indeed through JobSpy, which this plan avoids |
| [JobSync](https://github.com/Gsync/jobsync) | Self-hosted Next.js application tracker with AI assistant; MIT | Tracker and dashboard design |
| [openjobs](https://github.com/federicodeponte/openjobs) | Python library that extracts jobs from any careers page using Firecrawl and Gemini; MIT | The LLM-fallback approach for custom career pages in Phase 5 |
| [JF](https://github.com/compfaculty/jf) | Small Go project: boards plus career pages via chromedp, SQLite, web UI | A Go example of headless career-page scraping |
| [JobSpy](https://github.com/speedyapply/jobspy) | Python library scraping LinkedIn, Indeed, Glassdoor, Naukri | Not used: those sites block scrapers and forbid it |

**Rule for reuse.** Check each repo's licence before copying code, credit what you borrow in the README, and write the pipeline (queue, leases, rate limiter, scheduler) yourself, since that is the part you will be asked about in interviews.

Checked on 16 Nov 2026; star counts and activity change, so re-check before relying on any of them.
