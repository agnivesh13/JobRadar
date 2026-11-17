package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"jobradar/internal/boards"
	"jobradar/internal/config"
	"jobradar/internal/pipeline"
	"jobradar/internal/queue"
	"jobradar/internal/store"
)

func init() {
	commands["recrawl"] = runRecrawl
	commands["verify"] = runVerify
}

// runRecrawl marks every active company as due now, with re-extraction
// forced. The scheduler picks them up on its next tick.
func runRecrawl(ctx context.Context, cfg config.Config, _ []string) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	n, err := st.RequestCrawl(ctx, 0, "normal")
	slog.Info("recrawl requested", "companies", n)
	return err
}

// runVerify checks the invariants the pipeline promises, using the stored
// raw feeds as ground truth:
//
//  1. the queue is drained (nothing waiting, nothing in flight)
//  2. every active company finished its latest crawl
//  3. NO JOB LOST: every job in a company's stored feed is an open row
//     stamped with that company's latest crawl
//  4. NO DUPLICATE: no two rows share a company, title and location
//
// It exits non-zero if any invariant is broken.
func runVerify(ctx context.Context, cfg config.Config, _ []string) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	q, err := queue.Connect(ctx, cfg.NatsURL, "jobradar-verify")
	if err != nil {
		return err
	}
	defer q.Close()

	var problems []string
	fail := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }

	// 1. drained
	for _, c := range []string{queue.ConsFetchHigh, queue.ConsFetchNormal, queue.ConsExtract, queue.ConsScore} {
		n, err := q.Backlog(ctx, c)
		if err != nil {
			return err
		}
		if n > 0 {
			fail("queue not drained: consumer %s still has %d messages", c, n)
		}
	}
	dead := uint64(0)
	if s, err := q.JS.Stream(ctx, queue.StreamDLQ); err == nil {
		if info, err := s.Info(ctx); err == nil {
			dead = info.State.Msgs
		}
	} else if !errors.Is(err, jetstream.ErrStreamNotFound) {
		return err
	}

	// 2 + 3. per company
	companies, err := st.VerifyCompanies(ctx)
	if err != nil {
		return err
	}
	checked, failedCrawls, expectedTotal, extra := 0, 0, 0, 0
	for _, c := range companies {
		switch {
		case c.LastDoneCrawl == "":
			fail("%s: no crawl ever completed", c.Name)
			continue
		case c.InFlight:
			fail("%s: a scheduled crawl never completed", c.Name)
			continue
		case c.StatusReason != "":
			failedCrawls++ // the crawl completed with a recorded failure (404, timeout): not a lost job
			continue
		}
		feedURL, err := boards.FeedURL(c.BoardType, c.BoardToken)
		if err != nil {
			continue
		}
		body, err := st.PageBodyByURL(ctx, feedURL)
		if err != nil || len(body) == 0 {
			fail("%s: crawl completed but no stored feed", c.Name)
			continue
		}
		jobs, err := boards.ParseFeed(c.BoardType, feedURL, body)
		if err != nil {
			fail("%s: stored feed does not parse: %v", c.Name, err)
			continue
		}
		expected := map[string]string{}
		for _, j := range jobs {
			expected[pipeline.Fingerprint(c.ID, j.Title, j.Location)] = j.Title
		}
		actual, err := st.OpenFingerprints(ctx, c.ID, c.LastDoneCrawl)
		if err != nil {
			return err
		}
		for fp, title := range expected {
			if !actual[fp] {
				fail("%s: LOST JOB %q (in the feed, not in the database)", c.Name, title)
			}
		}
		for fp := range actual {
			if _, ok := expected[fp]; !ok {
				extra++
			}
		}
		expectedTotal += len(expected)
		checked++
	}

	// 4. duplicates
	dups, err := st.DuplicateJobs(ctx)
	if err != nil {
		return err
	}
	if dups > 0 {
		fail("DUPLICATES: %d groups of rows share a company, title and location", dups)
	}

	slog.Info("verify",
		"companies_checked", checked, "companies_with_failed_crawl", failedCrawls,
		"jobs_expected", expectedTotal, "rows_from_an_earlier_fetch_of_same_crawl", extra,
		"duplicate_groups", dups, "dead_letters", dead, "at", time.Now().Format(time.TimeOnly))
	if len(problems) > 0 {
		for i, p := range problems {
			if i == 25 {
				fmt.Printf("  ... and %d more\n", len(problems)-25)
				break
			}
			fmt.Println("  FAIL:", p)
		}
		return fmt.Errorf("%d invariant violations", len(problems))
	}
	fmt.Println("PASS: queue drained, every company completed, no job lost, no duplicate")
	return nil
}
