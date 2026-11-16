# Phase 3: Distributed pipeline

**Goal (from PLAN.md):** split into scheduler, fetcher, extractor, scorer over NATS; leases, retries, dead-letter, idempotency, shared rate limiter, leader lock.
**Done when:** killing any container mid-crawl loses no job and creates no duplicate; a test script proves it.

This is the phase to be able to explain in an interview, so this file is longer than the others.

## The shape

```
scheduler (x2, one leader) --crawl.fetch.high/normal--> fetcher (x3)
fetcher --crawl.fetched--> extractor (x2)
extractor --crawl.score (jobs..., then "done")--> scorer (x1)
any stage, after 5 failed attempts --dlq.<subject>--> DLQ stream
```

One JetStream stream (`CRAWL`, work-queue retention, file storage) carries every stage. Each subject has exactly one durable consumer, shared by all replicas of that service. Postgres holds all state; the queue holds only work in flight.

## What I built, step by step

1. **Queue layer (`internal/queue/work.go`).** `Setup` (idempotent stream and consumer creation), `Publish` with a message id, `PublishBatch`, `Backlog`, and `Consume`, which runs a handler with a heartbeat, retry with backoff, and dead-lettering.
2. **Services (`internal/services/services.go`).** `RunScheduler`, `RunFetcher`, `RunExtractor`, `RunScorer`. Each is a thin wrapper that decodes a message, calls the *same stage function Phase 1 used*, and publishes the result. No crawl logic was rewritten.
3. **Leader lock (`internal/store/leader.go`).** `pg_try_advisory_lock` on a dedicated connection.
4. **Scheduling queries (`store.DueCompanies`, `MarkScheduled`, `RequestCrawl`).**
5. **Tools.** `jobradar recrawl` (make everything due, force re-extraction), `jobradar verify` (check the invariants), `jobradar queue` (show stream and consumer state, list dead letters, or wipe the queue).
6. **`scripts/chaos.sh`.** Requests a full re-crawl, SIGKILLs 14 containers in rotation while it runs (fetchers, extractors, scorer, both schedulers, **NATS and Postgres**), waits for the queue to drain, runs `verify`.

## How each "distributed-systems part" from the plan is implemented

### At-least-once delivery with idempotent handlers
JetStream redelivers any message that was not acknowledged. So every handler must be safe to run twice:

| Handler | Why a second run is a no-op |
| --- | --- |
| fetch | Saving a page is an upsert keyed on URL |
| extract | It only reads the stored page and publishes; publishes carry deterministic ids |
| score (job) | `INSERT ... ON CONFLICT (fingerprint) DO UPDATE` |
| score (done) | Guarded by the crawl id: `companies.last_done_crawl_id` |

Publishing is idempotent too: every message has a deterministic `Nats-Msg-Id` (for example `j:<crawl>:<fingerprint>`), and the stream drops a repeated id within a 10 minute window. So "publish, crash before ack, redeliver, publish again" stores the message once.

**The rule that makes it all work: do the work, publish the results, *then* acknowledge.** A crash at any point before the ack means the input comes back.

### Leases
A consumer's `AckWait` is 30 seconds. While a handler runs, a goroutine calls `msg.InProgress()` every 10 seconds, which extends the lease. A worker that is killed stops heartbeating, the lease expires, and the server hands the message to another worker. Nothing has to notice the death.

### Retries and dead letters
A failed handler calls `NakWithDelay` with backoff (2 s, 5 s, 15 s, 30 s, 60 s), or the delay the site asked for (`Retry-After`). On the fifth failure the message is copied to `dlq.<subject>` with the error in a header and then terminated. A fetch that dies this way still publishes a "failed" result downstream, so its crawl completes instead of hanging.

### Distributed rate limiting
Unchanged from Phase 1: one SQL statement reserves the domain's next slot. Three fetchers with four tasks each still send at most one request per second to any domain.

### Back-pressure
Before scheduling, the leader asks NATS how far behind each stage is (waiting plus in flight). If the extractor or scorer backlog is over its limit, it schedules nothing this tick. It also never keeps more than 300 fetch tasks queued.

