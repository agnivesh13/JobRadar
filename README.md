# JobRadar

A Go crawler that runs in Docker on a laptop, finds job postings on company career sites, keeps the ones that match your profile and salary floor, and shows them in a web interface with a live crawl view and an application tracker.

The design is in [PLAN.md](PLAN.md). How it was built, phase by phase, with every problem hit and its fix, is in [learning/](learning/).

## Start

```
copy deploy\.env.example deploy\.env      # optional: every setting has a default
docker compose up -d --build
docker compose run --rm migrate seed      # first run only: loads seeds/companies.csv
node scripts/set-resume.mjs resume.txt    # optional: resume text for the fit score
```

Open **http://localhost:8080**. The scheduler starts crawling by itself. Stop with `docker compose stop`; workers finish their current task and unfinished work resumes next time.

Set `CRAWLER_CONTACT` in `deploy/.env` to an email or URL of yours: it is sent in the User-Agent so a site owner can reach you.

## Screens

| Screen | What it does |
| --- | --- |
| Live | Events as pages move through schedule, fetch, extract and score; counters; pause, resume and speed |
| Jobs | Postings that match, in tabs "Salary stated" and "Salary unknown"; filter, sort, set a status |
| Tracker | Every job you acted on, grouped by status, with contact, notes and follow-up date |
| Companies | The company list; type a name for a deep search; crawl now, pay tier, remove |
| Settings | Salary floor, stipend floor, role keywords, levels, locations, graduation year, resume |

## Services

One Go binary, one image; the start command picks the role.

| Container | Role |
| --- | --- |
| `api` | Web interface, REST endpoints, live event stream (SSE), deep search |
| `scheduler` x2 | Decides what to crawl next and runs discovery; a Postgres lock makes one the leader |
| `fetcher` x3 | Downloads feeds and pages, politely (robots.txt, one request per second per domain) |
| `extractor` x2 | Turns feeds and pages into job records |
| `scorer` | Salary state, filters, fit score; saves jobs |
| `renderer` (optional) | Headless Chrome for JavaScript-only pages: `docker compose --profile render up -d` |
| `nats`, `postgres` | Queue (work in flight) and database (all state) |

## Commands

Run any of these as `docker compose run --rm migrate <command>`:

| Command | Does |
| --- | --- |
| `seed [file]` | Load companies from a CSV |
| `recrawl` | Re-crawl every company now and re-extract |
| `verify` | Check the invariants: queue drained, nothing lost, nothing duplicated |
| `queue` | Show queue state; `queue -dead` lists dead letters; `queue -reset` wipes the queue (safe) |
| `crawl` | Single-process crawl without the queue (debugging) |

`bash scripts/chaos.sh` kills containers during a crawl and then runs `verify`.

## Tests

```
go test ./...          # never touches the network; fixtures are saved real responses
```

## Rules the crawler follows

- Obeys robots.txt; at most one request per second per domain across all workers; backs off on 429 and 5xx.
- Honest User-Agent. No logins, CAPTCHA solving or proxy rotation; a site that blocks it is marked "manual".
- Never crawls LinkedIn, Naukri, Indeed, Glassdoor or Wellfound.
- Never applies or emails for you, and stores a contact only when you type one in.

## Optional extras

All off by default; set them in `deploy/.env`, then switch them on in Settings.

- **LLM** (`LLM_BASE_URL`, `LLM_API_KEY`, `LLM_MODEL`): any OpenAI-compatible endpoint (Groq, Ollama). Used as a second opinion on fit and as the last-resort reader for career pages nothing else understood.
- **Telegram** (`TELEGRAM_TOKEN`, `TELEGRAM_CHAT_ID`): a message for each new job above a fit score.
- **SearXNG** (`SEARXNG_URL`): a self-hosted search engine for discovery queries and for resolving company names.

## Credits

Written from scratch. Projects read for ideas while planning, none of whose code is used: go-jobs, JobOps, JobSync, openjobs, JF (links in PLAN.md).
