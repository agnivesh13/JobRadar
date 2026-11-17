# JobRadar

A Go crawler that runs in Docker on a laptop, finds job postings on company career sites, keeps the ones that match your profile and salary floor, and shows them in a web interface with a live crawl view and an application tracker.

The design is in [PLAN.md](PLAN.md). How it was built, phase by phase, with every problem hit and its fix, is in [learning/](learning/).

## Deploy

Everything runs in Docker, so the only requirement is Docker. You do not need Go or a database on your machine.

### What you need

- **Docker** with Compose v2.24 or newer: Docker Desktop on Windows or macOS, or Docker Engine with the compose plugin on Linux. Check with `docker compose version`.
- **Git**, to clone the repository.
- **Node.js 18 or newer**, only for the helper scripts in `scripts/` (loading a resume, running a deep search from the terminal). The app itself does not need it.
- Free ports **8080** (web interface), **55432** (Postgres) and **4222** (NATS). You can change all three (step 2).

### 1. Get the code

```
git clone https://github.com/agnivesh13/JobRadar.git
cd JobRadar
```

### 2. Configure (optional)

Every setting has a default, so you can skip this step. To change anything, copy the example file and edit the copy:

```
cp deploy/.env.example deploy/.env          # Windows: copy deploy\.env.example deploy\.env
```

The settings most people change:

| Setting | Why |
| --- | --- |
| `CRAWLER_CONTACT` | An email address or URL of yours. It is sent to every site in the User-Agent so a site owner can reach you. Please set it. |
| `WORKDAY_COUNTRY` | The country to narrow large career sites to (default `India`). Leave it empty to crawl every country. |
| `API_HOST_PORT`, `POSTGRES_HOST_PORT`, `NATS_HOST_PORT` | Change them if something else already uses 8080, 55432 or 4222. |
| `FETCHER_REPLICAS` and the other worker counts | Fewer workers on a small machine. Crawling stays at one request per second per site either way. |

`deploy/.env` is ignored by git, so your settings and keys stay out of the repository.

### 3. Start

```
docker compose up -d --build
```

The first build takes a few minutes: it compiles the Go binary and the web interface into one image. Database migrations run on their own before the workers start. To check that it is up:

```
docker compose ps                           # every service should be "running" or "healthy"
curl http://localhost:8080/healthz          # {"nats":"ok","postgres":"ok","service":"api"}
```

### 4. Load the starting company list (first run only)

```
docker compose run --rm migrate seed        # loads seeds/companies.csv
```

You can skip this and add companies yourself on the Companies screen, by name or by pasting a careers link.

### 5. Set your profile

Open **http://localhost:8080** and go to **Settings**: salary floor, role keywords, levels, locations and graduation year. The defaults are for a fresher graduating in 2027, so change them to match you.

For the fit score, you can also load your resume as plain text:

```
node scripts/set-resume.mjs path/to/resume.txt
```

The resume is stored only in your local database. It is never sent anywhere unless you switch on the optional LLM (see "Optional extras").

The scheduler starts crawling by itself. Watch it on the **Live** screen; matching postings appear under **Jobs**.

### Stop, update, remove

```
docker compose stop                         # workers finish their current task; work resumes on the next start
docker compose start                        # start again

git pull                                    # update to the latest version
docker compose up -d --build                # rebuild; new migrations run on their own

docker compose logs -f fetcher              # follow one service's log (api, scheduler, extractor, scorer, ...)

docker compose down                         # remove the containers; your data is kept
docker compose down -v                      # remove the containers AND all data: jobs, tracker, settings
```

### Running it on a server

The web interface has **no login**, so the ports are bound to `127.0.0.1` and only reachable from the machine itself. To use it on a remote server, keep it that way and open an SSH tunnel from your own computer:

```
ssh -L 8080:localhost:8080 you@your-server
```

Then open http://localhost:8080 on your computer. Do not expose port 8080 to the internet without putting an authenticating reverse proxy in front of it: anyone who reaches it can read your tracker and change your settings.

### If something goes wrong

| Symptom | Fix |
| --- | --- |
| `port is already allocated` | Another program uses that port. Set a different `*_HOST_PORT` in `deploy/.env` and run `docker compose up -d` again. |
| `include` or `env_file` errors | Docker Compose is older than v2.24. Update Docker. |
| The page loads but nothing is crawled | Check the Live screen for errors. Make sure there are companies (step 4) and that crawling is not paused on the Live screen. |
| A company shows "manual" | The site blocked the crawler or its robots.txt forbids it. JobRadar does not work around that; check that company by hand. |
| Pages that need JavaScript return no jobs | Start the optional headless browser: `docker compose --profile render up -d`. |
| Anything else | `docker compose logs <service>` shows the error; `docker compose run --rm migrate verify` checks the queue and the database for lost or duplicated work. |

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