### Leader election
Two schedulers run. Each tick, each one calls `Hold()`: the holder pings its lock connection, the other tries `pg_try_advisory_lock`. The lock belongs to a database session, so if the leader dies, Postgres releases it and the standby wins on its next try (within 5 seconds). No timeouts to tune and no clock assumptions.

Even a brief double-leader could not cause harm: the crawl id is *deterministic* (`c<company>-<last crawl time>-<attempt>`), so two schedulers publishing the same company produce the same message id and the queue keeps one.

### Crash recovery
Covered by the chaos test below.

## Problems I hit and how I solved them

### 1. A design bug found on paper: "unchanged page" could silently lose every job of a company
- **The scenario:** fetcher saves page (hash H), then dies before publishing "fetched". The task is redelivered. The second fetch downloads the same content, compares against the stored hash, says **"unchanged"**, and extraction is skipped. The jobs were never extracted, and never will be until the feed changes.
- **Root cause:** "unchanged" was defined against the last *download*, but it has to mean "same as the last content whose jobs were fully applied".
- **Fix:** a second column, `pages.extracted_hash`, stamped by the scorer when it applies the crawl's "done". The fetcher compares against that.
- **Lesson:** a skip-if-seen optimisation must be keyed on *work completed*, not *work started*.

### 2. Scheduler crash between "pick a company" and "publish its task"
- **First design:** claim the company in the database, then publish. A crash in between leaves a company marked scheduled with no task, waiting out a 30 minute timeout.
- **Fix:** reverse the order. Select (read-only), publish with the deterministic id, then mark. A crash in between repeats the publish on the next tick, and the queue drops the duplicate.

### 3. Fetches timed out and one feed was "too large" forever
- **Symptom in the first distributed run:** many `context deadline exceeded ... while reading body`, and SpaceX dead-lettered with `body larger than 26214400 bytes`.
- **Causes:** (a) with `content=true`, big Greenhouse feeds are tens of megabytes and my 30 s timeout was too short when 12 downloads shared the laptop's connection; (b) a size error was being *retried* five times although retrying cannot shrink a file.
- **Fixes:** timeout 120 s, size cap 64 MB, and "too large" became a permanent, recorded failure (no retries, no dead letter).
- **Lesson:** classify errors as retryable or not at the point they are created.

### 4. One poison message blocked "queue drained"
- The UTF-8 truncation bug (see Phase 2, problem 7) made one posting fail forever. The retry and dead-letter path did its job: after five attempts it moved to the DLQ, and `jobradar queue -dead` showed exactly which posting and which error. The verifier then reported that one job as lost, which is how I found the bug.

### 5. Chaos run 1: nothing lost, nothing duplicated, but the queue never read as empty
- **Result:** 280 companies, 30,919 jobs expected, 0 lost, 0 duplicates. Yet the script failed: "consumer score still has 1 message", forever.
- **Investigation:** I wrote `jobradar queue` to look inside. The stream held 270 messages that no consumer considered pending, and the scorer's consumer counted one in-flight message that did not exist.
- **Cause:** SIGKILLing NATS. By default JetStream flushes to disk every two minutes, so a hard kill can leave the stream file and the consumer state file disagreeing: messages acknowledged but not yet deleted on disk, and a pending entry for a message already gone.
- **Fix:** `sync_interval: always` in `deploy/nats.conf`, so every write is flushed before it is confirmed.
- **Lesson:** "durable" has settings. A queue's defaults are tuned for throughput; if the promise is "survives kill -9", the fsync policy has to be chosen on purpose.

### 6. ...and that fix made the pipeline 10x slower
- **Symptom:** with a flush per write, the extractor published about 35 messages a second, since it waited for each confirmation before sending the next.
- **Fix:** `PublishBatch`: send up to 256 publishes without waiting, then collect the confirmations. The server can then confirm them in groups. The scorer also switched from a confirmed ack to a plain ack, which is safe because a lost ack only causes a redelivery, and the handler is idempotent.
- **Lesson:** durability costs a flush; batching is how you pay for one flush with many messages.

