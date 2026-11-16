package services

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"jobradar/internal/app"
	"jobradar/internal/crawl"
	"jobradar/internal/discover"
	"jobradar/internal/events"
	"jobradar/internal/queue"
	"jobradar/internal/resolve"
	"jobradar/internal/score"
	"jobradar/internal/store"
)

const (
	discoveryEvery  = 6 * time.Hour // how often the sources are read
	discoveryKey    = "discovery_last_run"
	resolvePerRound = 3  // candidates resolved per round, to stay gentle
	quietFetchQueue = 20 // discovery waits until polling known companies is nearly done
	maxPerSourceRun = 60 // new candidates accepted from one source per run
)

// discovery is the second loop of the plan: find companies not yet known and
// add them, so the list grows every session without manual additions. It
// runs inside the leader scheduler, one round at a time.
type discovery struct {
	d       *app.Deps
	ev      *events.Emitter
	client  *crawl.Client
	running atomic.Bool
}

func newDiscovery(d *app.Deps, ev *events.Emitter) *discovery {
	client := crawl.NewClient(d.Cfg.UserAgent(), &store.Limiter{Store: d.Store, Interval: d.Cfg.DomainInterval, Dynamic: d.Control().Interval}, 30*time.Second, 8<<20)
	return &discovery{d: d, ev: ev, client: client}
}

// Kick starts one round in the background unless one is already running.
func (x *discovery) Kick(ctx context.Context) {
	if !x.running.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer x.running.Store(false)
		rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		if err := x.round(rctx); err != nil && ctx.Err() == nil {
			x.d.Log.Warn("discovery round failed", "err", err)
		}
	}()
}

func (x *discovery) round(ctx context.Context) error {
	st := x.d.Store
	// Known companies come first: wait until the polling queue is nearly empty.
	n, err := x.d.Queue.Backlog(ctx, queue.ConsFetchNormal)
	if err != nil || n > quietFetchQueue {
		return err
	}
	last, _ := st.GetSetting(ctx, discoveryKey)
	if t, err := time.Parse(time.RFC3339, last); err != nil || time.Since(t) > discoveryEvery {
		x.readSources(ctx)
		if err := st.PutSetting(ctx, discoveryKey, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return x.resolveSome(ctx)
}

// readSources asks every source for names and queues the ones not yet known.
func (x *discovery) readSources(ctx context.Context) {
	known, err := x.d.Store.KnownDomains(ctx)
	if err != nil {
		return
	}
	queueUp := func(source string, cands []discover.Candidate, err error) {
		if err != nil {
			x.ev.Emit(events.Event{Stage: events.StageDiscover, Outcome: "error", Company: source, Detail: err.Error()})
			return
		}
		added := 0
		for _, c := range cands {
			if added >= maxPerSourceRun {
				break
			}
			if known["name:"+strings.ToLower(c.Name)] || (c.BoardToken != "" && known[c.BoardType+":"+strings.ToLower(c.BoardToken)]) {
				continue
			}
			ok, err := x.d.Store.AddCandidate(ctx, store.Candidate{Name: c.Name, Website: c.Website, BoardType: c.BoardType,
				BoardToken: c.BoardToken, Source: c.Source, Detail: c.Detail, FundingStage: c.FundingStage})
			if err == nil && ok {
				added++
			}
		}
		x.ev.Emit(events.Event{Stage: events.StageDiscover, Outcome: "ok", Company: source, N: added,
			Detail: fmt.Sprintf("%d names read, %d new candidates", len(cands), added)})
	}

	for _, feed := range discover.FundingFeeds {
		cands, err := discover.Funding(ctx, x.client, feed)
		queueUp("funding news: "+hostOf(feed), cands, err)
	}
	cands, err := discover.HackerNews(ctx, x.client)
	queueUp("Hacker News: Who is hiring", cands, err)
	cands, err = discover.YCombinator(ctx, x.client)
	queueUp("Y Combinator directory", cands, err)

	if x.d.Cfg.SearxngURL != "" {
		for _, q := range searchQueries(ctx, x.d.Store) {
			urls := resolve.Search(ctx, x.d.Cfg.SearxngURL, q)
			queueUp("search: "+q, discover.FromSearchResults(urls, q), nil)
		}
	}
}

// searchQueries builds a few queries from the user's own filters, e.g.
// "software engineer intern Hyderabad careers".
func searchQueries(ctx context.Context, st *store.Store) []string {
	s, err := score.LoadSettings(ctx, st)
	if err != nil {
		return nil
	}
	roles := []string{"software engineer intern", "software engineer new grad", "backend engineer fresher", "machine learning engineer intern"}
	var out []string
	for _, loc := range s.Locations {
		if loc == "anywhere" {
			continue
		}
		for _, role := range roles {
			out = append(out, role+" "+loc+" careers")
		}
	}
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	return strings.SplitN(u, "/", 2)[0]
}

// resolveSome turns a few queued names into companies. Each goes through the
// same resolver as deep search: site, careers page, board.
func (x *discovery) resolveSome(ctx context.Context) error {
	cands, err := x.d.Store.NextCandidates(ctx, resolvePerRound)
	if err != nil {
		return err
	}
	for _, c := range cands {
		if ctx.Err() != nil {
			return nil
		}
		status, note := x.resolveOne(ctx, c)
		if err := x.d.Store.MarkCandidate(ctx, c.ID, status, note); err != nil {
			return err
		}
		outcome := map[string]string{"added": "new", "known": "unchanged", "failed": "info"}[status]
		x.ev.Emit(events.Event{Stage: events.StageDiscover, Outcome: outcome, Company: c.Name, Detail: note + " (via " + c.Source + ")", URL: c.Website})
	}
	return nil
}

func (x *discovery) resolveOne(ctx context.Context, c store.Candidate) (status, note string) {
	var res resolve.Result
	if c.BoardToken != "" {
		res = resolve.Result{Name: c.Name, Board: c.BoardType, BoardToken: c.BoardToken}
	} else {
		input := c.Name
		if c.Website != "" {
			input = c.Website
		}
		var err error
		res, err = (&resolve.Resolver{Fetch: x.client, SearxngURL: x.d.Cfg.SearxngURL}).Resolve(ctx, input, "")
		if err != nil {
			return "failed", err.Error()
		}
		if res.Ambiguous {
			// Nobody is watching to pick, and guessing would add the wrong company.
			return "failed", "several websites could be this company; add it by hand from the Companies screen"
		}
		res.Name = c.Name
	}
	company, created, err := resolve.SaveNew(ctx, x.d.Store, res, resolve.Meta{Source: c.Source, Detail: c.Detail, FundingStage: c.FundingStage})
	if err != nil {
		return "failed", err.Error()
	}
	how := company.BoardType
	if company.BoardToken != "" {
		how += " board " + company.BoardToken
	} else if company.CareersURL != "" {
		how += " site " + company.CareersURL
	}
	if !created {
		return "known", "already in the list as " + company.Name
	}
	return "added", "added to the company list: " + how
}
