package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"jobradar/internal/events"
)

// InsertEvents appends a batch to crawl_events, the permanent crawl log.
func (s *Store) InsertEvents(ctx context.Context, evs []events.Event) error {
	if len(evs) == 0 {
		return nil
	}
	rows := make([][]any, len(evs))
	for i, e := range evs {
		rows[i] = []any{e.TS, e.Service, e.CompanyID, e.URL, e.Stage, e.Outcome, e.DurationMS, e.Company, e.Detail, e.SearchID, e.N}
	}
	_, err := s.Pool.CopyFrom(ctx, pgx.Identifier{"crawl_events"},
		[]string{"ts", "service", "company_id", "url", "stage", "outcome", "duration_ms", "company", "detail", "search_id", "n"},
		pgx.CopyFromRows(rows))
	return err
}

// ListEvents returns the newest events first, optionally for one deep search.
func (s *Store) ListEvents(ctx context.Context, searchID string, limit int) ([]events.Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.Pool.Query(ctx, `
		SELECT id, ts, service, COALESCE(company_id, 0), company, url, stage, outcome, detail, n, duration_ms, search_id
		FROM crawl_events WHERE ($1 = '' OR search_id = $1) ORDER BY id DESC LIMIT $2`, searchID, limit)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (events.Event, error) {
		var e events.Event
		err := r.Scan(&e.ID, &e.TS, &e.Service, &e.CompanyID, &e.Company, &e.URL, &e.Stage, &e.Outcome, &e.Detail, &e.N, &e.DurationMS, &e.SearchID)
		return e, err
	})
	if out == nil {
		out = []events.Event{}
	}
	return out, err
}

type Counters struct {
	PagesFetched int `json:"pages_fetched"`
	JobsFound    int `json:"jobs_found"`
	Matches      int `json:"matches"`
	Errors       int `json:"errors"`
	Companies    int `json:"companies_crawled"`
}

// EventCounters summarises the crawl log since a point in time.
func (s *Store) EventCounters(ctx context.Context, since time.Time) (Counters, error) {
	var c Counters
	err := s.Pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE stage IN ('fetch', 'render') AND outcome IN ('ok', 'unchanged')),
		       COALESCE(sum(n) FILTER (WHERE stage = 'extract'), 0),
		       count(*) FILTER (WHERE stage = 'match'),
		       count(*) FILTER (WHERE outcome IN ('error', 'retry', 'dead', 'blocked')),
		       count(DISTINCT company_id) FILTER (WHERE stage = 'score')
		FROM crawl_events WHERE ts >= $1`, since).Scan(&c.PagesFetched, &c.JobsFound, &c.Matches, &c.Errors, &c.Companies)
	return c, err
}

// PruneEvents keeps the crawl log from growing without bound.
func (s *Store) PruneEvents(ctx context.Context, olderThan time.Duration) error {
	_, err := s.Pool.Exec(ctx, `DELETE FROM crawl_events WHERE ts < now() - make_interval(secs => $1)`, olderThan.Seconds())
	return err
}
