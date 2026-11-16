// Package pipeline holds the three crawl stages as plain functions: fetch,
// extract, score. They know nothing about how messages travel, so the same
// code runs in one process (`jobradar crawl`) and across NATS workers.
//
// Every handler is idempotent: running it twice for the same input leaves
// the database exactly as running it once.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"jobradar/internal/boards"
	"jobradar/internal/crawl"
	"jobradar/internal/extract"
	"jobradar/internal/model"
	"jobradar/internal/store"
)

// ---- stage 1: fetch -----------------------------------------------------

type Fetcher struct {
	Store  *store.Store
	Client *crawl.Client
	Log    *slog.Logger
}

// Handle downloads the task's URL and stores the raw body. A returned error
// means "retry later"; permanent outcomes (blocked, 404) come back in Fetched.
func (f *Fetcher) Handle(ctx context.Context, t model.FetchTask) (model.Fetched, error) {
	out := model.Fetched{Task: t}
	res, err := f.Client.Fetch(ctx, t.URL)
	switch {
	case errors.Is(err, crawl.ErrBanned), errors.Is(err, crawl.ErrRobots):
		out.Blocked = err.Error()
		return out, nil
	case errors.Is(err, crawl.ErrTooLarge):
		out.Failed = err.Error()
		return out, nil
	case err != nil:
		return out, err
	}
	out.Status, out.FinalURL = res.Status, res.FinalURL
	if t.Kind == model.KindSitePage && res.Status == 200 && !strings.Contains(strings.ToLower(res.ContentType), "html") && !strings.Contains(strings.ToLower(res.ContentType), "xml") {
		out.Failed = "not an HTML page (" + res.ContentType + ")"
		return out, nil
	}
	if res.Status == 401 || res.Status == 403 {
		out.Blocked = fmt.Sprintf("site refused the crawler (http %d)", res.Status)
		return out, nil
	}
	sum := sha256.Sum256(res.Body)
	out.Hash = hex.EncodeToString(sum[:])
	out.PageID, out.Unchanged, err = f.Store.SavePage(ctx, t.CompanyID, t.URL, t.Kind, out.Hash, res.Status, false, res.Body)
	if t.Force || t.Kind == model.KindSitePage {
		out.Unchanged = false // site pages are always re-read: their links matter too
	}
	return out, err
}

// ---- stage 2: extract ---------------------------------------------------

type Extractor struct {
	Store *store.Store
	Log   *slog.Logger
	// CanRender reports whether a headless renderer is running (optional).
	CanRender func() bool
	// LLM is the last-resort extractor for pages nothing else understood (optional).
	LLM func(ctx context.Context, p extract.Page, pageURL string) (model.RawJob, bool)
}

// Extracted is everything one page yields.
type Extracted struct {
	Jobs   []model.RawJob
	Tasks  []model.FetchTask // follow-up pages to fetch
	Done   *model.CrawlDone  // set when this page completes the company's crawl
	Render *model.FetchTask  // set when the page must go through the headless renderer
	Note   string            // one line for the live view
}

func (e *Extractor) Handle(ctx context.Context, f model.Fetched) (Extracted, error) {
	if f.Task.Kind == model.KindSitePage {
		return e.sitePage(ctx, f)
	}
	t := f.Task
	done := &model.CrawlDone{CrawlID: t.CrawlID, CompanyID: t.CompanyID, Company: t.Company, SearchID: t.SearchID}
	switch {
	case f.Blocked != "":
		// Rule: if a site blocks the crawler, mark it "manual" and move on.
		if err := e.Store.MarkManual(ctx, t.CompanyID, f.Blocked); err != nil {
			return Extracted{}, err
		}
		done.Failed = f.Blocked
		return Extracted{Done: done}, nil
	case f.Failed != "":
		done.Failed = f.Failed
		return Extracted{Done: done}, nil
	case f.Status != 200:
		done.Failed = fmt.Sprintf("http %d", f.Status)
		return Extracted{Done: done}, nil
	case f.Unchanged:
		done.Unchanged = true
		return Extracted{Done: done}, nil
	}

	body, _, err := e.Store.PageBody(ctx, f.PageID)
	if err != nil {
		return Extracted{}, err
	}
	switch t.Kind {
	case model.KindBoardFeed:
		jobs, err := boards.Parse(t.BoardType, body)
		if err != nil {
			done.Failed = "feed did not parse: " + err.Error()
			return Extracted{Done: done}, nil
		}
		done.JobCount, done.PageID, done.Hash = len(jobs), f.PageID, f.Hash
		return Extracted{Jobs: jobs, Done: done}, nil
	}
	return Extracted{}, fmt.Errorf("no extractor for page kind %q", t.Kind)
}