### 7. The scorer was the bottleneck
- One scorer handling one message at a time took 15 minutes for 30k postings. I had kept it sequential so a crawl's jobs are always applied before its "done" marker.
- **Realisation:** the order does not need to be strict, because the two handlers commute. If "done" runs first, the late job gets one miss; when the job is then saved, the upsert resets its miss counter and reopens it. Same final state.
- **Fix:** scorer concurrency 4 (configurable). Throughput went up about four times.

### 8. A database outage parked six companies as "blocked by robots.txt"
- **Symptom after chaos run 1:** Palantir, Hevodata and four others were marked `manual` with reason "disallowed by robots.txt". All on `api.lever.co`, whose robots.txt allows everything.
- **Cause:** fetching robots.txt goes through the rate limiter, which is in Postgres. While Postgres was being killed, that query failed; my code treated *any* failure to load robots.txt as "deny", and deny means "mark the company manual".
- **Fix:** three outcomes instead of two: allowed, disallowed (a real rule), and "could not check right now", which is a retryable error and never parks a company.
- **Lesson:** "no" and "I don't know" must be different values. Collapsing them turned a five second outage into permanent damage.

## The chaos test

```
bash scripts/chaos.sh          # 14 kills, 7 seconds apart
```

What `jobradar verify` checks, using the raw feeds stored in `pages.body` as ground truth:

1. the queue is drained
2. every active company finished its latest crawl
3. **no job lost:** every job in a company's stored feed is an open row stamped with that crawl
4. **no duplicate:** no two rows share company, title and location

### Results

| Run | Setup | Kills | Companies checked | Jobs expected | Lost | Duplicates | Verdict |
| --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | one message per job, default NATS flushing, 1 scorer thread | 14 | 280 | 30,919 | 0 | 0 | Script failed: a phantom in-flight message after the NATS kill meant the queue never read as empty (problem 5) |
| 2 | `sync_interval: always`, still one message per job | 14 | - | - | - | - | Abandoned: correct but about 40 queue operations a second, far too slow (problem 6) |
| 3 | `sync_interval: always`, jobs in chunks, batched publishes, 4 scorer threads | 14 | 485 | 39,623 | **0** | **0** | **PASSED** |

Run 3 output (17 Nov 2026):

```
== before: 503 active companies, 39762 job rows
== chaos: 14 kills, one every 7s
   kill 1/14: jobradar-fetcher-1 ... kill 6/14: jobradar-nats-1 ... kill 10/14: jobradar-postgres-1 ... kill 14/14: jobradar-scheduler-1
== waiting for the queue to drain
PASS: queue drained, every company completed, no job lost, no duplicate
verify companies_checked=485 companies_with_failed_crawl=1 jobs_expected=39623 duplicate_groups=0
== after: 40511 job rows (was 39762)
== CHAOS TEST PASSED
```

Notes on reading it honestly:
- "Companies checked" covers the companies on job boards (485), where the stored feed is exact ground truth. The 27 custom-site companies are crawled by the same machinery but are not part of the lost-job comparison.
- One company's crawl failed with a recorded reason, which is a completed crawl, not a lost job.
- The job count rose because feeds gained postings between crawls and whole-site crawls of newly discovered companies were running at the same time.
- Most of the wait after the last kill was those whole-site crawls finishing at one page per second per site, not recovery.

### One more change that came out of run 2: chunked messages
One message per job meant about 80,000 queue writes per full crawl, each needing a disk flush. The extractor now sends jobs in chunks of up to 100 jobs or about 400 KB per message. A chunk's message id is a hash of its contents, so a redelivered page republishes identical chunks and the queue drops them. The scorer saves every job of a chunk and retries the whole chunk if any job fails, which is safe because saving is idempotent. A full crawl is now about 4,000 messages.

## How to verify

```
docker compose up -d --build
bash scripts/chaos.sh
docker compose run --rm migrate queue -dead     # inspect dead letters
```

**Resume line**, with numbers from these runs: "Distributed job-crawling pipeline in Go (NATS JetStream, Postgres, Docker): 481 companies, about 40,000 postings per crawl, zero lost or duplicated jobs under repeated container failure including the queue and the database." Replace the numbers with your own runs.
