package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---- settings ---------------------------------------------------------

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.Pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) PutSetting(ctx context.Context, key, value string) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, value)
	return err
}

// ---- company signal -----------------------------------------------------

type CompanySignal struct {
	PayTier        string
	FundingStage   string
	PaysAboveFloor bool
}

// CompanySignals returns, for every company, what is known about its pay:
// the hand-set tier, the funding stage, and whether any open posting states
// a yearly salary at or above floor (rupees per year).
func (s *Store) CompanySignals(ctx context.Context, floor int64) (map[int64]CompanySignal, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT c.id, c.pay_tier, COALESCE(c.funding_stage, ''),
		       EXISTS (SELECT 1 FROM jobs j WHERE j.company_id = c.id AND j.closed_at IS NULL
		               AND j.experience <> 'intern' AND j.salary_max >= $1)
		FROM companies c`, floor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]CompanySignal{}
	for rows.Next() {
		var id int64
		var sig CompanySignal
		if err := rows.Scan(&id, &sig.PayTier, &sig.FundingStage, &sig.PaysAboveFloor); err != nil {
			return nil, err
		}
		out[id] = sig
	}
	return out, rows.Err()
}

// ---- re-scoring ---------------------------------------------------------

// ScoreInput is the part of a stored job that scoring reads.
type ScoreInput struct {
	ID          int64
	CompanyID   int64
	Title       string
	Location    string
	Remote      bool
	Description string
	Experience  string
	SalaryMin   *int64
	SalaryMax   *int64
}

type ScoreUpdate struct {
	ID          int64
	Experience  string
	SalaryState string
	Matched     bool
	Flags       []string
	FitScore    int
	FitReason   string
}

// JobsForScoring pages through open jobs by id.
func (s *Store) JobsForScoring(ctx context.Context, afterID int64, limit int) ([]ScoreInput, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT id, company_id, title, location, remote, description, experience, salary_min, salary_max
		FROM jobs WHERE id > $1 AND closed_at IS NULL ORDER BY id LIMIT $2`, afterID, limit)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[ScoreInput])
}

func (s *Store) ApplyScores(ctx context.Context, updates []ScoreUpdate) error {
	batch := &pgx.Batch{}
	for _, u := range updates {
		if u.Flags == nil {
			u.Flags = []string{}
		}
		batch.Queue(`UPDATE jobs SET salary_state = $2, matched = $3, flags = $4,
			fit_score  = CASE WHEN llm_score IS NULL THEN $5 ELSE ($5 + llm_score) / 2 END,
			fit_reason = CASE WHEN llm_score IS NULL THEN $6 ELSE $6 || '; LLM: ' || llm_reason END,
			experience = $7
			WHERE id = $1`,
			u.ID, u.SalaryState, u.Matched, u.Flags, u.FitScore, u.FitReason, u.Experience)
	}
	return s.Pool.SendBatch(ctx, batch).Close()
}

// ---- jobs for the interface ---------------------------------------------

