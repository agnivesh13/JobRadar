package pipeline

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"jobradar/internal/crawl"
	"jobradar/internal/model"
	"jobradar/internal/store"
)

// Local runs fetch -> extract -> score for each company inside one process,
// with no queue. This is the Phase 1 path and stays useful for debugging.
type Local struct {
	Fetcher   *Fetcher
	Extractor *Extractor
	Scorer    *Scorer
	Workers   int
	Force     bool // re-extract even when the page is unchanged
}

type LocalStats struct {
	Companies, Failed, Unchanged, Jobs, NewJobs atomic.Int64
}

func (l *Local) Run(ctx context.Context, companies []store.DueCompany) *LocalStats {
	stats := &LocalStats{}
	work := make(chan store.DueCompany)
	var wg sync.WaitGroup
	for i := 0; i < max(l.Workers, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range work {
				l.crawlCompany(ctx, c, stats)
			}
		}()
	}
	for _, c := range companies {
		select {
		case work <- c:
		case <-ctx.Done():
		}
	}
	close(work)
	wg.Wait()
	return stats
}

func (l *Local) crawlCompany(ctx context.Context, c store.DueCompany, stats *LocalStats) {
	log := l.Fetcher.Log.With("company", c.Name)
	stats.Companies.Add(1)
	task, err := TaskFor(c.Company, c.CrawlID, "normal", "")
	if err != nil {
		log.Warn("skipped", "err", err)
		stats.Failed.Add(1)
		return
	}
	task.Force = l.Force || c.Force
	if err := Start(ctx, l.Fetcher.Store, task); err != nil {
		log.Error("start", "err", err)
		return
	}
	if err := l.Fetcher.Store.MarkScheduled(ctx, c.ID); err != nil {
		log.Error("mark scheduled", "err", err)
		return
	}

	pending := []model.FetchTask{task}
	for len(pending) > 0 && ctx.Err() == nil {
		t := pending[0]
		pending = pending[1:]

		fetched, err := l.fetchWithRetry(ctx, t)
		if err != nil {
			log.Warn("fetch failed", "url", t.URL, "err", err)
			l.Scorer.HandleDone(ctx, model.CrawlDone{CrawlID: t.CrawlID, CompanyID: c.ID, Failed: err.Error()})
			stats.Failed.Add(1)
			return
		}
		ex, err := l.Extractor.Handle(ctx, fetched)
		if err != nil {
			log.Error("extract failed", "url", t.URL, "err", err)
			stats.Failed.Add(1)
			return
		}
		pending = append(pending, ex.Tasks...)
		newJobs := 0
		for _, j := range ex.Jobs {
			_, inserted, _, err := l.Scorer.HandleJob(ctx, model.JobFound{CrawlID: t.CrawlID, CompanyID: c.ID, Job: j})
			if err != nil {
				log.Error("save failed", "title", j.Title, "err", err)
				continue
			}
			if inserted {
				newJobs++
			}
		}
		stats.Jobs.Add(int64(len(ex.Jobs)))
		stats.NewJobs.Add(int64(newJobs))
		if ex.Done != nil {
			if err := l.Scorer.HandleDone(ctx, *ex.Done); err != nil {
				log.Error("finish failed", "err", err)
			}
			switch {
			case ex.Done.Failed != "":
				stats.Failed.Add(1)
				log.Warn("crawl failed", "reason", ex.Done.Failed)
			case ex.Done.Unchanged:
				stats.Unchanged.Add(1)
				log.Info("unchanged")
			default:
				log.Info("crawled", "jobs", len(ex.Jobs), "new", newJobs)
			}
		}
	}
}

func (l *Local) fetchWithRetry(ctx context.Context, t model.FetchTask) (model.Fetched, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		f, err := l.Fetcher.Handle(ctx, t)
		if err == nil {
			return f, nil
		}
		lastErr = err
		var retry *crawl.RetryableError
		if !errors.As(err, &retry) {
			return f, err
		}
		select {
		case <-ctx.Done():
			return f, ctx.Err()
		case <-time.After(min(retry.After, time.Minute)):
		}
	}
	return model.Fetched{Task: t}, lastErr
}
