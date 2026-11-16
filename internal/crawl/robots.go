package crawl

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/temoto/robotstxt"
)

const (
	robotsTTL      = 24 * time.Hour
	robotsErrorTTL = 10 * time.Minute
	robotsAgent    = "JobRadar"
)

type robotsEntry struct {
	data    *robotstxt.RobotsData // nil = no rules, everything allowed
	denyAll bool                  // robots.txt unreachable (5xx / network): stay away for now
	expires time.Time
}

// robotsCache keeps each host's robots.txt for 24 hours.
type robotsCache struct {
	client *Client
	mu     sync.Mutex
	hosts  map[string]robotsEntry
}

func newRobotsCache(c *Client) *robotsCache {
	return &robotsCache{client: c, hosts: map[string]robotsEntry{}}
}

// Allowed follows RFC 9309: a 4xx for robots.txt means "no rules"; a 5xx or
// network failure means "do not crawl until it can be read".
//
// "Cannot read robots.txt right now" is reported as a RetryableError, never
// as a disallow: a disallow parks the company for good, and a passing outage
// (theirs or ours) must not do that.
func (rc *robotsCache) Allowed(ctx context.Context, u *url.URL) (bool, error) {
	key := u.Scheme + "://" + strings.ToLower(u.Host)
	rc.mu.Lock()
	entry, ok := rc.hosts[key]
	rc.mu.Unlock()

	if !ok || time.Now().After(entry.expires) {
		var err error
		entry, err = rc.load(ctx, key)
		if err != nil {
			return false, err // our own failure (rate limiter, shutdown): nothing learned, nothing cached
		}
		rc.mu.Lock()
		rc.hosts[key] = entry
		rc.mu.Unlock()
	}
	if entry.denyAll {
		return false, &RetryableError{Err: errors.New("robots.txt unavailable for " + u.Host), After: time.Minute}
	}
	if entry.data == nil {
		return true, nil
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return entry.data.TestAgent(path, robotsAgent), nil
}

func (rc *robotsCache) load(ctx context.Context, base string) (robotsEntry, error) {
	u, _ := url.Parse(base + "/robots.txt")
	res, err := rc.client.get(ctx, u)
	if err != nil {
		var retry *RetryableError
		if errors.As(err, &retry) {
			// The site itself failed (5xx, timeout): remember that briefly.
			return robotsEntry{denyAll: true, expires: time.Now().Add(robotsErrorTTL)}, nil
		}
		return robotsEntry{}, &RetryableError{Err: fmt.Errorf("robots.txt check: %w", err), After: 15 * time.Second}
	}
	if res.Status >= 400 {
		return robotsEntry{expires: time.Now().Add(robotsTTL)}, nil
	}
	data, err := robotstxt.FromBytes(res.Body)
	if err != nil {
		return robotsEntry{expires: time.Now().Add(robotsTTL)}, nil
	}
	return robotsEntry{data: data, expires: time.Now().Add(robotsTTL)}, nil
}