type JobRow struct {
	ID          int64      `json:"id"`
	CompanyID   int64      `json:"company_id"`
	Company     string     `json:"company"`
	PayTier     string     `json:"pay_tier"`
	Title       string     `json:"title"`
	Location    string     `json:"location"`
	Remote      bool       `json:"remote"`
	Experience  string     `json:"experience"`
	SalaryMin   *int64     `json:"salary_min"`
	SalaryMax   *int64     `json:"salary_max"`
	SalaryState string     `json:"salary_state"`
	SalaryText  *string    `json:"salary_text"`
	FitScore    int        `json:"fit_score"`
	FitReason   string     `json:"fit_reason"`
	Flags       []string   `json:"flags"`
	URL         string     `json:"url"`
	AltURLs     []string   `json:"alt_urls"`
	PostedAt    *time.Time `json:"posted_at"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	Updated     bool       `json:"updated"`
	Closed      bool       `json:"closed"`
	Status      string     `json:"status"` // application status, or "not_applied"
	Description string     `json:"description,omitempty"`
}

const jobRowCols = `j.id, j.company_id, c.name, c.pay_tier, j.title, j.location, j.remote, j.experience,
	j.salary_min, j.salary_max, j.salary_state, j.salary_text, j.fit_score, j.fit_reason, j.flags, j.url, j.alt_urls,
	j.posted_at, j.first_seen_at, (j.updated_at IS NOT NULL AND j.updated_at > j.first_seen_at + interval '1 hour'),
	j.closed_at IS NOT NULL, COALESCE(a.status, 'not_applied')`

const jobRowFrom = ` FROM jobs j JOIN companies c ON c.id = j.company_id LEFT JOIN applications a ON a.job_id = j.id `

func scanJobRow(row pgx.Row, withDescription bool) (JobRow, error) {
	var j JobRow
	dest := []any{&j.ID, &j.CompanyID, &j.Company, &j.PayTier, &j.Title, &j.Location, &j.Remote, &j.Experience,
		&j.SalaryMin, &j.SalaryMax, &j.SalaryState, &j.SalaryText, &j.FitScore, &j.FitReason, &j.Flags, &j.URL, &j.AltURLs,
		&j.PostedAt, &j.FirstSeenAt, &j.Updated, &j.Closed, &j.Status}
	if withDescription {
		dest = append(dest, &j.Description)
	}
	return j, row.Scan(dest...)
}

type JobFilter struct {
	Tab        string // stated | unknown | below | all
	Query      string // free text over title and company
	Location   string
	Experience string
	Days       int    // first seen within this many days; 0 = any
	Status     string // "" = everything except skipped; not_applied; or an application status
	Unmatched  bool   // include postings that fail the role/experience/location filters
	CompanyID  int64
	Sort       string // newest | fit | salary
	Limit      int
	Offset     int
}

func (s *Store) ListJobs(ctx context.Context, f JobFilter) (jobs []JobRow, total int, err error) {
	where := []string{"j.closed_at IS NULL"}
	var args []any
	add := func(cond string, arg any) {
		args = append(args, arg)
		where = append(where, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	switch f.Tab {
	case "stated":
		where = append(where, "j.salary_state = 'above'")
	case "unknown":
		where = append(where, "j.salary_state = 'unknown'")
	case "below":
		where = append(where, "j.salary_state = 'below'")
	}
	if !f.Unmatched {
		where = append(where, "j.matched")
	}
	if f.Query != "" {
		add("(j.title ILIKE ? OR c.name ILIKE ?)", "%"+f.Query+"%")
	}
	if f.Location != "" {
		add("j.location ILIKE ?", "%"+f.Location+"%")
	}
	if f.Experience != "" {
		add("j.experience = ?", f.Experience)
	}
	if f.Days > 0 {
		add("j.first_seen_at >= now() - make_interval(days => ?)", f.Days)
	}
	if f.CompanyID > 0 {
		add("j.company_id = ?", f.CompanyID)
	}
	switch f.Status {
	case "":
		where = append(where, "COALESCE(a.status, '') <> 'skipped'")
	case "any":
	case "not_applied":
		where = append(where, "a.job_id IS NULL")
	default:
		add("a.status = ?", f.Status)
	}
	order := "j.first_seen_at DESC, j.id DESC"
	switch f.Sort {
	case "fit":
		order = "j.fit_score DESC, j.first_seen_at DESC, j.id DESC"
	case "salary":
		order = "j.salary_max DESC NULLS LAST, j.fit_score DESC, j.id DESC"
	}
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 100
	}
	cond := " WHERE " + strings.Join(where, " AND ")

	if err = s.Pool.QueryRow(ctx, `SELECT count(*)`+jobRowFrom+cond, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	args = append(args, f.Limit, f.Offset)
	rows, err := s.Pool.Query(ctx, `SELECT `+jobRowCols+jobRowFrom+cond+
		fmt.Sprintf(` ORDER BY %s LIMIT $%d OFFSET $%d`, order, len(args)-1, len(args)), args...)
	if err != nil {
		return nil, 0, err
	}
	jobs, err = pgx.CollectRows(rows, func(r pgx.CollectableRow) (JobRow, error) { return scanJobRow(r, false) })
	if jobs == nil {
		jobs = []JobRow{}
	}
	return jobs, total, err
}

func (s *Store) GetJob(ctx context.Context, id int64) (JobRow, error) {
	return scanJobRow(s.Pool.QueryRow(ctx, `SELECT `+jobRowCols+`, j.description`+jobRowFrom+` WHERE j.id = $1`, id), true)
}

// ---- tracker ------------------------------------------------------------

type Application struct {
	JobID        int64      `json:"job_id"`
	Status       string     `json:"status"`
	AppliedAt    *time.Time `json:"applied_at"`
	ContactName  string     `json:"contact_name"`
	ContactEmail string     `json:"contact_email"`
	Notes        string     `json:"notes"`
	FollowUpOn   *string    `json:"follow_up_on"` // YYYY-MM-DD
	UpdatedAt    time.Time  `json:"updated_at"`
}

// ApplicationPatch changes only the fields that are non-nil.
type ApplicationPatch struct {
	Status       *string `json:"status"`
	ContactName  *string `json:"contact_name"`
	ContactEmail *string `json:"contact_email"`
	Notes        *string `json:"notes"`
	FollowUpOn   *string `json:"follow_up_on"` // "" clears it
}

var ApplicationStatuses = []string{"saved", "applied", "replied", "interview", "offer", "rejected", "skipped"}

// SaveApplication creates or updates the tracker row of a job. Status
// "not_applied" removes the row. applied_at is stamped the first time the
// status reaches "applied".
func (s *Store) SaveApplication(ctx context.Context, jobID int64, p ApplicationPatch) error {
	if p.Status != nil && *p.Status == "not_applied" {
		_, err := s.Pool.Exec(ctx, `DELETE FROM applications WHERE job_id = $1`, jobID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO applications (job_id, status, contact_name, contact_email, notes, follow_up_on, applied_at)
		VALUES ($1, COALESCE($2, 'saved'), COALESCE($3, ''), COALESCE($4, ''), COALESCE($5, ''), NULLIF($6, '')::date,
		        CASE WHEN $2 = 'applied' THEN now() END)
		ON CONFLICT (job_id) DO UPDATE SET
			status        = COALESCE($2, applications.status),
			contact_name  = COALESCE($3, applications.contact_name),
			contact_email = COALESCE($4, applications.contact_email),
			notes         = COALESCE($5, applications.notes),
			follow_up_on  = CASE WHEN $6::text IS NULL THEN applications.follow_up_on ELSE NULLIF($6, '')::date END,
			applied_at    = CASE WHEN $2 = 'applied' AND applications.applied_at IS NULL THEN now() ELSE applications.applied_at END,
			updated_at    = now()`,
		jobID, p.Status, p.ContactName, p.ContactEmail, p.Notes, p.FollowUpOn)
	return err
}

