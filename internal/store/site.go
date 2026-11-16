package store

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"jobradar/internal/model"
)

// ---- site-crawl frontier ---------------------------------------------------

// SeedCrawlPage registers the first page of a site crawl (idempotent).
func (s *Store) SeedCrawlPage(ctx context.Context, crawlID, url string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO crawl_pages (crawl_id, url) VALUES ($1, $2) ON CONFLICT DO NOTHING`, crawlID, url)
	return err
}

// SiteLink is a URL to add to a crawl's frontier.
type SiteLink struct {
	URL   string
	Depth int
}

// SitePageResult is what finishing one page of a site crawl returns.
type SitePageResult struct {
	Follow    []SiteLink // links to fetch: newly claimed, plus unfinished children on a redelivery
	Remaining int        // pages of this crawl not yet processed
	TotalJobs int        // jobs found by the whole crawl so far
	Pages     int        // pages in the crawl
}

// FinishSitePage marks one page of a site crawl processed and claims its
// outgoing links, in one transaction. Claiming is "insert if absent" capped
// at pageLimit pages per crawl, so two extractors never fetch the same URL
// and a crawl cannot run away.
//
// Called twice for the same page (redelivery), it changes nothing and returns
// the page's still-unprocessed children again, so tasks lost in a crash
// between commit and publish are re-published.
func (s *Store) FinishSitePage(ctx context.Context, crawlID, url string, jobs int, links []SiteLink, pageLimit int) (SitePageResult, error) {
	var res SitePageResult
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return res, err
	}
	defer tx.Rollback(ctx)

	// Serialise page completions of one crawl, so "remaining == 0" is seen by exactly one caller.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, crawlID); err != nil {
		return res, err
	}
	tag, err := tx.Exec(ctx, `UPDATE crawl_pages SET processed = true, jobs = $3 WHERE crawl_id = $1 AND url = $2 AND NOT processed`, crawlID, url, jobs)
	if err != nil {
		return res, err
	}
	first := tag.RowsAffected() == 1
	if first {
		if err = tx.QueryRow(ctx, `SELECT count(*) FROM crawl_pages WHERE crawl_id = $1`, crawlID).Scan(&res.Pages); err != nil {
			return res, err
		}
		for _, l := range links {
			if res.Pages >= pageLimit {
				break
			}
			tag, err := tx.Exec(ctx, `INSERT INTO crawl_pages (crawl_id, url, parent, depth) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`, crawlID, l.URL, url, l.Depth)
			if err != nil {
				return res, err
			}
			res.Pages += int(tag.RowsAffected())
		}
	}
	rows, err := tx.Query(ctx, `SELECT url, depth FROM crawl_pages WHERE crawl_id = $1 AND parent = $2 AND NOT processed`, crawlID, url)
	if err != nil {
		return res, err
	}
	if res.Follow, err = pgx.CollectRows(rows, pgx.RowToStructByPos[SiteLink]); err != nil {
		return res, err
	}
	err = tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE NOT processed), COALESCE(sum(jobs), 0), count(*) FROM crawl_pages WHERE crawl_id = $1`, crawlID).
		Scan(&res.Remaining, &res.TotalJobs, &res.Pages)
	if err != nil {
		return res, err
	}
	return res, tx.Commit(ctx)
}

