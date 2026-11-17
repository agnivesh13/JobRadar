package api

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"jobradar/internal/crawl"
	"jobradar/internal/events"
	"jobradar/internal/model"
	"jobradar/internal/pipeline"
	"jobradar/internal/queue"
	"jobradar/internal/resolve"
	"jobradar/internal/store"
)

// deepSearch is the state of one "type a company name" request.
type deepSearch struct {
	ID         string              `json:"search_id"`
	Name       string              `json:"name"`
	State      string              `json:"state"` // resolving | pick | crawling | failed
	Candidates []resolve.Candidate `json:"candidates,omitempty"`
	CompanyID  int64               `json:"company_id,omitempty"`
	Company    string              `json:"company,omitempty"`
	Error      string              `json:"error,omitempty"`
}

type searches struct {
	mu   sync.Mutex
	byID map[string]*deepSearch
}

func (s *searches) get(id string) (deepSearch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.byID[id]
	if !ok {
		return deepSearch{}, false
	}
	return *d, true
}

func (s *searches) update(id string, fn func(*deepSearch)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.byID[id]; ok {
		fn(d)
	}
}

// StartCompanies registers the Companies screen's endpoints and deep search.
func (s *Server) StartCompanies() {
	d := s.Deps
	s.searches = &searches{byID: map[string]*deepSearch{}}
	s.emit = &events.Emitter{NC: d.Queue.NC, Service: "api", Log: d.Log}
	s.client = crawl.NewClient(d.Cfg.UserAgent(), &store.Limiter{Store: d.Store, Interval: d.Cfg.DomainInterval, Dynamic: d.Control().Interval}, 30*time.Second, 8<<20)
	s.Register(func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/companies", s.listCompanies)
		mux.HandleFunc("POST /api/companies", s.addCompany)
		mux.HandleFunc("PATCH /api/companies/{id}", s.patchCompany)
		mux.HandleFunc("DELETE /api/companies/{id}", s.removeCompany)
		mux.HandleFunc("POST /api/companies/{id}/crawl", s.crawlCompany)
		mux.HandleFunc("POST /api/search", s.startSearch)
		mux.HandleFunc("GET /api/search/{id}", s.getSearch)
		mux.HandleFunc("POST /api/search/{id}/pick", s.pickSearch)
	})
}

func (s *Server) listCompanies(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	rows, total, err := s.Deps.Store.ListCompanies(r.Context(), strings.TrimSpace(q.Get("q")), limit, offset)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"companies": rows, "total": total})
}

// addCompany adds a company whose source is already known (board + token, or
// a careers URL). To add one by name alone, use deep search.
func (s *Server) addCompany(w http.ResponseWriter, r *http.Request) {
	var c model.Company
	if !readJSON(w, r, &c) {
		return
	}
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		writeErr(w, 400, "name is required")
		return
	}
	if b, tok, ok := boardFrom(c); ok {
		c.BoardType, c.BoardToken = b, tok
	} else if c.CareersURL != "" {
		c.BoardType = model.BoardCustom
	} else {
		writeErr(w, 400, "give a board type and token, or a careers URL; or use the search box to find it by name")
		return
	}
	c.Source = "manual"
	id, created, err := s.Deps.Store.AddCompany(r.Context(), c)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"id": id, "created": created})
}

func boardFrom(c model.Company) (string, string, bool) {
	if slices.Contains([]string{model.BoardGreenhouse, model.BoardLever, model.BoardAshby, model.BoardWorkday, model.BoardKeka, model.BoardDarwinbox, model.BoardSmartRecruiters, model.BoardAmazon, model.BoardEightfold, model.BoardApple}, c.BoardType) && c.BoardToken != "" {
		return c.BoardType, c.BoardToken, true
	}
	return "", "", false
}

