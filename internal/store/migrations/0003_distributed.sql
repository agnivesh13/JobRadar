-- Bookkeeping for the distributed pipeline (Phase 3).

-- The content hash whose jobs were fully applied by the scorer. A page counts
-- as "unchanged" only against this, never against a hash that was merely
-- downloaded: otherwise a worker that dies between "page saved" and "page
-- extracted" would make the retry see an unchanged page and skip its jobs.
ALTER TABLE pages ADD COLUMN extracted_hash TEXT;

-- When the scheduler last handed this company to the queue. A company is not
-- scheduled again until its crawl finishes or this times out (with backoff).
ALTER TABLE companies ADD COLUMN scheduled_at TIMESTAMPTZ;
-- Set by "crawl now" and by `jobradar recrawl`: crawl at the next tick and
-- re-extract even if the page is unchanged.
ALTER TABLE companies ADD COLUMN force_next BOOLEAN NOT NULL DEFAULT false;
-- 'high' while a deep search or "crawl now" is waiting.
ALTER TABLE companies ADD COLUMN next_priority TEXT NOT NULL DEFAULT 'normal';
