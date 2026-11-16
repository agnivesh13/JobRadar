package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"jobradar/internal/model"
)

// ---- shared rate limiter ----------------------------------------------

// Limiter is the distributed per-domain rate limit. Each call reserves the
// domain's next free slot in one atomic statement, so N workers hitting the
// same domain are queued one interval apart by the database, with no
// coordination between them.
type Limiter struct {
	Store    *Store
	Interval time.Duration
	// Dynamic, when set, overrides Interval (the Live screen's speed control).
	Dynamic func() time.Duration
}

func (l *Limiter) gap() float64 {
	if l.Dynamic != nil {
		return l.Dynamic().Seconds()
	}
	return l.Interval.Seconds()
}

func (l *Limiter) Wait(ctx context.Context, domain string) error {
	var wait float64 // seconds until our reserved slot
	err := l.Store.Pool.QueryRow(ctx, `
		INSERT INTO domain_slots (domain, next_free_at)
		VALUES ($1, now() + make_interval(secs => $2))
		ON CONFLICT (domain) DO UPDATE
		   SET next_free_at = GREATEST(domain_slots.next_free_at, now()) + make_interval(secs => $2)
		RETURNING EXTRACT(EPOCH FROM next_free_at - make_interval(secs => $2) - now())::float8`,
		domain, l.gap()).Scan(&wait)
	if err != nil {
		return err
	}
	if wait <= 0 {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(time.Duration(wait * float64(time.Second))):
		return nil
	}
}

func (l *Limiter) Penalize(ctx context.Context, domain string, d time.Duration) error {
	_, err := l.Store.Pool.Exec(ctx, `
		INSERT INTO domain_slots (domain, next_free_at)
		VALUES ($1, now() + make_interval(secs => $2))
		ON CONFLICT (domain) DO UPDATE
		   SET next_free_at = GREATEST(domain_slots.next_free_at, now() + make_interval(secs => $2))`,
		domain, d.Seconds())
	return err
}

// ---- companies ----------------------------------------------------------

const companyCols = `id, name, COALESCE(domain,''), COALESCE(careers_url,''), board_type, COALESCE(board_token,''),
	pay_tier, source, COALESCE(funding_stage,''), status, last_crawled_at, last_job_count`

func scanCompany(row pgx.Row) (model.Company, error) {
	var c model.Company
	err := row.Scan(&c.ID, &c.Name, &c.Domain, &c.CareersURL, &c.BoardType, &c.BoardToken,
		&c.PayTier, &c.Source, &c.FundingStage, &c.Status, &c.LastCrawledAt, &c.LastJobCount)
	return c, err
}

