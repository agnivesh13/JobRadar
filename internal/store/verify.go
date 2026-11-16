package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// VerifyCompany is what `jobradar verify` needs to know about a company.
type VerifyCompany struct {
	ID            int64
	Name          string
	BoardType     string
	BoardToken    string
	LastDoneCrawl string
	StatusReason  string
	InFlight      bool // scheduled or forced, and that crawl has not finished
}

func (s *Store) VerifyCompanies(ctx context.Context) ([]VerifyCompany, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, name, board_type, COALESCE(board_token, ''), last_done_crawl_id, status_reason,
		       force_next OR (scheduled_at IS NOT NULL AND (last_crawled_at IS NULL OR scheduled_at > last_crawled_at))
		FROM companies
		WHERE status = 'active' AND board_type IN ('greenhouse', 'lever', 'ashby')
		ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[VerifyCompany])
}

func (s *Store) PageBodyByURL(ctx context.Context, url string) ([]byte, error) {
	var body []byte
	err := s.Pool.QueryRow(ctx, `SELECT COALESCE(body, ''::bytea) FROM pages WHERE url = $1`, url).Scan(&body)
	return body, err
}

// OpenFingerprints returns the open jobs of a company stamped with crawlID.
func (s *Store) OpenFingerprints(ctx context.Context, companyID int64, crawlID string) (map[string]bool, error) {
	rows, err := s.Pool.Query(ctx, `SELECT fingerprint FROM jobs WHERE company_id = $1 AND last_crawl_id = $2 AND closed_at IS NULL`, companyID, crawlID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var fp string
		if err := rows.Scan(&fp); err != nil {
			return nil, err
		}
		out[fp] = true
	}
	return out, rows.Err()
}

// DuplicateJobs counts groups of rows that are the same posting twice.
func (s *Store) DuplicateJobs(ctx context.Context) (int, error) {
	var n int
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FROM (
			SELECT 1 FROM jobs
			GROUP BY company_id, lower(regexp_replace(title, '[^[:alnum:]]+', ' ', 'g')), lower(regexp_replace(location, '[^[:alnum:]]+', ' ', 'g'))
			HAVING count(*) > 1) d`).Scan(&n)
	return n, err
}
