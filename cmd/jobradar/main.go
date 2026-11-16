// jobradar is one binary with a subcommand per service, so a single Docker
// image runs every role.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"jobradar/internal/api"
	"jobradar/internal/app"
	"jobradar/internal/config"
	"jobradar/internal/salary"
	"jobradar/internal/score"
	"jobradar/internal/services"
	"jobradar/internal/store"
)

const usage = `usage: jobradar <command>

services:
  api         web interface, REST endpoints, live event stream
  scheduler   decides what to crawl next
  fetcher     downloads pages and feeds
  extractor   turns raw pages into job records
  scorer      filters, scores and saves jobs
  renderer    headless Chrome for JavaScript-heavy pages

tools:
  migrate     apply database migrations and exit
  seed [csv]  load companies from a CSV (default seeds/companies.csv)
  crawl       single-process crawl without the queue (-limit, -workers, -force)
  recrawl     ask the scheduler to re-crawl every company now
  verify      check pipeline invariants: nothing lost, nothing duplicated
  queue       show queue state (-dead lists dead letters, -reset wipes the work queue)
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cfg := config.Load()

	cmd, args := os.Args[1], os.Args[2:]
	run, ok := commands[cmd]
	if !ok {
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err := run(ctx, cfg, args); err != nil && ctx.Err() == nil {
		slog.Error("exited with error", "command", cmd, "err", err)
		os.Exit(1)
	}
}

type command func(ctx context.Context, cfg config.Config, args []string) error

var commands = map[string]command{
	"migrate": runMigrate,
	"api":     runAPI,
}

func runMigrate(ctx context.Context, cfg config.Config, _ []string) error {
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer st.Close()
	applied, err := st.Migrate(ctx)
	slog.Info("migrations", "applied", applied)
	return err
}

func runAPI(ctx context.Context, cfg config.Config, _ []string) error {
	d, err := app.Connect(ctx, "api", cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	judge := &score.Judge{Store: d.Store, Rates: rates(cfg)}
	srv := api.New(d, judge)
	if err := srv.StartLive(ctx); err != nil {
		return err
	}
	srv.StartCompanies()
	return app.Serve(ctx, cfg.HTTPAddr, srv.Handler(), d.Log)
}

// service wraps a background role: connect, expose /healthz, run until the
// shutdown signal, then let in-flight work finish.
func service(name string, run func(ctx context.Context, d *app.Deps) error) command {
	return func(ctx context.Context, cfg config.Config, _ []string) error {
		d, err := app.Connect(ctx, name, cfg)
		if err != nil {
			return err
		}
		defer d.Close()
		d.ServeHealth(ctx)
		d.Log.Info("ready")
		err = run(ctx, d)
		d.Log.Info("stopped")
		return err
	}
}

func init() {
	commands["scheduler"] = service("scheduler", services.RunScheduler)
	commands["fetcher"] = service("fetcher", func(ctx context.Context, d *app.Deps) error {
		return services.RunFetcher(ctx, d, d.Cfg.FetchConcurrency)
	})
	commands["extractor"] = service("extractor", func(ctx context.Context, d *app.Deps) error {
		return services.RunExtractor(ctx, d, d.Cfg.ExtractConcurrency)
	})
	commands["scorer"] = service("scorer", services.RunScorer)
	commands["renderer"] = service("renderer", services.RunRenderer)
}
func rates(cfg config.Config) salary.Rates {
	return salary.Rates{USD: cfg.USDToINR, EUR: cfg.EURToINR, GBP: cfg.GBPToINR}
}