// AddCompany inserts a company unless one with the same name or the same
// board token already exists. It returns the id and whether it was created.
func (s *Store) AddCompany(ctx context.Context, c model.Company) (id int64, created bool, err error) {
	interval := "6 hours"
	if c.BoardType == model.BoardCustom || c.BoardType == model.BoardUnknown {
		interval = "24 hours"
	}
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO companies (name, domain, careers_url, board_type, board_token, pay_tier, source, funding_stage, crawl_interval)
		VALUES ($1, NULLIF($2,''), NULLIF($3,''), $4, NULLIF($5,''), COALESCE(NULLIF($6,''),'unknown'), COALESCE(NULLIF($7,''),'seed'), NULLIF($8,''), $9::interval)
		ON CONFLICT DO NOTHING
		RETURNING id`,
		c.Name, c.Domain, c.CareersURL, c.BoardType, c.BoardToken, c.PayTier, c.Source, c.FundingStage, interval).Scan(&id)
	if err == nil {
		return id, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, false, err
	}
	err = s.Pool.QueryRow(ctx, `
		SELECT id FROM companies
		WHERE lower(name) = lower($1) OR ($2 <> '' AND board_type = $3 AND board_token = $2)
		ORDER BY id LIMIT 1`, c.Name, c.BoardToken, c.BoardType).Scan(&id)
	return id, false, err
}

func (s *Store) GetCompany(ctx context.Context, id int64) (model.Company, error) {
	return scanCompany(s.Pool.QueryRow(ctx, `SELECT `+companyCols+` FROM companies c WHERE id = $1`, id))
}

// DueCompany is a company the scheduler should crawl now.
type DueCompany struct {
	model.Company
	Force    bool   // re-extract even if the page is unchanged
	Priority string // high | normal
	CrawlID  string // deterministic, so scheduling the same due instance twice is a no-op downstream
}

// DueCompanies lists active companies to crawl, forced ones first, then the
// most overdue (never-crawled first of all). A company is due when its crawl
// interval has passed and it is not already scheduled; a scheduled crawl
// that never finished is retried with exponential backoff (30 min, 1 h, ...).
//
// This only reads. The scheduler publishes the task and then calls
// MarkScheduled; if it dies in between, the next tick picks the same company
// and produces the same CrawlID, which the queue deduplicates.
func (s *Store) DueCompanies(ctx context.Context, limit int) ([]DueCompany, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT `+companyCols+`, force_next, next_priority,
		       'c' || id || '-' || COALESCE(EXTRACT(EPOCH FROM last_crawled_at)::bigint, 0) || '-' || fail_count
		         || CASE WHEN force_next THEN '-f' || COALESCE(EXTRACT(EPOCH FROM scheduled_at)::bigint, 0) ELSE '' END
		FROM companies
		WHERE status = 'active' AND board_type <> 'unknown'
		  AND (force_next OR (
		        (last_crawled_at IS NULL OR last_crawled_at + crawl_interval <= now())
		    AND (scheduled_at IS NULL OR scheduled_at + make_interval(mins => 15 * power(2, LEAST(fail_count, 6))::int) <= now())))
		ORDER BY force_next DESC, (next_priority = 'high') DESC, last_crawled_at NULLS FIRST, id
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(r pgx.CollectableRow) (DueCompany, error) {
		var d DueCompany
		c := &d.Company
		err := r.Scan(&c.ID, &c.Name, &c.Domain, &c.CareersURL, &c.BoardType, &c.BoardToken,
			&c.PayTier, &c.Source, &c.FundingStage, &c.Status, &c.LastCrawledAt, &c.LastJobCount,
			&d.Force, &d.Priority, &d.CrawlID)
		return d, err
	})
}

// MarkScheduled records that a company's crawl task is in the queue.
// fail_count goes up here and back to zero when the crawl finishes, so a
// crawl that vanishes (dead-lettered, lost) backs off by itself.
func (s *Store) MarkScheduled(ctx context.Context, id int64) error {
	_, err := s.Pool.Exec(ctx, `UPDATE companies SET scheduled_at = now(), fail_count = fail_count + 1,
		force_next = false, next_priority = 'normal' WHERE id = $1`, id)
	return err
}

// RequestCrawl makes companies due immediately. id 0 means every active company.
func (s *Store) RequestCrawl(ctx context.Context, id int64, priority string) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE companies SET force_next = true, next_priority = $2
		WHERE status = 'active' AND ($1 = 0 OR id = $1)`, id, priority)
	return tag.RowsAffected(), err
}

// MarkManual takes a company out of automatic crawling (robots.txt, blocked).
func (s *Store) MarkManual(ctx context.Context, id int64, reason string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE companies SET status = 'manual', status_reason = $2, last_crawled_at = now() WHERE id = $1`, id, reason)
	return err
}

// ---- pages ----------------------------------------------------------------

// SavePage records a fetch. unchanged is true only when the body equals the
// one whose jobs were already fully applied (pages.extracted_hash); see
// migration 0003 for why a merely downloaded hash does not count.
func (s *Store) SavePage(ctx context.Context, companyID int64, url, kind, hash string, status int, rendered bool, body []byte) (pageID int64, unchanged bool, err error) {
	var prev *string
	err = s.Pool.QueryRow(ctx, `
		WITH old AS (SELECT extracted_hash FROM pages WHERE url = $2)
		INSERT INTO pages (company_id, url, kind, content_hash, http_status, fetched_at, rendered, body)
		VALUES ($1, $2, $3, $4, $5, now(), $6, $7)
		ON CONFLICT (url) DO UPDATE
		   SET company_id = EXCLUDED.company_id, kind = EXCLUDED.kind, content_hash = EXCLUDED.content_hash,
		       http_status = EXCLUDED.http_status, fetched_at = now(), rendered = EXCLUDED.rendered, body = EXCLUDED.body
		RETURNING id, (SELECT extracted_hash FROM old)`,
		companyID, url, kind, hash, status, rendered, body).Scan(&pageID, &prev)
	return pageID, prev != nil && *prev == hash, err
}

func (s *Store) PageBody(ctx context.Context, pageID int64) (body []byte, url string, err error) {
	err = s.Pool.QueryRow(ctx, `SELECT COALESCE(body, ''::bytea), url FROM pages WHERE id = $1`, pageID).Scan(&body, &url)
	return
}

// PurgeBodies drops raw bodies older than the retention period, keeping hashes.
func (s *Store) PurgeBodies(ctx context.Context, olderThan time.Duration) (int64, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE pages SET body = NULL WHERE body IS NOT NULL AND fetched_at < now() - make_interval(secs => $1)`, olderThan.Seconds())
	return tag.RowsAffected(), err
}

