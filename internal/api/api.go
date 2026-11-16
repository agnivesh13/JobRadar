// Package api serves the web interface and its REST endpoints.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"github.com/jackc/pgx/v5"

	"jobradar/internal/app"
	"jobradar/internal/crawl"
	"jobradar/internal/events"
	"jobradar/internal/score"
	"jobradar/internal/store"
)

type Server struct {
	Deps  *app.Deps
	Judge *score.Judge
	Log   *slog.Logger

	hub       *hub
	searches  *searches
	emit      *events.Emitter
	client    *crawl.Client
	rescoring atomic.Bool
	pending   atomic.Bool // settings changed while a re-score was running
	// routes added by later phases register themselves here
	extra []func(mux *http.ServeMux)
}

func New(d *app.Deps, judge *score.Judge) *Server {
	return &Server{Deps: d, Judge: judge, Log: d.Log}
}

// Register lets another file of this package add routes.
func (s *Server) Register(fn func(mux *http.ServeMux)) { s.extra = append(s.extra, fn) }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.Deps.HealthHandler())

	mux.HandleFunc("GET /api/stats", s.stats)
	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.HandleFunc("PUT /api/jobs/{id}/application", s.saveApplication)
	mux.HandleFunc("GET /api/tracker", s.tracker)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings", s.putSettings)
	for _, fn := range s.extra {
		fn(mux)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such endpoint")
	})
	mux.Handle("/", spa(s.Deps.Cfg.WebDir))
	return mux
}

// spa serves the built React app; unknown paths fall back to index.html so
// the client-side router can handle them.
func spa(dir string) http.Handler {
	files := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, filepath.Clean("/"+r.URL.Path))
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			if _, err := os.Stat(filepath.Join(dir, "index.html")); err != nil {
				http.Error(w, "web app not built: run `npm run build` in web/", http.StatusNotFound)
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		files.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	s.Log.Error("request failed", "err", err)
	writeErr(w, http.StatusInternalServerError, err.Error())
}

func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id > 0
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.Deps.Store.Stats(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"stats": st, "rescoring": s.rescoring.Load()})
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	atoi := func(k string) int { n, _ := strconv.Atoi(q.Get(k)); return n }
	f := store.JobFilter{
		Tab: q.Get("tab"), Query: strings.TrimSpace(q.Get("q")), Location: strings.TrimSpace(q.Get("location")),
		Experience: q.Get("experience"), Days: atoi("days"), Status: q.Get("status"), Unmatched: q.Get("unmatched") == "1",
		CompanyID: int64(atoi("company_id")), Sort: q.Get("sort"), Limit: atoi("limit"), Offset: atoi("offset"),
	}
	jobs, total, err := s.Deps.Store.ListJobs(r.Context(), f)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": jobs, "total": total})
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	job, err := s.Deps.Store.GetJob(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, job)
}

func (s *Server) saveApplication(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(r)
	if !ok {
		writeErr(w, 400, "bad id")
		return
	}
	var p store.ApplicationPatch
	if !readJSON(w, r, &p) {
		return
	}
	if p.Status != nil && *p.Status != "not_applied" && !slices.Contains(store.ApplicationStatuses, *p.Status) {
		writeErr(w, 400, "unknown status")
		return
	}
	if _, err := s.Deps.Store.GetJob(r.Context(), id); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.Deps.Store.SaveApplication(r.Context(), id, p); err != nil {
		s.fail(w, err)
		return
	}
	job, err := s.Deps.Store.GetJob(r.Context(), id)
	if err != nil {
		s.fail(w, err)
		return
	}
	job.Description = ""
	writeJSON(w, 200, job)
}

func (s *Server) tracker(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Deps.Store.Tracker(r.Context())
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": rows, "statuses": store.ApplicationStatuses})
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	st, err := score.LoadSettings(r.Context(), s.Deps.Store)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"settings": st, "defaults": score.Defaults(), "llm_configured": s.Deps.Cfg.LLMBaseURL != "",
		"telegram_configured": s.Deps.Cfg.TelegramToken != ""})
}

// putSettings saves and then re-scores every open job in the background.
func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	var st score.Settings
	if !readJSON(w, r, &st) {
		return
	}
	if err := score.SaveSettings(r.Context(), s.Deps.Store, st); err != nil {
		s.fail(w, err)
		return
	}
	s.Rescore()
	s.getSettings(w, r)
}

// Rescore re-scores every open job in the background. A call that arrives
// while one is running is not lost: the running pass repeats once it ends.
func (s *Server) Rescore() {
	s.pending.Store(true)
	if !s.rescoring.CompareAndSwap(false, true) {
		return
	}
	go func() {
		for s.pending.Swap(false) {
			n, err := s.Judge.RescoreAll(context.Background())
			if err != nil {
				s.Log.Error("rescore failed", "err", err)
				break
			}
			s.Log.Info("rescored", "jobs", n)
		}
		s.rescoring.Store(false)
		if s.pending.Load() {
			s.Rescore()
		}
	}()
}
