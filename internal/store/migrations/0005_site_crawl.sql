-- Whole-site crawling (Phase 5).

-- The frontier of one site crawl: every URL claimed for it, once. The primary
-- key deduplicates URLs across all extractors; `processed` makes the
-- "this page is finished" bookkeeping idempotent under redelivery.
CREATE TABLE crawl_pages (
    crawl_id  TEXT    NOT NULL,
    url       TEXT    NOT NULL,
    parent    TEXT    NOT NULL DEFAULT '',
    depth     INT     NOT NULL DEFAULT 0,
    processed BOOLEAN NOT NULL DEFAULT false,
    jobs      INT     NOT NULL DEFAULT 0,
    PRIMARY KEY (crawl_id, url)
);
CREATE INDEX crawl_pages_open_idx ON crawl_pages (crawl_id) WHERE NOT processed;

-- Where a company came from, for the Companies screen and discovery.
ALTER TABLE companies ADD COLUMN source_detail TEXT NOT NULL DEFAULT '';
