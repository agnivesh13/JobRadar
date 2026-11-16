# Phase 4: Live view

**Goal (from PLAN.md):** `crawl_events`, Server-Sent Events relay, Live screen with counters and pause.
**Done when:** you watch pages move through stages in real time.

## What I built, step by step

1. **Event type and emitter (`internal/events`).** One `Event` struct (time, service, company, URL, stage, outcome, detail, count, duration, search id). Every service has an `Emitter` whose `Emit` publishes to the NATS subject `events.crawl` and returns immediately.
2. **An `EVENTS` stream** that captures that subject (kept for a day at most).
3. **Emit points in every service (`internal/services`).** scheduler: "schedule"; fetcher: "fetch" (ok, unchanged, retry, blocked, error, dead); extractor: "extract" with the job count; scorer: "match" for each new posting that passes the filters, and "score" when a company's crawl completes.
4. **Relay in the api (`internal/api/live.go`).** One durable consumer reads events in batches. For each batch it first inserts into `crawl_events` (the permanent log), then hands the events to an in-memory hub, then acknowledges. The hub fans out to every connected browser.
5. **`GET /api/live`**, a Server-Sent Events stream with two message kinds: `crawl` (one event) and `status` (counters, queue depth per stage, live workers, paused flag, current speed) every two seconds. `?search_id=` narrows the stream to one deep search (used in Phase 5).
6. **Heartbeats.** Each worker publishes a tiny message every 3 seconds on a plain (not stored) NATS subject. The api counts an instance as alive if it was heard from in the last 10 seconds. That is the "active workers" number.
7. **Pause, resume and speed (`internal/app/control.go`).** Two keys in the `settings` table: `paused` and `domain_interval_ms`. Every container re-reads them at most every two seconds. The scheduler stops scheduling and the fetchers stop taking messages while paused. The shared rate limiter reads the interval on every request.
8. **Live screen (`web/src/Live.jsx`).** Six counters, a pause button, a speed slider, the worker list, and a feed capped at 500 lines, with a "freeze feed" checkbox for reading.

## Design decisions worth remembering

- **Emitting can never fail or slow the pipeline.** `Emit` is a plain publish with no acknowledgement wait, and errors are ignored. The live view is a window onto the crawl, not a dependency of it.
- **Store first, then show.** The relay writes a batch to `crawl_events` before broadcasting and acknowledges only after the insert succeeds. So anything a browser showed is in the table, and if the api restarts it resumes from the last acknowledged event with nothing lost. The stream buffers events while the api is down.
- **Server-Sent Events, not WebSockets.** The data only flows server to browser, SSE is plain HTTP, and the browser's `EventSource` reconnects by itself. A slow browser gets lines dropped (its channel is bounded) rather than stalling everyone else.
- **The speed control can only slow down.** The plan's rule is at most one request per second per domain. The slider's minimum is that configured floor, and the server clamps anything lower. A UI control must not be able to break a politeness rule.
- **Pause leaves the queue alone.** Tasks already queued simply wait; nothing is cancelled or lost, and Resume continues where it stopped. Extractor and scorer are not paused, so work already downloaded still gets saved.
- **Control lives in the database, not in memory**, because there are many containers and they must all agree. A two-second cache keeps it cheap.

## Problems I hit and how I solved them

### 1. Which process writes `crawl_events`?
- **The tension:** the plan says services write events to NATS and the full log lives in `crawl_events`. If every service also wrote to Postgres, each event would cost a database write on the hot path.
- **Decision:** only the api writes the table, in batches of up to 200 with `COPY`. Services only publish.

### 2. Company names were missing from events
- **Symptom (foreseen while writing the emit calls):** fetch tasks carried only `company_id`, so the feed would show numbers.
- **Fix:** added the company name to `FetchTask`, `JobFound` and `CrawlDone`, filled once when the task is created. An event should be readable on its own.

### 3. My first "save control" handler slept for two seconds
- **What I wrote first:** after saving `paused`, the handler slept 2.1 s so the cached value would expire before replying.
- **Why that was wrong:** a sleep in a request handler is a guess, and it made the button feel broken.
- **Fix:** an `Invalidate()` method on the control cache; the handler calls it and replies with fresh values immediately.

### 4. Per-job events would have flooded the feed
- **Numbers:** one crawl of the seed list touches about 40,000 postings.
- **Decision:** the scorer emits an event only for a *new posting that passes the filters* ("match"), plus one summary per company crawl. The other stages emit one event per page, not per job.

### 5. Counting "errors" consistently
- Retries, blocks and dead letters are all counted as errors in the counter, each with its own outcome label in the feed, so a number above zero always has visible lines explaining it.

## How to verify

```
docker compose up -d --build
docker compose run --rm migrate recrawl      # gives the crawler something to do
```
Open http://localhost:8080/#/live. You should see:
- lines moving through `schedule -> fetch -> extract -> score`, newest on top
- "Pages fetched" and "Jobs found" climbing, "Queue depth" rising then falling
- "Active workers" equal to the running containers (2 schedulers, 3 fetchers, 2 extractors, 1 scorer = 8)
- **Pause crawling** stops new fetch lines within a few seconds; **Resume** continues

`curl -N http://localhost:8080/api/live` shows the raw stream.

Result on 17 Nov 2026: verified in the browser during a full re-crawl: 8 active workers, events streaming with durations, counters live.
