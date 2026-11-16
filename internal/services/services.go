// Package services wires the pipeline stages to the queue: one Run function
// per container role.
package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"jobradar/internal/app"
	"jobradar/internal/crawl"
	"jobradar/internal/events"
	"jobradar/internal/model"
	"jobradar/internal/pipeline"
	"jobradar/internal/queue"
	"jobradar/internal/render"
	"jobradar/internal/salary"
	"jobradar/internal/score"
	"jobradar/internal/store"
)

// Back-pressure limits: the scheduler stops adding work while a later stage
// is this far behind, instead of filling the queue.
const (
	maxFetchBacklog   = 300
	maxExtractBacklog = 50
	maxScoreBacklog   = 5000
	schedulerLockID   = 7_201_602
	// One score message carries at most this many jobs and roughly this many
	// bytes, well under the 1 MB NATS message limit.
	maxChunkJobs  = 100
	maxChunkBytes = 400_000
	schedulerTick = 5 * time.Second
)

func short(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

// emitter builds the event publisher of a service and starts its heartbeat.
func emitter(ctx context.Context, d *app.Deps, busy *atomic.Int64) *events.Emitter {
	ev := &events.Emitter{NC: d.Queue.NC, Service: d.Service, Log: d.Log}
	host, _ := os.Hostname()
	go func() {
		t := time.NewTicker(3 * time.Second)
		defer t.Stop()
		for {
			ev.Beat(d.Service+"@"+host, int(busy.Load()))
			select {
			case <-ctx.Done():
				return
			case <-t.C:
			}
		}
	}()
	return ev
}

func fetchSubject(priority string) string {
	if priority == "high" {
		return queue.SubjFetchHigh
	}
	return queue.SubjFetchNormal
}

// ---- scheduler ----------------------------------------------------------

// RunScheduler decides what to crawl next. Several schedulers may run; only
// the one holding the Postgres leader lock publishes.
func RunScheduler(ctx context.Context, d *app.Deps) error {
	if err := d.Queue.Setup(ctx); err != nil {
		return err
	}
	var busy atomic.Int64
	ev := emitter(ctx, d, &busy)
	finder := newDiscovery(d, ev)
	lock := store.NewLeaderLock(d.Cfg.DatabaseURL, schedulerLockID)
	defer lock.Release()

	wasLeader := false
	lastPurge := time.Time{}
	ticker := time.NewTicker(schedulerTick)
	defer ticker.Stop()
	for {
		leader := lock.Hold(ctx)
		if leader != wasLeader {
			d.Log.Info("leadership changed", "leader", leader)
			wasLeader = leader
		}
		if leader {
			if err := scheduleTick(ctx, d, ev); err != nil && ctx.Err() == nil {
				d.Log.Warn("schedule tick failed", "err", err)
			}
			if !d.Control().Paused() {
				finder.Kick(ctx) // find new companies once known ones are polled
			}
			if time.Since(lastPurge) > time.Hour {
				d.Store.PruneCrawlPages(ctx)
				if n, err := d.Store.PurgeBodies(ctx, d.Cfg.BodyRetention); err == nil && n > 0 {
					d.Log.Info("old page bodies purged", "pages", n)
				}
				lastPurge = time.Now()
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func scheduleTick(ctx context.Context, d *app.Deps, ev *events.Emitter) error {
	if paused, err := d.Store.GetSetting(ctx, "paused"); err != nil || paused == "1" {
		return err
	}
	backlog := func(names ...string) (int, error) {
		total := 0
		for _, n := range names {
			b, err := d.Queue.Backlog(ctx, n)
			if err != nil {
				return 0, err
			}
			total += b
		}
		return total, nil
	}
	fetching, err := backlog(queue.ConsFetchHigh, queue.ConsFetchNormal, queue.ConsRender)
	if err != nil {
		return err
	}
	extracting, err := backlog(queue.ConsExtract)
	if err != nil {
		return err
	}
	scoring, err := backlog(queue.ConsScore)
	if err != nil {
		return err
	}
	if extracting > maxExtractBacklog || scoring > maxScoreBacklog {
		d.Log.Info("back-pressure: later stages are behind, not scheduling", "extract_backlog", extracting, "score_backlog", scoring)
		return nil
	}
	room := maxFetchBacklog - fetching
	if room <= 0 {
		return nil
	}
	due, err := d.Store.DueCompanies(ctx, room)
	if err != nil {
		return err
	}
	scheduled := 0
	for _, c := range due {
		task, err := pipeline.TaskFor(c.Company, c.CrawlID, c.Priority, "")
		if err != nil {
			// Nothing to crawl for this company yet; park it until it is edited.
			d.Store.MarkManual(ctx, c.ID, err.Error())
			continue
		}
		task.Force = c.Force
		if err := pipeline.Start(ctx, d.Store, task); err != nil {
			return err
		}
		// Publish first, mark second. A crash in between repeats the publish
		// with the same id next tick, which the queue drops as a duplicate.
		if err := d.Queue.Publish(ctx, fetchSubject(c.Priority), "t:"+c.CrawlID, task); err != nil {
			return err
		}
		if err := d.Store.MarkScheduled(ctx, c.ID); err != nil {
			return err
		}
		ev.Emit(events.Event{Stage: events.StageSchedule, Outcome: "ok", CompanyID: c.ID, Company: c.Name, URL: task.URL, Detail: c.Priority + " priority"})
		scheduled++
	}
	if scheduled > 0 {
		d.Log.Info("scheduled", "companies", scheduled, "fetch_backlog", fetching)
	}
	return nil
}

// ---- fetcher --------------------------------------------------------------

func RunFetcher(ctx context.Context, d *app.Deps, concurrency int) error {
	if err := d.Queue.Setup(ctx); err != nil {
		return err
	}
	var busy atomic.Int64
	ev := emitter(ctx, d, &busy)
	client := crawl.NewClient(d.Cfg.UserAgent(), &store.Limiter{Store: d.Store, Interval: d.Cfg.DomainInterval, Dynamic: d.Control().Interval}, d.Cfg.FetchTimeout, d.Cfg.MaxBodyBytes)
	fetcher := &pipeline.Fetcher{Store: d.Store, Client: client, Log: d.Log}

	return d.Queue.Consume(ctx, queue.ConsumeOptions{
		Consumers:   []string{queue.ConsFetchHigh, queue.ConsFetchNormal}, // high priority first
		Concurrency: concurrency,
		Log:         d.Log,
		Paused:      d.Control().Paused,
		// A fetch that failed for good still has to close its crawl, or the
		// company would look "in flight" until the scheduler's timeout.
		OnDead: func(ctx context.Context, data []byte, cause error) {
			var t model.FetchTask
			if json.Unmarshal(data, &t) == nil {
				ev.Emit(events.Event{Stage: events.StageFetch, Outcome: "dead", CompanyID: t.CompanyID, Company: t.Company, URL: t.URL, Detail: cause.Error(), SearchID: t.SearchID})
			}
			if json.Unmarshal(data, &t) == nil {
				// Hand it on as a failed page, so the crawl it belongs to still completes.
				failed := model.Fetched{Task: t, Failed: "fetch failed: " + cause.Error()}
				d.Queue.Publish(ctx, queue.SubjFetched, "f:"+t.CrawlID+":"+short(t.URL+"dead"), failed)
			}
		},
	}, func(ctx context.Context, data []byte, attempt int) error {
		var t model.FetchTask
		if err := json.Unmarshal(data, &t); err != nil {
			d.Log.Error("dropping unreadable fetch task", "err", err)
			return nil
		}
		busy.Add(1)
		defer busy.Add(-1)
		start := time.Now()
		fetched, err := fetcher.Handle(ctx, t)
		fe := events.Event{Stage: events.StageFetch, Outcome: "ok", CompanyID: t.CompanyID, Company: t.Company, URL: t.URL, SearchID: t.SearchID, DurationMS: int(time.Since(start).Milliseconds())}
		switch {
		case err != nil:
			fe.Outcome, fe.Detail = "retry", fmt.Sprintf("attempt %d: %v", attempt, err)
		case fetched.Blocked != "":
			fe.Outcome, fe.Detail = "blocked", fetched.Blocked
		case fetched.Failed != "":
			fe.Outcome, fe.Detail = "error", fetched.Failed
		case fetched.Status != 200:
			fe.Outcome, fe.Detail = "error", fmt.Sprintf("http %d", fetched.Status)
		case fetched.Unchanged:
			fe.Outcome = "unchanged"
		}
		ev.Emit(fe)
		if err != nil {
			var retry *crawl.RetryableError
			if errors.As(err, &retry) {
				return &queue.RetryAfter{Delay: retry.After, Err: err}
			}
			return err
		}
		// The id includes the content hash: a redelivered task that downloads
		// different content is new work; identical content is a duplicate.
		return d.Queue.Publish(ctx, queue.SubjFetched, "f:"+t.CrawlID+":"+short(t.URL+fetched.Hash+fetched.Blocked+fetched.Failed), fetched)
	})
}

// ---- extractor --------------------------------------------------------------

func RunExtractor(ctx context.Context, d *app.Deps, concurrency int) error {
	if err := d.Queue.Setup(ctx); err != nil {
		return err
	}
	var busy atomic.Int64
	ev := emitter(ctx, d, &busy)
	extractor := &pipeline.Extractor{Store: d.Store, Log: d.Log}
	extractor.CanRender = func() bool { return d.Queue.HasWorkers(ctx, queue.ConsRender) }
	extractor.LLM = llmExtractor(d)

	return d.Queue.Consume(ctx, queue.ConsumeOptions{
		Consumers: []string{queue.ConsExtract}, Concurrency: concurrency, Log: d.Log,
	}, func(ctx context.Context, data []byte, attempt int) error {
		var f model.Fetched
		if err := json.Unmarshal(data, &f); err != nil {
			d.Log.Error("dropping unreadable fetched message", "err", err)
			return nil
		}
		busy.Add(1)
		defer busy.Add(-1)
		start := time.Now()
		ex, err := extractor.Handle(ctx, f)
		if err != nil {
			return err
		}
		t := f.Task
		ee := events.Event{Stage: events.StageExtract, Outcome: "ok", CompanyID: t.CompanyID, Company: t.Company, URL: t.URL, SearchID: t.SearchID, N: len(ex.Jobs), DurationMS: int(time.Since(start).Milliseconds())}
		switch {
		case ex.Done != nil && ex.Done.Failed != "":
			ee.Outcome, ee.Detail = "error", ex.Done.Failed
		case ex.Done != nil && ex.Done.Unchanged:
			ee.Outcome = "unchanged"
		case ex.Note != "":
			ee.Detail = ex.Note
			if len(ex.Jobs) == 0 && ex.Render == nil && len(ex.Tasks) == 0 {
				ee.Outcome = "error"
			}
		case t.Kind == model.KindSitePage:
			ee.Detail = fmt.Sprintf("%d jobs, %d links to follow", len(ex.Jobs), len(ex.Tasks))
		default:
			ee.Detail = fmt.Sprintf("%d jobs", len(ex.Jobs))
		}
		ev.Emit(ee)
		// Order matters: jobs first, the crawl-done marker last. All of it is
		// published before the ack, so a crash anywhere here redelivers the
		// page and the ids below make the repeat publishes no-ops.
		// Jobs go out in chunks (see model.ScoreMsg). A chunk's id is derived
		// from its content, so a redelivered page republishes identical chunks
		// and the queue drops them as duplicates.
		var batch []queue.Item
		var chunk []model.JobFound
		size, ids := 0, ""
		flush := func() {
			if len(chunk) > 0 {
				batch = append(batch, queue.Item{Subject: queue.SubjScore, MsgID: "j:" + t.CrawlID + ":" + short(ids), Value: model.ScoreMsg{Jobs: chunk}})
			}
			chunk, size, ids = nil, 0, ""
		}
		for _, job := range ex.Jobs {
			chunk = append(chunk, model.JobFound{CrawlID: t.CrawlID, CompanyID: t.CompanyID, Company: t.Company, SearchID: t.SearchID, Job: job})
			size += len(job.Description) + len(job.Title) + len(job.URL) + 300
			ids += pipeline.Fingerprint(t.CompanyID, job.Title, job.Location) + job.URL
			if len(chunk) >= maxChunkJobs || size >= maxChunkBytes {
				flush()
			}
		}
		flush()
		if err := d.Queue.PublishBatch(ctx, batch); err != nil {
			return err
		}
		if ex.Render != nil {
			if err := d.Queue.Publish(ctx, queue.SubjRender, "r:"+t.CrawlID+":"+short(t.URL), ex.Render); err != nil {
				return err
			}
		}
		for _, next := range ex.Tasks {
			if err := d.Queue.Publish(ctx, fetchSubject(next.Priority), "t:"+next.CrawlID+":"+short(next.URL), next); err != nil {
				return err
			}
		}
		if ex.Done != nil {
			if err := d.Queue.Publish(ctx, queue.SubjScore, "d:"+t.CrawlID+":"+short(f.Hash+ex.Done.Failed), model.ScoreMsg{Done: ex.Done}); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- scorer -----------------------------------------------------------------

// RunScorer saves jobs. Several messages are handled at once, so a crawl's
// "done" marker can be applied while its last few jobs are still in flight.
// That is safe because the handlers commute: a job saved after the marker
// resets its own miss counter and reopens itself, ending in the same state.
func RunScorer(ctx context.Context, d *app.Deps) error {
	if err := d.Queue.Setup(ctx); err != nil {
		return err
	}
	judge := &score.Judge{Store: d.Store, Rates: salary.Rates{USD: d.Cfg.USDToINR, EUR: d.Cfg.EURToINR, GBP: d.Cfg.GBPToINR}}
	var busy atomic.Int64
	ev := emitter(ctx, d, &busy)
	scorer := &pipeline.Scorer{Store: d.Store, Judge: judge, Log: d.Log}
	extras := newAfterMatch(d)

	return d.Queue.Consume(ctx, queue.ConsumeOptions{
		Consumers: []string{queue.ConsScore}, Concurrency: d.Cfg.ScoreConcurrency, Log: d.Log,
	}, func(ctx context.Context, data []byte, attempt int) error {
		var m model.ScoreMsg
		if err := json.Unmarshal(data, &m); err != nil {
			d.Log.Error("dropping unreadable score message", "err", err)
			return nil
		}
		busy.Add(1)
		defer busy.Add(-1)
		if m.Job != nil {
			m.Jobs = append(m.Jobs, *m.Job)
		}
		// Every job of the chunk is attempted even if one fails; any failure
		// retries the whole chunk, which is harmless because saving is idempotent.
		var firstErr error
		for _, job := range m.Jobs {
			jobID, inserted, v, err := scorer.HandleJob(ctx, job)
			if err != nil {
				if firstErr == nil {
					firstErr = fmt.Errorf("%s (%s): %w", job.Job.Title, job.Job.URL, err)
				}
				continue
			}
			if inserted && v.Matched && v.SalaryState != score.Below {
				v.FitScore = extras.Run(ctx, jobID, job, v)
				ev.Emit(events.Event{Stage: events.StageMatch, Outcome: "new", CompanyID: job.CompanyID, Company: job.Company, URL: job.Job.URL, SearchID: job.SearchID,
					Detail: fmt.Sprintf("%s (fit %d)", job.Job.Title, v.FitScore)})
			}
		}
		if firstErr != nil {
			return firstErr
		}
		switch {
		case m.Done != nil:
			err := scorer.HandleDone(ctx, *m.Done)
			if err == nil {
				de := events.Event{Stage: events.StageScore, Outcome: "ok", CompanyID: m.Done.CompanyID, Company: m.Done.Company, SearchID: m.Done.SearchID, N: m.Done.JobCount, Detail: fmt.Sprintf("crawl complete, %d jobs", m.Done.JobCount)}
				switch {
				case m.Done.Failed != "":
					de.Outcome, de.Detail = "error", m.Done.Failed
				case m.Done.Unchanged:
					de.Outcome, de.Detail = "unchanged", "crawl complete, nothing changed"
				}
				ev.Emit(de)
			}
			return err
		}
		return nil
	})
}

// ---- renderer -----------------------------------------------------------------

// RunRenderer loads pages in headless Chrome for the extractor. It obeys the
// same rules as the fetcher (never-crawl list, robots.txt, rate limit).
func RunRenderer(ctx context.Context, d *app.Deps) error {
	if err := d.Queue.Setup(ctx); err != nil {
		return err
	}
	var busy atomic.Int64
	ev := emitter(ctx, d, &busy)
	client := crawl.NewClient(d.Cfg.UserAgent(), &store.Limiter{Store: d.Store, Interval: d.Cfg.DomainInterval, Dynamic: d.Control().Interval}, d.Cfg.FetchTimeout, d.Cfg.MaxBodyBytes)
	browser := render.New(d.Cfg.UserAgent())
	defer browser.Close()

	failed := func(ctx context.Context, t model.FetchTask, why string) error {
		return d.Queue.Publish(ctx, queue.SubjFetched, "f:"+t.CrawlID+":"+short(t.URL+"render"+why), model.Fetched{Task: t, Failed: why})
	}
	return d.Queue.Consume(ctx, queue.ConsumeOptions{
		Consumers: []string{queue.ConsRender}, Concurrency: d.Cfg.RendererTabs, Log: d.Log, Paused: d.Control().Paused,
		OnDead: func(ctx context.Context, data []byte, cause error) {
			var t model.FetchTask
			if json.Unmarshal(data, &t) == nil {
				failed(ctx, t, "render failed: "+cause.Error())
			}
		},
	}, func(ctx context.Context, data []byte, attempt int) error {
		var t model.FetchTask
		if err := json.Unmarshal(data, &t); err != nil {
			return nil
		}
		busy.Add(1)
		defer busy.Add(-1)
		start := time.Now()
		re := events.Event{Stage: events.StageRender, Outcome: "ok", CompanyID: t.CompanyID, Company: t.Company, URL: t.URL, SearchID: t.SearchID}
		emit := func(outcome, detail string) {
			re.Outcome, re.Detail, re.DurationMS = outcome, detail, int(time.Since(start).Milliseconds())
			ev.Emit(re)
		}
		if err := client.Allow(ctx, t.URL); err != nil {
			if errors.Is(err, crawl.ErrBanned) || errors.Is(err, crawl.ErrRobots) {
				emit("blocked", err.Error())
				return d.Queue.Publish(ctx, queue.SubjFetched, "f:"+t.CrawlID+":"+short(t.URL+"renderblocked"), model.Fetched{Task: t, Blocked: err.Error()})
			}
			emit("retry", err.Error())
			return err
		}
		html, finalURL, err := browser.HTML(ctx, t.URL)
		if err != nil {
			emit("retry", fmt.Sprintf("attempt %d: %v", attempt, err))
			return err
		}
		sum := sha256.Sum256([]byte(html))
		hash := hex.EncodeToString(sum[:])
		pageID, _, err := d.Store.SavePage(ctx, t.CompanyID, t.URL, t.Kind, hash, 200, true, []byte(html))
		if err != nil {
			return err
		}
		emit("ok", fmt.Sprintf("%d KB after JavaScript", len(html)/1024))
		return d.Queue.Publish(ctx, queue.SubjFetched, "f:"+t.CrawlID+":"+short(t.URL+hash), model.Fetched{Task: t, PageID: pageID, Status: 200, FinalURL: finalURL, Hash: hash})
	})
}
