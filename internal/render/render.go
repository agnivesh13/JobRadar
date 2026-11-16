// Package render loads JavaScript-heavy pages in headless Chrome and returns
// the HTML after scripts have run.
package render

import (
	"context"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
)

// Renderer owns one headless browser; each page gets its own tab.
type Renderer struct {
	userAgent string

	mu      sync.Mutex
	browser context.Context
	stop    []context.CancelFunc
}

func New(userAgent string) *Renderer { return &Renderer{userAgent: userAgent} }

// start launches the browser on first use, and again if it has died.
func (r *Renderer) start() (context.Context, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.browser != nil && r.browser.Err() == nil {
		return r.browser, nil
	}
	r.closeLocked()
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoSandbox, // required inside an unprivileged container
		chromedp.DisableGPU,
		chromedp.Flag("disable-dev-shm-usage", true), // /dev/shm is tiny in Docker
		chromedp.Flag("blink-settings", "imagesEnabled=false"),
		chromedp.UserAgent(r.userAgent),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(), opts...)
	browser, cancelBrowser := chromedp.NewContext(allocCtx)
	if err := chromedp.Do(browser); err != nil { // forces the process to start
		cancelBrowser()
		cancelAlloc()
		return nil, err
	}
	r.browser, r.stop = browser, []context.CancelFunc{cancelBrowser, cancelAlloc}
	return browser, nil
}

func (r *Renderer) closeLocked() {
	for _, c := range r.stop {
		c()
	}
	r.browser, r.stop = nil, nil
}

func (r *Renderer) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closeLocked()
}

// HTML opens url in a new tab, waits for the page to settle and returns its
// rendered HTML and final address.
func (r *Renderer) HTML(ctx context.Context, url string) (html, finalURL string, err error) {
	browser, err := r.start()
	if err != nil {
		return "", "", err
	}
	tab, cancelTab := chromedp.NewContext(browser)
	defer cancelTab()
	tab, cancelTimeout := context.WithTimeout(tab, 45*time.Second)
	defer cancelTimeout()
	// Stop early if the caller gives up.
	go func() {
		select {
		case <-ctx.Done():
			cancelTab()
		case <-tab.Done():
		}
	}()
	// chromedp v0.23 API (checked against the module source): Do runs steps
	// that return nothing, Run runs one action and returns its value.
	if err = chromedp.Do(tab,
		chromedp.Navigate(url),
		chromedp.WaitReady(chromedp.CSS("body")),
	); err != nil {
		return "", "", err
	}
	// Wait for the app to fetch and draw its content: poll the visible text
	// until it has stopped growing (or 15 seconds have passed). A fixed pause
	// returned empty shells for slower apps.
	last, stable := -1, 0
	for i := 0; i < 10 && stable < 2; i++ {
		if err = chromedp.Do(tab, chromedp.Sleep(1500*time.Millisecond)); err != nil {
			return "", "", err
		}
		n, err := chromedp.Run(tab, chromedp.Evaluate[int](`document.body ? document.body.innerText.length : 0`))
		if err != nil {
			break
		}
		if n == last && n > 200 {
			stable++
		} else {
			stable = 0
		}
		last = n
	}
	if finalURL, err = chromedp.Run(tab, chromedp.Location()); err != nil {
		return "", "", err
	}
	html, err = chromedp.Run(tab, chromedp.OuterHTML(chromedp.CSS("html")))
	return html, finalURL, err
}
