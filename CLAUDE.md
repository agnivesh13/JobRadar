# JobRadar

Go crawler that finds job postings on company career sites, filters by salary floor, and shows them in a web UI. Full design in [PLAN.md](PLAN.md). Build journal in [learning/](learning/).

## Stack

- Go 1.27, one binary (`cmd/jobradar`) with a subcommand per service
- Postgres 16 (all state), NATS JetStream (work in flight)
- React + Tailwind (Vite) in `web/`, served by the `api` service
- Docker Compose: `deploy/docker-compose.yml`, included by the root `compose.yaml`

## Commands

Go is installed at `C:\Program Files\Go\bin` but is not on PATH; add it first in PowerShell: `$env:Path += ";C:\Program Files\Go\bin"`.

```
docker compose up -d --build          # start everything (UI at http://localhost:8080)
docker compose stop                   # workers finish their current task
go build ./... ; go vet ./...         # compile check
go test ./...                         # unit tests; they never touch the network
cd web ; npm run build                # front end
```

Host ports: api 8080, Postgres **55432** (5432 and 5433 are used by other software on this machine), NATS 4222, NATS monitor 8222.

## Layout

See "Repo layout" in PLAN.md. Rules of thumb:

- SQL lives only in `internal/store`. Migrations are numbered files in `internal/store/migrations/`, embedded and applied by `jobradar migrate`. Never edit an applied migration; add a new one.
- Board parsers live in `internal/boards`, one file per board, each with a test that reads a real saved response from `testdata/`.
- Every stage handler must be idempotent: a message can be delivered twice.

## Crawling rules (enforced in code, do not weaken)

- Obey robots.txt (cached 24 h). At most one request per second per domain across all workers; back off on 429 and 5xx.
- Honest User-Agent with the contact from `CRAWLER_CONTACT`.
- No logins, CAPTCHA solving or proxy rotation. A site that blocks the crawler is marked `manual`.
- Never crawl LinkedIn, Naukri, Indeed, Glassdoor or Wellfound.
- No auto-applying, no auto-emailing, no harvesting of personal email addresses.

## Working rules

- One phase at a time; check the phase's "done when" from PLAN.md before moving on; commit at the end of each phase.
- After each phase, add `learning/phase-N-*.md`: what was built, problems hit, how they were solved.
