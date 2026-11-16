-- The six tables from PLAN.md "Data model". The queue only holds work in
-- flight; everything that must survive lives here.

CREATE TABLE companies (
    id              BIGSERIAL PRIMARY KEY,
    name            TEXT        NOT NULL,
    domain          TEXT,
    careers_url     TEXT,
    board_type      TEXT        NOT NULL DEFAULT 'unknown', -- greenhouse | lever | ashby | custom | unknown
    board_token     TEXT,
    pay_tier        TEXT        NOT NULL DEFAULT 'unknown', -- high | medium | low | unknown
    source          TEXT        NOT NULL DEFAULT 'seed',    -- seed | manual | deep_search | rss | hn | yc | search | outbound
    funding_stage   TEXT,
    status          TEXT        NOT NULL DEFAULT 'active',  -- active | manual (blocked us) | removed
    last_crawled_at TIMESTAMPTZ,
    crawl_interval  INTERVAL    NOT NULL DEFAULT '6 hours',
    last_job_count  INT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX companies_name_uq  ON companies (lower(name));
CREATE UNIQUE INDEX companies_board_uq ON companies (board_type, board_token) WHERE board_token IS NOT NULL;

CREATE TABLE pages (
    id           BIGSERIAL PRIMARY KEY,
    company_id   BIGINT REFERENCES companies (id) ON DELETE CASCADE,
    url          TEXT    NOT NULL UNIQUE,
    kind         TEXT    NOT NULL DEFAULT 'board_feed',
    content_hash TEXT,
    http_status  INT,
    fetched_at   TIMESTAMPTZ,
    rendered     BOOLEAN NOT NULL DEFAULT false,
    body         BYTEA   -- raw body, deleted after BODY_RETENTION_DAYS; the hash stays
);
CREATE INDEX pages_company_idx ON pages (company_id);

CREATE TABLE jobs (
    id            BIGSERIAL PRIMARY KEY,
    company_id    BIGINT      NOT NULL REFERENCES companies (id) ON DELETE CASCADE,
    url           TEXT        NOT NULL,
    alt_urls      TEXT[]      NOT NULL DEFAULT '{}',      -- same job seen at other URLs
    title         TEXT        NOT NULL,
    location      TEXT        NOT NULL DEFAULT '',
    remote        BOOLEAN     NOT NULL DEFAULT false,
    department    TEXT        NOT NULL DEFAULT '',
    experience    TEXT        NOT NULL DEFAULT '',        -- intern | new_grad | junior | mid | senior | ''
    salary_min    BIGINT,                                 -- rupees per year
    salary_max    BIGINT,                                 -- rupees per year
    salary_state  TEXT        NOT NULL DEFAULT 'unknown', -- above | below | unknown
    salary_text   TEXT,                                   -- the sentence the figure came from
    description   TEXT        NOT NULL DEFAULT '',
    desc_hash     TEXT        NOT NULL DEFAULT '',
    posted_at     TIMESTAMPTZ,
    first_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_crawl_id TEXT        NOT NULL DEFAULT '',        -- crawl that last saw this job
    missed_crawls INT         NOT NULL DEFAULT 0,         -- consecutive company crawls without it
    closed_at     TIMESTAMPTZ,
    updated_at    TIMESTAMPTZ,                            -- set when the description changed ("updated" flag)
    matched       BOOLEAN     NOT NULL DEFAULT false,     -- passes the role/experience/location filters
    flags         TEXT[]      NOT NULL DEFAULT '{}',      -- e.g. batch_mismatch
    fit_score     INT         NOT NULL DEFAULT 0,
    fit_reason    TEXT        NOT NULL DEFAULT '',
    fingerprint   TEXT        NOT NULL UNIQUE             -- hash(company, normalised title, location)
);
CREATE INDEX jobs_company_idx ON jobs (company_id);
CREATE INDEX jobs_list_idx    ON jobs (matched, salary_state, first_seen_at DESC) WHERE closed_at IS NULL;

CREATE TABLE applications (
    job_id        BIGINT PRIMARY KEY REFERENCES jobs (id) ON DELETE CASCADE,
    status        TEXT        NOT NULL DEFAULT 'saved', -- saved | applied | replied | interview | offer | rejected | skipped
    applied_at    TIMESTAMPTZ,
    contact_name  TEXT        NOT NULL DEFAULT '',
    contact_email TEXT        NOT NULL DEFAULT '',
    notes         TEXT        NOT NULL DEFAULT '',
    follow_up_on  DATE,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE crawl_events (
    id          BIGSERIAL PRIMARY KEY,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    service     TEXT        NOT NULL,
    company_id  BIGINT,
    url         TEXT        NOT NULL DEFAULT '',
    stage       TEXT        NOT NULL,
    outcome     TEXT        NOT NULL,
    duration_ms INT         NOT NULL DEFAULT 0
);
CREATE INDEX crawl_events_ts_idx ON crawl_events (ts DESC);

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
