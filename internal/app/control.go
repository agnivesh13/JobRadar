package app

import (
	"context"
	"strconv"
	"sync"
	"time"
)

// Control is the runtime switchboard the Live screen edits: pause/resume and
// crawl speed. It lives in the settings table, so every container sees the
// same values; each one re-reads them at most every two seconds.
type Control struct {
	d *Deps

	mu       sync.Mutex
	loadedAt time.Time
	paused   bool
	interval time.Duration
}

const (
	KeyPaused   = "paused"
	KeyInterval = "domain_interval_ms"
)

func (d *Deps) Control() *Control {
	d.controlOnce.Do(func() { d.control = &Control{d: d, interval: d.Cfg.DomainInterval} })
	return d.control
}

func (c *Control) refresh() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.loadedAt) < 2*time.Second {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c.loadedAt = time.Now()
	paused, err := c.d.Store.GetSetting(ctx, KeyPaused)
	if err != nil {
		return // keep the last known values while the database is away
	}
	c.paused = paused == "1"
	c.interval = c.d.Cfg.DomainInterval
	if raw, err := c.d.Store.GetSetting(ctx, KeyInterval); err == nil {
		if ms, err := strconv.Atoi(raw); err == nil {
			c.interval = time.Duration(ms) * time.Millisecond
		}
	}
	// The politeness rule is a floor, not a default: never faster than the
	// configured per-domain interval (one request per second).
	c.interval = max(c.interval, c.d.Cfg.DomainInterval)
}

// Paused reports whether crawling is paused.
func (c *Control) Paused() bool {
	c.refresh()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.paused
}

// Interval is the current minimum gap between requests to one domain.
func (c *Control) Interval() time.Duration {
	c.refresh()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.interval
}

// Invalidate makes the next read fetch fresh values.
func (c *Control) Invalidate() {
	c.mu.Lock()
	c.loadedAt = time.Time{}
	c.mu.Unlock()
}
