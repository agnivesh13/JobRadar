// jobradar is one binary with a subcommand per service, so a single Docker
// image runs every role.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"jobradar/internal/app"
	"jobradar/internal/config"
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
	"migrate":   runMigrate,
	"api":       runAPI,
	"scheduler": worker("scheduler"),
	"fetcher":   worker("fetcher"),
	"extractor": worker("extractor"),
	"scorer":    worker("scorer"),
	"renderer":  worker("renderer"),
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
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", d.HealthHandler())
	return app.Serve(ctx, cfg.HTTPAddr, mux, d.Log)
}

// worker is the Phase 0 body of every background service: connect, expose
// /healthz, wait for shutdown. Later phases replace it per service.
func worker(name string) command {
	return func(ctx context.Context, cfg config.Config, _ []string) error {
		d, err := app.Connect(ctx, name, cfg)
		if err != nil {
			return err
		}
		defer d.Close()
		d.ServeHealth(ctx)
		d.Log.Info("ready")
		<-ctx.Done()
		d.Log.Info("shutting down")
		return nil
	}
}