// ---- stage 3: score -----------------------------------------------------

// Verdict is what scoring decides about one posting.
type Verdict struct {
	Experience  string
	SalaryMin   *int64
	SalaryMax   *int64
	SalaryState string
	SalaryText  string
	Matched     bool
	Flags       []string
	FitScore    int
	FitReason   string
}

// Judge scores a posting. Phase 1 uses NoJudge; Phase 2 plugs in the real one.
type Judge interface {
	Judge(ctx context.Context, companyID int64, j model.RawJob) (Verdict, error)
}

type NoJudge struct{}

func (NoJudge) Judge(context.Context, int64, model.RawJob) (Verdict, error) {
	return Verdict{SalaryState: "unknown"}, nil
}

type Scorer struct {
	Store *store.Store
	Judge Judge
	Log   *slog.Logger
}

// HandleJob saves one posting. It returns the job id and whether it is new.
func (s *Scorer) HandleJob(ctx context.Context, m model.JobFound) (id int64, inserted bool, v Verdict, err error) {
	v, err = s.Judge.Judge(ctx, m.CompanyID, m.Job)
	if err != nil {
		return 0, false, v, err
	}
	sum := sha256.Sum256([]byte(m.Job.Description))
	id, inserted, err = s.Store.UpsertJob(ctx, store.Job{
		CompanyID:   m.CompanyID,
		URL:         m.Job.URL,
		Title:       m.Job.Title,
		Location:    m.Job.Location,
		Remote:      m.Job.Remote,
		Department:  m.Job.Department,
		Experience:  v.Experience,
		SalaryMin:   v.SalaryMin,
		SalaryMax:   v.SalaryMax,
		SalaryState: v.SalaryState,
		SalaryText:  v.SalaryText,
		Description: m.Job.Description,
		DescHash:    hex.EncodeToString(sum[:8]),
		PostedAt:    m.Job.PostedAt,
		CrawlID:     m.CrawlID,
		Matched:     v.Matched,
		Flags:       v.Flags,
		FitScore:    v.FitScore,
		FitReason:   v.FitReason,
		Fingerprint: Fingerprint(m.CompanyID, m.Job.Title, m.Job.Location),
	})
	return id, inserted, v, err
}

// HandleDone closes a company crawl: bumps misses, closes vanished postings.
func (s *Scorer) HandleDone(ctx context.Context, d model.CrawlDone) error {
	closed, dropped, err := s.Store.FinishCrawl(ctx, d)
	if err != nil {
		return err
	}
	if dropped {
		s.Log.Warn("company dropped to zero jobs; nothing closed, check the parser", "company_id", d.CompanyID)
	}
	if closed > 0 {
		s.Log.Info("postings closed", "company_id", d.CompanyID, "closed", closed)
	}
	return nil
}

var nonAlnum = regexp.MustCompile(`[^\p{L}\p{N}]+`)

func normalise(s string) string {
	return strings.TrimSpace(nonAlnum.ReplaceAllString(strings.ToLower(s), " "))
}

// Fingerprint identifies a posting regardless of which URL it was found at:
// the same company, title and location is the same job.
func Fingerprint(companyID int64, title, location string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d|%s|%s", companyID, normalise(title), normalise(location))))
	return hex.EncodeToString(sum[:16])
}

// NewCrawlID names one crawl of one company.
func NewCrawlID(companyID int64) string {
	return fmt.Sprintf("c%d-%d", companyID, time.Now().UnixNano())
}

// TaskFor builds the first fetch task of a company crawl.
func TaskFor(c model.Company, crawlID, priority, searchID string) (model.FetchTask, error) {
	t := model.FetchTask{CrawlID: crawlID, CompanyID: c.ID, Company: c.Name, BoardType: c.BoardType, Priority: priority, SearchID: searchID}
	switch c.BoardType {
	case model.BoardGreenhouse, model.BoardLever, model.BoardAshby:
		u, err := boards.FeedURL(c.BoardType, c.BoardToken)
		if err != nil {
			return t, err
		}
		t.URL, t.Kind = u, model.KindBoardFeed
		return t, nil
	case model.BoardCustom:
		if c.CareersURL != "" {
			t.URL, t.Kind = c.CareersURL, model.KindSitePage
			return t, nil
		}
	}
	return t, fmt.Errorf("company %q has no crawlable source yet (board type %q)", c.Name, c.BoardType)
}
