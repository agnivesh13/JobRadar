# Phase 0: Skeleton

**Goal (from PLAN.md):** repo, Compose file with Postgres and NATS, migrations, one Go binary with subcommands, health endpoints.
**Done when:** `docker compose up` starts clean and `api` answers `/healthz`.

## What I built, step by step

1. **Repo.** Renamed the plan to `PLAN.md`, ran `git init`, `go mod init jobradar`, created the folder layout from the plan.
2. **Config (`internal/config`).** One struct filled from environment variables, every value with a default. Reason: the same binary must run on the laptop (`go run`) and in a container with no code change; only the environment differs.
3. **Schema (`internal/store/migrations/0001_init.sql`).** The six tables from the plan. I added a few columns the plan implies but does not list: `jobs.missed_crawls` (to implement "not seen for two consecutive crawls = closed"), `jobs.desc_hash` (to detect "job changed"), `jobs.alt_urls` (same job at two URLs), `pages.body` (raw page, deleted after 7 days).
4. **Migration runner (`internal/store/store.go`).** SQL files are compiled into the binary with `//go:embed`, applied in filename order, each in its own transaction, and recorded in a `schema_migrations` table. I wrote this by hand (about 60 lines) instead of pulling in a migration library.
5. **Queue wrapper (`internal/queue`).** Connects to NATS and opens a JetStream handle. Nothing is published yet.
6. **One binary, many roles (`cmd/jobradar/main.go`).** A map from subcommand name to function: `api`, `scheduler`, `fetcher`, `extractor`, `scorer`, `renderer`, `migrate`. In this phase every worker just connects to Postgres and NATS, serves `/healthz`, and waits for a shutdown signal.
7. **Health endpoint (`internal/app`).** `/healthz` returns 200 only if both Postgres and NATS answer; otherwise 503 with the error. Docker uses it for its health checks.
8. **Dockerfile.** Three build stages: Node builds the React app, Go builds the binary, a small Alpine image carries both. A fourth target adds Chromium for the renderer.
9. **Compose file.** Postgres, NATS (JetStream on), a one-shot `migrate` container, then the services. 3 fetchers and 2 extractors by default.
10. **Web scaffold.** Vite + React + Tailwind with a placeholder page, so the Docker build has something to build.

## Design decisions worth remembering

- **Startup order with `depends_on` conditions.** `migrate` waits for Postgres to be *healthy* (not just started). Every service waits for `migrate` to have *completed successfully*. Without this, services start before tables exist and crash-loop.
- **Advisory lock around migrations.** `pg_advisory_lock` makes two processes that migrate at the same time take turns. Only one container migrates today, but this costs one line and removes a race forever.
- **Retry on connect.** `store.Open` and `queue.Connect` retry for 30 seconds. Containers often start a moment before their dependency accepts connections.
- **Graceful shutdown.** `signal.NotifyContext` turns Ctrl+C / `docker stop` into a cancelled context. Everything listens on that context. This is the base for "workers finish their current task" later.
- **YAML anchors (`x-app: &app`).** All Go services share one block of Compose config, so a change is made once.

## Problems I hit and how I solved them

### 1. `go` was "not recognized"
- **Symptom:** `go version` failed in PowerShell.
- **Cause:** Go was installed at `C:\Program Files\Go\bin` but that folder was not on PATH.
- **Fix:** `$env:Path += ";C:\Program Files\Go\bin"` at the start of each shell session. Lesson: check whether a tool is missing or merely not on PATH before reinstalling it.

### 2. Postgres container failed: "ports are not available ... 5433"
- **Symptom:** `docker compose up` created all containers, then Postgres failed to start, and every other service was skipped because its dependency never became healthy.
- **How I found the cause:** I had already seen that 5432 was in use, so I chose 5433. The error text ("forbidden by its access permissions") looks like a Windows reserved-port problem, so I first ran `netsh interface ipv4 show excludedportrange protocol=tcp`. 5433 was not reserved. Then `Get-NetTCPConnection -State Listen` showed another process already listening on 5433 (a second local Postgres).
- **Fix:** moved the host side to 55432 and made it configurable (`POSTGRES_HOST_PORT`). Inside the Docker network the services still use `postgres:5432`; only the laptop-facing port changed.
- **Lesson:** the host port and the container port are separate. A clash on the host never needs a change inside the containers. And one failed dependency cascades: read the *first* error, not the last.

### 3. Dockerfile needed a web app that did not exist yet
- **Symptom (foreseen, not hit):** the Dockerfile's Node stage runs `npm ci`, which fails without `package.json` and a lock file.
- **Fix:** created the minimal Vite + React + Tailwind scaffold now rather than in Phase 2, and ran `npm install` once locally to produce `package-lock.json`.

## How to verify

```
docker compose up -d --build
docker compose ps                     # every service "healthy"
curl http://localhost:8080/healthz    # {"nats":"ok","postgres":"ok","service":"api"}
docker compose logs migrate           # applied=[0001_init]
```

Result on 16 Nov 2026: 10 containers healthy, `/healthz` returned 200.