// ---- jobs -----------------------------------------------------------------

// Job is one row of the jobs table as the scorer writes it.
type Job struct {
	CompanyID   int64
	URL         string
	Title       string
	Location    string
	Remote      bool
	Department  string
	Experience  string
	SalaryMin   *int64
	SalaryMax   *int64
	SalaryState string
	SalaryText  string
	Description string
	DescHash    string
	PostedAt    *time.Time
	CrawlID     string
	Matched     bool
	Flags       []string
	FitScore    int
	FitReason   string
	Fingerprint string
}

// UpsertJob is idempotent on the fingerprint: the first delivery inserts, any
// repeat only refreshes last_seen. A second URL for the same job is kept in
// alt_urls; a changed description sets updated_at.
func (s *Store) UpsertJob(ctx context.Context, j Job) (id int64, inserted bool, err error) {
	if j.Flags == nil {
		j.Flags = []string{}
	}
	// Text from the web can hold broken UTF-8 or NUL bytes, both of which
	// Postgres rejects; one bad posting must not poison the queue.
	for _, p := range []*string{&j.URL, &j.Title, &j.Location, &j.Department, &j.SalaryText, &j.Description, &j.FitReason} {
		*p = strings.ReplaceAll(strings.ToValidUTF8(*p, ""), "\x00", "")
	}
	err = s.Pool.QueryRow(ctx, `
		INSERT INTO jobs (company_id, url, title, location, remote, department, experience,
			salary_min, salary_max, salary_state, salary_text, description, desc_hash, posted_at,
			last_crawl_id, matched, flags, fit_score, fit_reason, fingerprint)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,NULLIF($11,''),$12,$13,$14,$15,$16,$17,$18,$19,$20)
		ON CONFLICT (fingerprint) DO UPDATE SET
			last_seen_at  = now(),
			last_crawl_id = EXCLUDED.last_crawl_id,
			missed_crawls = 0,
			closed_at     = NULL,
			alt_urls      = CASE WHEN jobs.url <> EXCLUDED.url AND NOT (EXCLUDED.url = ANY (jobs.alt_urls))
			                     THEN array_append(jobs.alt_urls, EXCLUDED.url) ELSE jobs.alt_urls END,
			updated_at    = CASE WHEN jobs.desc_hash <> EXCLUDED.desc_hash THEN now() ELSE jobs.updated_at END,
			description   = EXCLUDED.description,
			desc_hash     = EXCLUDED.desc_hash,
			remote        = EXCLUDED.remote,
			department    = EXCLUDED.department,
			experience    = EXCLUDED.experience,
			salary_min    = EXCLUDED.salary_min,
			salary_max    = EXCLUDED.salary_max,
			salary_state  = EXCLUDED.salary_state,
			salary_text   = EXCLUDED.salary_text,
			posted_at     = COALESCE(EXCLUDED.posted_at, jobs.posted_at),
			matched       = EXCLUDED.matched,
			flags         = EXCLUDED.flags,
			fit_score     = CASE WHEN jobs.llm_score IS NULL THEN EXCLUDED.fit_score ELSE (EXCLUDED.fit_score + jobs.llm_score) / 2 END,
			fit_reason    = CASE WHEN jobs.llm_score IS NULL THEN EXCLUDED.fit_reason ELSE EXCLUDED.fit_reason || '; LLM: ' || jobs.llm_reason END
		RETURNING id, (xmax = 0)`,
		j.CompanyID, j.URL, j.Title, j.Location, j.Remote, j.Department, j.Experience,
		j.SalaryMin, j.SalaryMax, j.SalaryState, j.SalaryText, j.Description, j.DescHash, j.PostedAt,
		j.CrawlID, j.Matched, j.Flags, j.FitScore, j.FitReason, j.Fingerprint).Scan(&id, &inserted)
	return id, inserted, err
}

