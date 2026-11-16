// Package app holds what every service shares: dependencies, the health
// endpoint and graceful shutdown.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"jobradar/internal/config"
	"jobradar/internal/queue"
	"jobradar/internal/store"
)

// Deps are the connections every service needs.
type Deps struct {
	Service string
	Cfg     config.Config
	Store   *store.Store
	Queue   *queue.Queue
	Log     *slog.Logger

	controlOnce sync.Once
	control     *Control
}

// Connect opens Postgres and NATS for the named service.
func Connect(ctx context.Context, service string, cfg config.Config) (*Deps, error) {
	log := slog.Default().With("service", service)
	st, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	q, err := queue.Connect(ctx, cfg.NatsURL, "jobradar-"+service)
	if err != nil {
		st.Close()
		return nil, err
	}
	return &Deps{Service: service, Cfg: cfg, Store: st, Queue: q, Log: log}, nil
}

func (d *Deps) Close() {
	d.Queue.Close()
	d.Store.Close()
}

// HealthHandler answers 200 only when both Postgres and NATS respond.
func (d *Deps) HealthHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		res := map[string]string{"service": d.Service, "postgres": "ok", "nats": "ok"}
		code := http.StatusOK
		if err := d.Store.Ping(ctx); err != nil {
			res["postgres"], code = err.Error(), http.StatusServiceUnavailable
		}
		if err := d.Queue.Ping(ctx); err != nil {
			res["nats"], code = err.Error(), http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(res)
	}
}

// Serve runs an HTTP server until ctx is cancelled, then shuts it down gracefully.
func Serve(ctx context.Context, addr string, h http.Handler, log *slog.Logger) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("http listening", "addr", addr)
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return nil
	}
}

// ServeHealth exposes /healthz for a worker in the background.
func (d *Deps) ServeHealth(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", d.HealthHandler())
	go func() {
		if err := Serve(ctx, d.Cfg.HealthAddr, mux, d.Log); err != nil {
			d.Log.Error("health server", "err", err)
		}
	}()
}