// PruneCrawlPages drops frontiers of crawls that finished long ago.
func (s *Store) PruneCrawlPages(ctx context.Context) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM crawl_pages cp WHERE NOT EXISTS (
		SELECT 1 FROM companies c WHERE c.last_done_crawl_id = cp.crawl_id OR
		       (c.scheduled_at > now() - interval '1 day' AND cp.crawl_id LIKE 'c' || c.id || '-%'))`)
	return err
}

// ---- companies for the interface -----------------------------------------

type CompanyRow struct {
	model.Company
	StatusReason string `json:"status_reason"`
	SourceDetail string `json:"source_detail"`
	OpenJobs     int    `json:"open_jobs"`
	Matching     int    `json:"matching"`
	InFlight     bool   `json:"in_flight"`
}

func (s *Store) ListCompanies(ctx context.Context, query string, limit, offset int) (rows []CompanyRow, total int, err error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	like := "%" + query + "%"
	if err = s.Pool.QueryRow(ctx, `SELECT count(*) FROM companies WHERE status <> 'removed' AND ($1 = '%%' OR name ILIKE $1 OR domain ILIKE $1)`, like).Scan(&total); err != nil {
		return nil, 0, err
	}
	r, err := s.Pool.Query(ctx, `
		SELECT `+companyCols+`, c.status_reason, c.source_detail,
		       (SELECT count(*) FROM jobs j WHERE j.company_id = c.id AND j.closed_at IS NULL),
		       (SELECT count(*) FROM jobs j WHERE j.company_id = c.id AND j.closed_at IS NULL AND j.matched AND j.salary_state <> 'below'),
		       c.force_next OR (c.scheduled_at IS NOT NULL AND (c.last_crawled_at IS NULL OR c.scheduled_at > c.last_crawled_at) AND c.scheduled_at > now() - interval '15 minutes')
		FROM companies c
		WHERE c.status <> 'removed' AND ($1 = '%%' OR c.name ILIKE $1 OR c.domain ILIKE $1)
		ORDER BY 16 DESC, c.name LIMIT $2 OFFSET $3`, like, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	rows, err = pgx.CollectRows(r, func(row pgx.CollectableRow) (CompanyRow, error) {
		var c CompanyRow
		err := row.Scan(&c.ID, &c.Name, &c.Domain, &c.CareersURL, &c.BoardType, &c.BoardToken, &c.PayTier, &c.Source,
			&c.FundingStage, &c.Status, &c.LastCrawledAt, &c.LastJobCount, &c.StatusReason, &c.SourceDetail, &c.OpenJobs, &c.Matching, &c.InFlight)
		return c, err
	})
	if rows == nil {
		rows = []CompanyRow{}
	}
	return rows, total, err
}

// CompanyPatch changes only the non-nil fields.
type CompanyPatch struct {
	PayTier    *string `json:"pay_tier"`
	BoardType  *string `json:"board_type"`
	BoardToken *string `json:"board_token"`
	CareersURL *string `json:"careers_url"`
	Domain     *string `json:"domain"`
	Status     *string `json:"status"`
}

func (s *Store) UpdateCompany(ctx context.Context, id int64, p CompanyPatch) error {
	_, err := s.Pool.Exec(ctx, `
		UPDATE companies SET
			pay_tier    = COALESCE($2, pay_tier),
			board_type  = COALESCE($3, board_type),
			board_token = CASE WHEN $4::text IS NULL THEN board_token ELSE NULLIF($4, '') END,
			careers_url = CASE WHEN $5::text IS NULL THEN careers_url ELSE NULLIF($5, '') END,
			domain      = CASE WHEN $6::text IS NULL THEN domain ELSE NULLIF($6, '') END,
			status      = COALESCE($7, status),
			status_reason = CASE WHEN $7 = 'active' THEN '' ELSE status_reason END,
			fail_count  = CASE WHEN $7 = 'active' THEN 0 ELSE fail_count END,
			crawl_interval = CASE WHEN COALESCE($3, board_type) IN ('custom', 'unknown') THEN interval '24 hours' ELSE interval '6 hours' END
		WHERE id = $1`, id, p.PayTier, p.BoardType, p.BoardToken, p.CareersURL, p.Domain, p.Status)
	return err
}

// FindCompany looks a company up by exact name or domain.
func (s *Store) FindCompany(ctx context.Context, name, domain string) (model.Company, bool, error) {
	c, err := scanCompany(s.Pool.QueryRow(ctx, `SELECT `+companyCols+` FROM companies c
		WHERE status <> 'removed' AND (lower(name) = lower($1) OR ($2 <> '' AND lower(domain) = lower($2))) ORDER BY id LIMIT 1`, name, domain))
	if err == pgx.ErrNoRows {
		return c, false, nil
	}
	return c, err == nil, err
}

// KnownDomains returns every domain and board token already in the list, so
// discovery can skip what is known without a query per candidate.
func (s *Store) KnownDomains(ctx context.Context) (map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `SELECT lower(COALESCE(domain, '')), lower(name), board_type || ':' || lower(COALESCE(board_token, '')) FROM companies`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var d, n, b string
		if err := rows.Scan(&d, &n, &b); err != nil {
			return nil, err
		}
		out[d], out["name:"+n], out[b] = true, true, true
	}
	return out, rows.Err()
}

// RemoveCompany takes a company off the list. Its jobs stay (the tracker may
// reference them) but are closed, so they leave the results.
func (s *Store) RemoveCompany(ctx context.Context, id int64) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE companies SET status = 'removed', force_next = false WHERE id = $1`, id); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE jobs SET closed_at = now() WHERE company_id = $1 AND closed_at IS NULL`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SetSourceDetail(ctx context.Context, id int64, detail string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE companies SET source_detail = $2 WHERE id = $1`, id, detail)
	return err
}