// FinishCrawl applies the end of one company crawl exactly once:
//   - unchanged feed: every job seen by the previous crawl counts as seen again
//   - jobs not seen by this crawl get a miss; two misses in a row close them
//   - an empty result from a company that had jobs is treated as a broken
//     parser, not as "everything closed": nothing is closed and dropped=true
//
// A redelivered done message for the same crawl id changes nothing.
func (s *Store) FinishCrawl(ctx context.Context, d model.CrawlDone) (closed int64, dropped bool, err error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, false, err
	}
	defer tx.Rollback(ctx)

	var prevCrawl string
	var prevCount *int
	err = tx.QueryRow(ctx, `SELECT last_done_crawl_id, last_job_count FROM companies WHERE id = $1 FOR UPDATE`, d.CompanyID).Scan(&prevCrawl, &prevCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil // company was removed meanwhile
	}
	if err != nil {
		return 0, false, err
	}
	if prevCrawl == d.CrawlID {
		return 0, false, nil // already applied
	}

	if d.Failed != "" {
		_, err = tx.Exec(ctx, `UPDATE companies SET last_done_crawl_id = $2, last_crawled_at = now(), status_reason = $3 WHERE id = $1`,
			d.CompanyID, d.CrawlID, d.Failed)
		if err != nil {
			return 0, false, err
		}
		return 0, false, tx.Commit(ctx)
	}

	count := d.JobCount
	if d.Unchanged {
		if _, err = tx.Exec(ctx, `UPDATE jobs SET last_crawl_id = $3, last_seen_at = now() WHERE company_id = $1 AND last_crawl_id = $2 AND closed_at IS NULL`,
			d.CompanyID, prevCrawl, d.CrawlID); err != nil {
			return 0, false, err
		}
		if prevCount != nil {
			count = *prevCount
		}
	}
	dropped = !d.Unchanged && d.JobCount == 0 && prevCount != nil && *prevCount > 0
	if !dropped {
		err = tx.QueryRow(ctx, `
			WITH missed AS (
				UPDATE jobs SET missed_crawls = missed_crawls + 1,
				                closed_at = CASE WHEN missed_crawls + 1 >= 2 THEN now() ELSE NULL END
				WHERE company_id = $1 AND closed_at IS NULL AND last_crawl_id <> $2
				RETURNING closed_at)
			SELECT count(*) FILTER (WHERE closed_at IS NOT NULL) FROM missed`, d.CompanyID, d.CrawlID).Scan(&closed)
		if err != nil {
			return 0, false, err
		}
	}
	reason := ""
	if dropped {
		reason = "feed returned 0 jobs after having some; parser or feed may have changed"
		count = *prevCount
	}
	if _, err = tx.Exec(ctx, `
		UPDATE companies SET last_done_crawl_id = $2, last_crawled_at = now(), last_job_count = $3, fail_count = 0, status_reason = $4
		WHERE id = $1`, d.CompanyID, d.CrawlID, count, reason); err != nil {
		return 0, false, err
	}
	if d.PageID > 0 && d.Hash != "" && !d.Unchanged && !dropped {
		if _, err = tx.Exec(ctx, `UPDATE pages SET extracted_hash = $2 WHERE id = $1 AND content_hash = $2`, d.PageID, d.Hash); err != nil {
			return 0, false, err
		}
	}
	return closed, dropped, tx.Commit(ctx)
}
