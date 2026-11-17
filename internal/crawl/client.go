// Package crawl is the polite HTTP side of the crawler: the fetch client,
// robots.txt, the banned-site list and the per-domain rate limit.
package crawl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Limiter spaces out requests to one domain across every worker.
type Limiter interface {
	// Wait blocks until this caller may send one request to domain.
	Wait(ctx context.Context, domain string) error
	// Penalize pushes the domain's next free slot at least d into the future.
	Penalize(ctx context.Context, domain string, d time.Duration) error
}

var (
	// ErrBanned: the site is on the never-crawl list.
	ErrBanned = errors.New("site is on the never-crawl list")
	// ErrRobots: robots.txt disallows this URL for us.
	ErrRobots = errors.New("disallowed by robots.txt")
	// ErrTooLarge: the body exceeds the size cap. Retrying cannot help.
	ErrTooLarge = errors.New("body larger than the size cap")
)

// RetryableError means "try again later": 429, 5xx or a network failure.
type RetryableError struct {
	Status int
	After  time.Duration
	Err    error
}

func (e *RetryableError) Error() string {
	if e.Err != nil {
		return "retryable: " + e.Err.Error()
	}
	return fmt.Sprintf("retryable: http %d", e.Status)
}
func (e *RetryableError) Unwrap() error { return e.Err }

// banned are sites whose terms forbid crawling. Matching is by domain suffix.
var banned = []string{"linkedin.com", "naukri.com", "indeed.com", "glassdoor.com", "glassdoor.co.in", "wellfound.com", "angel.co"}

// Banned reports whether host is one of the never-crawl sites.
func Banned(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, b := range banned {
		if host == b || strings.HasSuffix(host, "."+b) {
			return true
		}
	}
	return false
}

type Result struct {
	Status      int
	Body        []byte
	FinalURL    string // after redirects
	ContentType string
}

type Client struct {
	HTTP      *http.Client
	UserAgent string
	Limiter   Limiter
	MaxBody   int64
	robots    *robotsCache
}

func NewClient(userAgent string, limiter Limiter, timeout time.Duration, maxBody int64) *Client {
	c := &Client{UserAgent: userAgent, Limiter: limiter, MaxBody: maxBody}
	c.HTTP = &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("too many redirects")
			}
			if Banned(req.URL.Hostname()) {
				return ErrBanned
			}
			return nil
		},
	}
	c.robots = newRobotsCache(c)
	return c
}

// Fetch downloads one URL, obeying the ban list, robots.txt and the rate limit.
// A 4xx is returned as a normal Result; 429, 5xx and network errors come back
// as *RetryableError after the domain has been slowed down.
func (c *Client) Fetch(ctx context.Context, rawURL string) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("bad url %q", rawURL)
	}
	if Banned(u.Hostname()) {
		return nil, ErrBanned
	}
	allowed, err := c.robots.Allowed(ctx, u)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrRobots
	}
	return c.do(ctx, http.MethodGet, u, nil)
}

// PostJSON sends one JSON POST under the same rules as Fetch. Some job boards
// (Workday) serve their public listing only this way.
func (c *Client) PostJSON(ctx context.Context, rawURL string, body []byte) (*Result, error) {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return nil, fmt.Errorf("bad url %q", rawURL)
	}
	if Banned(u.Hostname()) {
		return nil, ErrBanned
	}
	allowed, err := c.robots.Allowed(ctx, u)
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, ErrRobots
	}
	return c.do(ctx, http.MethodPost, u, body)
}

// Allow runs every politeness check for a URL without downloading it: the
// never-crawl list, robots.txt, and the domain's rate-limit slot. The headless
// renderer calls it before opening a page.
func (c *Client) Allow(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return fmt.Errorf("bad url %q", rawURL)
	}
	if Banned(u.Hostname()) {
		return ErrBanned
	}
	allowed, err := c.robots.Allowed(ctx, u)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrRobots
	}
	return c.Limiter.Wait(ctx, strings.ToLower(u.Hostname()))
}

// get is one rate-limited GET with no robots check (used for robots.txt itself).
func (c *Client) get(ctx context.Context, u *url.URL) (*Result, error) {
	return c.do(ctx, http.MethodGet, u, nil)
}

// do is one rate-limited request with no robots check.
func (c *Client) do(ctx context.Context, method string, u *url.URL, payload []byte) (*Result, error) {
	domain := strings.ToLower(u.Hostname())
	if err := c.Limiter.Wait(ctx, domain); err != nil {
		return nil, err
	}
	var reqBody io.Reader
	if payload != nil {
		reqBody = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.5")

	res, err := c.HTTP.Do(req)
	if err != nil {
		if errors.Is(err, ErrBanned) {
			return nil, ErrBanned
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &RetryableError{Err: err, After: 30 * time.Second}
	}
	defer res.Body.Close()

	if res.StatusCode == http.StatusTooManyRequests || res.StatusCode >= 500 {
		after := retryAfter(res.Header.Get("Retry-After"))
		// Slow the whole domain down for every worker, not just this one.
		c.Limiter.Penalize(context.WithoutCancel(ctx), domain, after)
		io.Copy(io.Discard, io.LimitReader(res.Body, 1<<16))
		return nil, &RetryableError{Status: res.StatusCode, After: after}
	}

	body, err := io.ReadAll(io.LimitReader(res.Body, c.MaxBody+1))
	if err != nil {
		return nil, &RetryableError{Err: err, After: 30 * time.Second}
	}
	if int64(len(body)) > c.MaxBody {
		return nil, fmt.Errorf("%w (%d MB)", ErrTooLarge, c.MaxBody>>20)
	}
	return &Result{
		Status:      res.StatusCode,
		Body:        body,
		FinalURL:    cleanURL(res.Request.URL),
		ContentType: res.Header.Get("Content-Type"),
	}, nil
}

func retryAfter(h string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(h)); err == nil && secs > 0 {
		return min(time.Duration(secs)*time.Second, 10*time.Minute)
	}
	return 30 * time.Second
}

// cleanURL drops a default port ("example.com:443"), which some sites put in
// their redirects, so the same page is not stored under two addresses.
func cleanURL(u *url.URL) string {
	c := *u
	if (c.Scheme == "https" && c.Port() == "443") || (c.Scheme == "http" && c.Port() == "80") {
		c.Host = c.Hostname()
	}
	return c.String()
}