type TrackerRow struct {
	Job JobRow      `json:"job"`
	App Application `json:"application"`
}

// Tracker lists every job the user acted on, including closed postings.
func (s *Store) Tracker(ctx context.Context) ([]TrackerRow, error) {
	rows, err := s.Pool.Query(ctx, `SELECT `+jobRowCols+`,
		a.job_id, a.status, a.applied_at, a.contact_name, a.contact_email, a.notes, a.follow_up_on::text, a.updated_at
		FROM applications a JOIN jobs j ON j.id = a.job_id JOIN companies c ON c.id = j.company_id
		ORDER BY a.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (TrackerRow, error) {
		var t TrackerRow
		j, a := &t.Job, &t.App
		err := r.Scan(&j.ID, &j.CompanyID, &j.Company, &j.PayTier, &j.Title, &j.Location, &j.Remote, &j.Experience,
			&j.SalaryMin, &j.SalaryMax, &j.SalaryState, &j.SalaryText, &j.FitScore, &j.FitReason, &j.Flags, &j.URL, &j.AltURLs,
			&j.PostedAt, &j.FirstSeenAt, &j.Updated, &j.Closed, &j.Status,
			&a.JobID, &a.Status, &a.AppliedAt, &a.ContactName, &a.ContactEmail, &a.Notes, &a.FollowUpOn, &a.UpdatedAt)
		return t, err
	})
	if out == nil {
		out = []TrackerRow{}
	}
	return out, err
}

// ---- counters -----------------------------------------------------------

type Stats struct {
	Companies     int `json:"companies"`
	OpenJobs      int `json:"open_jobs"`
	Matched       int `json:"matched"`
	MatchedStated int `json:"matched_stated"`
	MatchedUnknow int `json:"matched_unknown"`
	Tracked       int `json:"tracked"`
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var st Stats
	err := s.Pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM companies WHERE status <> 'removed'),
		       count(*),
		       count(*) FILTER (WHERE matched AND salary_state <> 'below'),
		       count(*) FILTER (WHERE matched AND salary_state = 'above'),
		       count(*) FILTER (WHERE matched AND salary_state = 'unknown'),
		       (SELECT count(*) FROM applications WHERE status <> 'skipped')
		FROM jobs WHERE closed_at IS NULL`).Scan(&st.Companies, &st.OpenJobs, &st.Matched, &st.MatchedStated, &st.MatchedUnknow, &st.Tracked)
	return st, err
}