func (s *Server) patchCompany(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var p store.CompanyPatch
	if !readJSON(w, r, &p) {
		return
	}
	if p.PayTier != nil && !slices.Contains([]string{"high", "medium", "low", "unknown"}, *p.PayTier) {
		writeErr(w, 400, "pay tier must be high, medium, low or unknown")
		return
	}
	if p.Status != nil && !slices.Contains([]string{"active", "manual"}, *p.Status) {
		writeErr(w, 400, "status must be active or manual")
		return
	}
	if err := s.Deps.Store.UpdateCompany(r.Context(), id, p); err != nil {
		s.fail(w, err)
		return
	}
	if p.PayTier != nil {
		s.Rescore() // the pay tier feeds the fit score of salary-unknown jobs
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) removeCompany(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	if err := s.Deps.Store.RemoveCompany(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// crawlCompany makes one company due now, at high priority.
func (s *Server) crawlCompany(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	n, err := s.Deps.Store.RequestCrawl(r.Context(), id, "high")
	if err != nil {
		s.fail(w, err)
		return
	}
	if n == 0 {
		writeErr(w, 409, "company is not active; set it active first")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- deep search ---------------------------------------------------------

func (s *Server) startSearch(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Name string `json:"name"`
	}
	if !readJSON(w, r, &p) {
		return
	}
	p.Name = strings.TrimSpace(p.Name)
	if len(p.Name) < 2 {
		writeErr(w, 400, "type a company name or its website")
		return
	}
	ds := &deepSearch{ID: fmt.Sprintf("s%d", time.Now().UnixNano()), Name: p.Name, State: "resolving"}
	s.searches.mu.Lock()
	s.searches.byID[ds.ID] = ds
	s.searches.mu.Unlock()
	go s.runSearch(ds.ID, p.Name, "")
	writeJSON(w, 200, ds)
}

func (s *Server) getSearch(w http.ResponseWriter, r *http.Request) {
	ds, ok := s.searches.get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, "unknown search")
		return
	}
	writeJSON(w, 200, ds)
}

func (s *Server) pickSearch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ds, ok := s.searches.get(id)
	var p struct {
		Domain string `json:"domain"`
	}
	if !ok || ds.State != "pick" {
		writeErr(w, 409, "this search is not waiting for a choice")
		return
	}
	if !readJSON(w, r, &p) {
		return
	}
	if !slices.ContainsFunc(ds.Candidates, func(c resolve.Candidate) bool { return c.Domain == p.Domain }) {
		writeErr(w, 400, "pick one of the offered websites")
		return
	}
	s.searches.update(id, func(d *deepSearch) { d.State = "resolving" })
	go s.runSearch(id, ds.Name, p.Domain)
	ds.State = "resolving"
	writeJSON(w, 200, ds)
}

// runSearch resolves the name, saves the company and queues a high-priority
// crawl tagged with the search id, so the panel can stream just this crawl.
func (s *Server) runSearch(id, name, pick string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	step := func(outcome, detail, u string) {
		s.emit.Emit(events.Event{Stage: events.StageResolve, Outcome: outcome, Company: name, Detail: detail, URL: u, SearchID: id})
	}
	failed := func(err error) {
		step("error", err.Error(), "")
		s.searches.update(id, func(d *deepSearch) { d.State, d.Error = "failed", err.Error() })
	}
	step("info", "resolving "+name, "")
	rs := &resolve.Resolver{Fetch: s.client, SearxngURL: s.Deps.Cfg.SearxngURL, Step: step}
	res, err := rs.Resolve(ctx, name, pick)
	if err != nil {
		failed(err)
		return
	}
	if res.Ambiguous {
		s.searches.update(id, func(d *deepSearch) { d.State, d.Candidates = "pick", res.Candidates })
		return
	}
	company, err := resolve.Save(ctx, s.Deps.Store, res, resolve.Meta{Source: "deep_search"})
	if err != nil {
		failed(err)
		return
	}
	task, err := pipeline.TaskFor(company, pipeline.NewCrawlID(company.ID), "high", id)
	if err != nil {
		failed(err)
		return
	}
	task.Force = true
	if err := pipeline.Start(ctx, s.Deps.Store, task); err != nil {
		failed(err)
		return
	}
	if err := s.Deps.Queue.Publish(ctx, queue.SubjFetchHigh, "t:"+task.CrawlID, task); err != nil {
		failed(err)
		return
	}
	s.Deps.Store.MarkScheduled(ctx, company.ID)
	step("ok", "crawl queued at high priority", task.URL)
	for _, more := range res.More {
		// Further career sites of the same company: saved, and crawled by the scheduler.
		if c, err := resolve.Save(ctx, s.Deps.Store, more, resolve.Meta{Source: "deep_search"}); err == nil {
			step("ok", "also added "+c.Name+" (crawled in the background)", "")
		}
	}
	s.searches.update(id, func(d *deepSearch) { d.State, d.CompanyID, d.Company = "crawling", company.ID, company.Name })
}