// ---- discovery candidates -------------------------------------------------

type Candidate struct {
	ID           int64
	Name         string
	Website      string
	BoardType    string
	BoardToken   string
	Source       string
	Detail       string
	FundingStage string
}

// AddCandidate queues a company name for resolving. A name already queued
// (in any state) is not queued again.
func (s *Store) AddCandidate(ctx context.Context, c Candidate) (bool, error) {
	for _, p := range []*string{&c.Name, &c.Detail, &c.Website} {
		*p = strings.ReplaceAll(strings.ToValidUTF8(*p, ""), "\x00", "")
	}
	tag, err := s.Pool.Exec(ctx, `
		INSERT INTO candidates (name, website, board_type, board_token, source, detail, funding_stage)
		VALUES ($1, $2, $3, $4, $5, $6, $7) ON CONFLICT DO NOTHING`,
		c.Name, c.Website, c.BoardType, c.BoardToken, c.Source, c.Detail, c.FundingStage)
	return tag.RowsAffected() == 1, err
}

func (s *Store) NextCandidates(ctx context.Context, limit int) ([]Candidate, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, name, website, board_type, board_token, source, detail, funding_stage
		FROM candidates WHERE status = 'new' ORDER BY id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Candidate])
}

func (s *Store) MarkCandidate(ctx context.Context, id int64, status, note string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE candidates SET status = $2, note = $3, tried_at = now() WHERE id = $1`, id, status, note)
	return err
}

func (s *Store) SetFundingStage(ctx context.Context, companyID int64, stage string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE companies SET funding_stage = $2 WHERE id = $1 AND COALESCE(funding_stage, '') = ''`, companyID, stage)
	return err
}

// ---- LLM opinion and alerts -----------------------------------------------

// SetLLMScore stores the LLM's opinion and blends it into the fit score
// (the average of the rule-based score and the LLM's).
func (s *Store) SetLLMScore(ctx context.Context, jobID int64, llmScore int, reason string) (fit int, err error) {
	reason = strings.ReplaceAll(strings.ToValidUTF8(reason, ""), "\x00", "")
	err = s.Pool.QueryRow(ctx, `
		UPDATE jobs SET llm_score = $2, llm_reason = $3,
		       fit_score = (fit_score + $2) / 2, fit_reason = fit_reason || '; LLM: ' || $3
		WHERE id = $1 AND llm_score IS NULL RETURNING fit_score`, jobID, llmScore, reason).Scan(&fit)
	return fit, err
}

// ClaimAlert marks a job as alerted and reports whether this call was the
// first to do so, so a job is announced at most once.
func (s *Store) ClaimAlert(ctx context.Context, jobID int64) (bool, error) {
	tag, err := s.Pool.Exec(ctx, `UPDATE jobs SET alerted_at = now() WHERE id = $1 AND alerted_at IS NULL`, jobID)
	return tag.RowsAffected() == 1, err
}
