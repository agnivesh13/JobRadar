-- Live view (Phase 4): events carry enough to be read on their own.
ALTER TABLE crawl_events ADD COLUMN company   TEXT NOT NULL DEFAULT '';
ALTER TABLE crawl_events ADD COLUMN detail    TEXT NOT NULL DEFAULT '';
ALTER TABLE crawl_events ADD COLUMN search_id TEXT NOT NULL DEFAULT '';
ALTER TABLE crawl_events ADD COLUMN n         INT  NOT NULL DEFAULT 0; -- a count: jobs extracted, jobs saved
CREATE INDEX crawl_events_search_idx ON crawl_events (search_id, id) WHERE search_id <> '';
