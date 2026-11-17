package main

import (
	"context"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"jobradar/internal/config"
	"jobradar/internal/crawl"
	"jobradar/internal/model"
	"jobradar/internal/pipeline"
	"jobradar/internal/score"
	"jobradar/internal/store"
)

func init() {
	commands["seed"] = runSeed
	commands["crawl"] = runCrawl
}

// runSeed loads companies from a CSV with a header row. Columns are matched
// by name: name, board_type, board_token, domain, careers_url, pay_tier.
// Loading the same file twice adds nothing.
func runSeed(ctx context.Context, cfg config.Config, args []string) error {
	path := "seeds/companies.csv"
	if len(args) > 0 {
		path = args[0]
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	header, err := r.Read()
	if err != nil {
		return fmt.Errorf("read header: %w", err)
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.ToLower(strings.TrimSpace(h))] = i
	}
	if _, ok := col["name"]; !ok {
		return errors.New(`seed file needs a "name" column`)
	}

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	added, existing := 0, 0
	for line := 2; ; line++ {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("line %d: %w", line, err)
		}
		get := func(name string) string {
			if i, ok := col[name]; ok && i < len(rec) {
				return strings.TrimSpace(rec[i])
			}
			return ""
		}
		c := model.Company{
			Name: get("name"), BoardType: strings.ToLower(get("board_type")), BoardToken: get("board_token"),
			Domain: get("domain"), CareersURL: get("careers_url"), PayTier: get("pay_tier"), Source: "seed",
		}
		if c.Name == "" {
			continue
		}
		if c.BoardType == "" {
			c.BoardType = model.BoardUnknown
		}
		_, created, err := st.AddCompany(ctx, c)
		if err != nil {
			return fmt.Errorf("line %d (%s): %w", line, c.Name, err)
		}
		if created {
			added++
		} else {
			existing++
		}
	}
	slog.Info("seed loaded", "file", path, "added", added, "already_present", existing)
	return nil
}

// runCrawl is the single-process pipeline: fetch, extract and save every due
// company without the queue.
func runCrawl(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("crawl", flag.ExitOnError)
	limit := fs.Int("limit", 100000, "crawl at most this many companies")
	workers := fs.Int("workers", 6, "parallel companies (the per-domain rate limit still applies)")
	force := fs.Bool("force", false, "crawl every company now and re-extract even if the page is unchanged")
	fs.Parse(args)

	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()

	log := slog.Default()
	client := crawl.NewClient(cfg.UserAgent(), &store.Limiter{Store: st, Interval: cfg.DomainInterval}, cfg.FetchTimeout, cfg.MaxBodyBytes)
	local := &pipeline.Local{
		Fetcher:   &pipeline.Fetcher{Store: st, Client: client, Log: log},
		Extractor: &pipeline.Extractor{Store: st, Log: log, WorkdayCountry: cfg.WorkdayCountry},
		Scorer:    &pipeline.Scorer{Store: st, Judge: &score.Judge{Store: st, Rates: rates(cfg)}, Log: log},
		Workers:   *workers,
		Force:     *force,
	}
	if *force {
		if _, err := st.RequestCrawl(ctx, 0, "normal"); err != nil {
			return err
		}
	}
	companies, err := st.DueCompanies(ctx, *limit)
	if err != nil {
		return err
	}
	slog.Info("crawl starting", "due_companies", len(companies), "workers", *workers)
	start := time.Now()
	stats := local.Run(ctx, companies)
	slog.Info("crawl finished",
		"companies", stats.Companies.Load(), "failed", stats.Failed.Load(), "unchanged", stats.Unchanged.Load(),
		"jobs_seen", stats.Jobs.Load(), "new_jobs", stats.NewJobs.Load(), "took", time.Since(start).Round(time.Second))
	return nil
}
