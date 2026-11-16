package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"jobradar/internal/app"
	"jobradar/internal/events"
	"jobradar/internal/queue"
	"jobradar/internal/store"
)

// hub fans crawl events out to every connected browser and keeps the
// counters the Live screen shows.
type hub struct {
	mu       sync.Mutex
	seq      int64
	subs     map[chan events.Event]struct{}
	counters store.Counters
	seen     map[int64]struct{}  // companies crawled this session
	workers  map[string]workerOK // instance -> last heartbeat
	since    time.Time
}

type workerOK struct {
	Service string
	Busy    int
	At      time.Time
}

func newHub() *hub {
	return &hub{subs: map[chan events.Event]struct{}{}, seen: map[int64]struct{}{}, workers: map[string]workerOK{}, since: time.Now()}
}

func (h *hub) subscribe() chan events.Event {
	ch := make(chan events.Event, 256)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *hub) unsubscribe(ch chan events.Event) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

func (h *hub) publish(evs []events.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range evs {
		h.seq++
		e.ID = h.seq
		switch {
		case e.Outcome == "error" || e.Outcome == "retry" || e.Outcome == "dead" || e.Outcome == "blocked":
			h.counters.Errors++
		case e.Stage == events.StageFetch || e.Stage == events.StageRender:
			h.counters.PagesFetched++
		case e.Stage == events.StageExtract:
			h.counters.JobsFound += e.N
		case e.Stage == events.StageMatch:
			h.counters.Matches++
		case e.Stage == events.StageScore && e.CompanyID > 0:
			h.seen[e.CompanyID] = struct{}{}
			h.counters.Companies = len(h.seen)
		}
		for ch := range h.subs {
			select {
			case ch <- e:
			default: // a slow browser drops lines rather than stalling everyone
			}
		}
	}
}

func (h *hub) beat(b events.Beat) {
	h.mu.Lock()
	h.workers[b.Instance] = workerOK{Service: b.Service, Busy: b.Busy, At: time.Now()}
	h.mu.Unlock()
}

// StartLive begins relaying events. It returns immediately.
func (s *Server) StartLive(ctx context.Context) error {
	s.hub = newHub()
	d := s.Deps
	if err := d.Queue.Setup(ctx); err != nil {
		return err
	}
	if err := events.Setup(ctx, d.Queue.JS); err != nil {
		return err
	}
	sub, err := d.Queue.NC.Subscribe(events.Heartbeat, func(m *nats.Msg) {
		var b events.Beat
		if json.Unmarshal(m.Data, &b) == nil {
			s.hub.beat(b)
		}
	})
	if err != nil {
		return err
	}
	go func() {
		<-ctx.Done()
		sub.Unsubscribe()
	}()
	// Store first, then show: what the browser saw is always in crawl_events.
	go events.Consume(ctx, d.Queue.JS, func(evs []events.Event) error {
		ictx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.Store.InsertEvents(ictx, evs); err != nil {
			return err
		}
		s.hub.publish(evs)
		return nil
	})
	go func() {
		t := time.NewTicker(time.Hour)
		defer t.Stop()
		for {
			d.Store.PruneEvents(ctx, 14*24*time.Hour)
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	s.Register(func(mux *http.ServeMux) {
		mux.HandleFunc("GET /api/live", s.live)
		mux.HandleFunc("GET /api/events", s.listEvents)
		mux.HandleFunc("GET /api/control", s.getControl)
		mux.HandleFunc("PUT /api/control", s.putControl)
	})
	return nil
}

type liveStatus struct {
	Counters   store.Counters `json:"counters"`
	Since      time.Time      `json:"since"`
	Queue      map[string]int `json:"queue"`
	QueueDepth int            `json:"queue_depth"`
	Workers    map[string]int `json:"workers"` // service -> live instances
	Busy       int            `json:"busy"`
	Paused     bool           `json:"paused"`
	IntervalMS int64          `json:"interval_ms"`
	MinMS      int64          `json:"min_interval_ms"`
}

func (s *Server) status(ctx context.Context) liveStatus {
	st := liveStatus{Queue: map[string]int{}, Workers: map[string]int{}}
	for _, c := range []string{queue.ConsFetchHigh, queue.ConsFetchNormal, queue.ConsRender, queue.ConsExtract, queue.ConsScore} {
		if n, err := s.Deps.Queue.Backlog(ctx, c); err == nil {
			st.Queue[c] = n
			st.QueueDepth += n
		}
	}
	h := s.hub
	h.mu.Lock()
	st.Counters, st.Since = h.counters, h.since
	for id, w := range h.workers {
		if time.Since(w.At) > 10*time.Second {
			delete(h.workers, id)
			continue
		}
		st.Workers[w.Service]++
		st.Busy += w.Busy
	}
	h.mu.Unlock()
	ctl := s.Deps.Control()
	st.Paused, st.IntervalMS, st.MinMS = ctl.Paused(), ctl.Interval().Milliseconds(), s.Deps.Cfg.DomainInterval.Milliseconds()
	return st
}

// live is the Server-Sent Events stream: "event" lines as they happen and a
// "status" line every two seconds. ?search_id= narrows it to one deep search.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, "streaming unsupported")
		return
	}
	searchID := r.URL.Query().Get("search_id")
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")

	send := func(kind string, v any) {
		data, _ := json.Marshal(v)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", kind, data)
	}
	ch := s.hub.subscribe()
	defer s.hub.unsubscribe(ch)
	send("status", s.status(r.Context()))
	flusher.Flush()

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-ch:
			if searchID == "" || e.SearchID == searchID {
				send("crawl", e)
			}
			// Drain what is already queued before flushing once.
			for n := len(ch); n > 0; n-- {
				if e := <-ch; searchID == "" || e.SearchID == searchID {
					send("crawl", e)
				}
			}
			flusher.Flush()
		case <-tick.C:
			send("status", s.status(r.Context()))
			flusher.Flush()
		}
	}
}

func (s *Server) listEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	evs, err := s.Deps.Store.ListEvents(r.Context(), r.URL.Query().Get("search_id"), limit)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"events": evs})
}

func (s *Server) getControl(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.status(r.Context()))
}

// putControl pauses or resumes crawling and changes the per-domain gap.
// The gap can be raised (slower) but never set below the configured floor.
func (s *Server) putControl(w http.ResponseWriter, r *http.Request) {
	var p struct {
		Paused     *bool  `json:"paused"`
		IntervalMS *int64 `json:"interval_ms"`
	}
	if !readJSON(w, r, &p) {
		return
	}
	st := s.Deps.Store
	if p.Paused != nil {
		v := "0"
		if *p.Paused {
			v = "1"
		}
		if err := st.PutSetting(r.Context(), app.KeyPaused, v); err != nil {
			s.fail(w, err)
			return
		}
	}
	if p.IntervalMS != nil {
		ms := min(max(*p.IntervalMS, s.Deps.Cfg.DomainInterval.Milliseconds()), 60_000)
		if err := st.PutSetting(r.Context(), app.KeyInterval, strconv.FormatInt(ms, 10)); err != nil {
			s.fail(w, err)
			return
		}
	}
	s.Deps.Control().Invalidate()
	writeJSON(w, 200, s.status(r.Context()))
}
