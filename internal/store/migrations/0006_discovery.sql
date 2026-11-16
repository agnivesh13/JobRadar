-- Discovery and scoring extras (Phase 6).

-- Companies heard about but not yet resolved into something crawlable. Every
-- discovery source writes here; the scheduler resolves a few per tick.
CREATE TABLE candidates (
    id            BIGSERIAL PRIMARY KEY,
    name          TEXT        NOT NULL,
    website       TEXT        NOT NULL DEFAULT '',  -- a URL or domain, when the source gave one
    board_type    TEXT        NOT NULL DEFAULT '',  -- set when the source linked straight to a board
    board_token   TEXT        NOT NULL DEFAULT '',
    source        TEXT        NOT NULL,             -- rss | hn | yc | search | outbound
    detail        TEXT        NOT NULL DEFAULT '',  -- e.g. the headline it came from
    funding_stage TEXT        NOT NULL DEFAULT '',
    status        TEXT        NOT NULL DEFAULT 'new', -- new | added | known | failed
    note          TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    tried_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX candidates_name_uq ON candidates (lower(name));
CREATE INDEX candidates_new_idx ON candidates (id) WHERE status = 'new';

-- Optional LLM opinion, kept apart from the rule-based score so that
-- re-scoring after a settings change does not need another LLM call.
ALTER TABLE jobs ADD COLUMN llm_score  INT;
ALTER TABLE jobs ADD COLUMN llm_reason TEXT NOT NULL DEFAULT '';
-- Set once a Telegram alert has been sent, so a job alerts at most once.
ALTER TABLE jobs ADD COLUMN alerted_at TIMESTAMPTZ;
