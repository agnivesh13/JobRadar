-- Crawl bookkeeping added in Phase 1.

-- Shared per-domain rate limiter. Every fetcher reserves its next request
-- slot here in one atomic statement, so all workers together stay under the
-- per-domain rate no matter how many there are.
CREATE TABLE domain_slots (
    domain       TEXT PRIMARY KEY,
    next_free_at TIMESTAMPTZ NOT NULL
);

-- The crawl whose "done" was last applied to a company. Makes the
-- disappeared-job bookkeeping idempotent: a redelivered "done" is a no-op.
ALTER TABLE companies ADD COLUMN last_done_crawl_id TEXT NOT NULL DEFAULT '';
-- Why a company cannot be crawled automatically (robots.txt, blocked, dead feed).
ALTER TABLE companies ADD COLUMN status_reason TEXT NOT NULL DEFAULT '';
-- Consecutive failed crawls; drives backoff and the switch to status 'manual'.
ALTER TABLE companies ADD COLUMN fail_count INT NOT NULL DEFAULT 0;
