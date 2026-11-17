// Package resolve turns a company name into something crawlable:
// name -> domain -> careers page -> job board. Deep search and discovery
// both go through it.
package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"jobradar/internal/boards"
	"jobradar/internal/crawl"
	"jobradar/internal/extract"
	"jobradar/internal/model"
)

// Fetcher is the polite HTTP client (robots.txt, rate limit, ban list).
type Fetcher interface {
	Fetch(ctx context.Context, url string) (*crawl.Result, error)
}

type Resolver struct {
	Fetch      Fetcher
	SearxngURL string // optional self-hosted search; "" = guess domains from the name
	// Step reports progress for the live panel (may be nil).
	Step func(outcome, detail, url string)
}

// Candidate is a website that may be the company's.
type Candidate struct {
	Domain string `json:"domain"`
	Title  string `json:"title"`
	Score  int    `json:"score"`
}

// Result is what resolving found. Exactly one of these holds:
//   - Board != "": the company is on a job board (crawl its feed)
//   - CareersURL != "": crawl the site from that page
//   - len(Candidates) > 1 and Ambiguous: the user must pick a domain
type Result struct {
	Name       string
	Domain     string
	CareersURL string
	Board      string
	BoardToken string
	Candidates []Candidate
	Ambiguous  bool
	// More holds further boards of the same company (a Workday tenant can
	// run several career sites); each is saved as its own company.
	More []Result
}

func (r *Resolver) step(outcome, detail, u string) {
	if r.Step != nil {
		r.Step(outcome, detail, u)
	}
}

var (
	nonSlug = regexp.MustCompile(`[^a-z0-9]+`)
	suffix  = regexp.MustCompile(`(?i)\b(pvt|private|ltd|limited|inc|llc|llp|corp|corporation|technologies|technology|labs|software|solutions|systems|india|global|group|co)\b\.?`)
	// Hosts that are never a company's own site.
	notCompany = regexp.MustCompile(`(?i)(^|\.)(linkedin|naukri|indeed|glassdoor|wellfound|angel|facebook|twitter|x|instagram|youtube|wikipedia|crunchbase|github|medium|google|bing|ambitionbox|quora|reddit|tracxn|zaubacorp|justdial|apple|play\.google|foundit|shine|internshala|cutshort|instahyre|ycombinator|techcrunch|inc42|yourstory|entrackr)\.`)
)

// Slugs are the forms a name commonly takes as a domain label or board token.
func Slugs(name string) []string {
	lower := strings.ToLower(strings.TrimSpace(name))
	full := nonSlug.ReplaceAllString(lower, "")
	short := nonSlug.ReplaceAllString(strings.TrimSpace(suffix.ReplaceAllString(lower, " ")), "")
	hyphen := strings.Trim(nonSlug.ReplaceAllString(strings.TrimSpace(suffix.ReplaceAllString(lower, " ")), "-"), "-")
	var out []string
	for _, s := range []string{short, full, hyphen} {
		if len(s) >= 2 && !contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Resolve runs the whole chain for a name, a domain or a URL. If pick is
// non-empty it is the domain the user chose from an earlier ambiguous result.
func (r *Resolver) Resolve(ctx context.Context, input, pick string) (Result, error) {
	input = strings.TrimSpace(input)
	res := Result{Name: input}
	if input == "" {
		return res, fmt.Errorf("empty company name")
	}

	// The input may already be a board link or a website.
	if b, tok, ok := boards.Detect(input); ok {
		res.Board, res.BoardToken, res.Name = b, tok, boards.DisplayName(b, tok)
		r.step("ok", "that is a "+b+" board link", input)
		return res, nil
	}
	if tenant, hosts, ok := boards.WorkdayHosts(input); ok {
		return r.workdayTenant(ctx, tenant, tenant, hosts)
	}
	typedDomain := false
	switch {
	case pick != "":
		res.Domain = pick
	case strings.Contains(input, ".") && !strings.Contains(input, " "):
		u, err := url.Parse(input)
		if err != nil || u.Host == "" {
			u, err = url.Parse("https://" + input)
		}
		if err != nil || u.Hostname() == "" {
			return res, fmt.Errorf("cannot read %q as a website", input)
		}
		res.Domain = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		res.Name = strings.SplitN(res.Domain, ".", 2)[0]
		typedDomain = true
	}

	// 1. A board under the company's own name finishes the job in one request.
	if res.Domain == "" {
		if b, tok, n := r.boardBySlug(ctx, res.Name); b != "" {
			res.Board, res.BoardToken = b, tok
			r.step("ok", fmt.Sprintf("found on %s as %q (%d jobs listed)", b, tok, n), "")
			return res, nil
		}
		if wd, ok := r.workdayByName(ctx, res.Name); ok {
			return wd, nil
		}
		r.step("info", "not on a known job board under its own name; looking for its website", "")
	}

	// 2. Name -> domain.
	if res.Domain == "" {
		res.Candidates = r.candidates(ctx, res.Name)
		switch {
		case len(res.Candidates) == 0:
			return res, fmt.Errorf("could not find a website for %q; try typing its domain (example.com)", res.Name)
		case len(res.Candidates) > 1 && res.Candidates[0].Score-res.Candidates[1].Score < 2:
			res.Ambiguous = true
			r.step("info", "more than one website looks plausible; pick one", "")
			return res, nil
		}
		res.Domain = res.Candidates[0].Domain
	}
	r.step("ok", "domain resolved: "+res.Domain, "https://"+res.Domain)

	// 3. Domain -> careers page (and maybe a board).
	if err := r.careers(ctx, &res); err != nil {
		// A typed domain skipped step 1; its name may still be a Workday tenant.
		if typedDomain {
			if wd, ok := r.workdayByName(ctx, res.Name); ok {
				wd.Domain = res.Domain
				return wd, nil
			}
		}
		return res, err
	}
	return res, nil
}

// workdayByName looks for a Workday tenant named like the company. Workday
// has no search by name, but a tenant's host is "{tenant}.wdN.myworkdayjobs.com"
// and the tenant is usually the company's name in one word, so each slug is
// tried on the common data centres (an unknown tenant answers 422, like a
// wrong data centre; checked live on 17 Nov 2026).
func (r *Resolver) workdayByName(ctx context.Context, name string) (Result, bool) {
	for _, slug := range Slugs(name) {
		_, hosts, ok := boards.WorkdayHosts(slug + ".wd1.myworkdayjobs.com")
		if !ok {
			continue
		}
		if res, err := r.workdayTenant(ctx, name, slug, hosts); err == nil {
			return res, true
		}
	}
	return Result{}, false
}

// boardBySlug asks each board whether it hosts the company under a slug of its name.
func (r *Resolver) boardBySlug(ctx context.Context, name string) (board, token string, jobs int) {
	for _, slug := range Slugs(name) {
		for _, b := range []string{model.BoardGreenhouse, model.BoardLever, model.BoardAshby, model.BoardKeka, model.BoardSmartRecruiters} {
			token := slug
			feed, _ := boards.FeedURL(b, token)
			res, err := r.Fetch.Fetch(ctx, feed)
			if err != nil || res.Status != 200 {
				continue
			}
			var titles []string
			if boards.Paged(b) {
				// The first list page: country "" so that it lists postings.
				if list, err := boards.ParseListPage(b, res.Body, feed, ""); err == nil {
					for _, p := range list.Postings {
						titles = append(titles, p.Title)
					}
				}
			} else if list, err := boards.ParseFeed(b, feed, res.Body); err == nil {
				for _, j := range list {
					titles = append(titles, j.Title)
				}
			}
			if len(titles) > 0 && !onlyPlaceholders(titles) {
				return b, token, len(titles)
			}
		}
	}
	return "", "", 0
}

// placeholderRe matches the titles of test postings. A slug guessed from a
// company's name can belong to someone else's test account: "uber" on
// SmartRecruiters held one posting, "test-uat" (17 Nov 2026).
var placeholderRe = regexp.MustCompile(`(?i)\b(test|testing|uat|dummy|sample|do not apply)\b`)

// onlyPlaceholders reports whether every title is a test posting.
func onlyPlaceholders(titles []string) bool {
	for _, t := range titles {
		if !placeholderRe.MatchString(t) {
			return false
		}
	}
	return true
}

// candidates finds websites that may belong to the company, best first.
func (r *Resolver) candidates(ctx context.Context, name string) []Candidate {
	var hosts []string
	add := func(h string) {
		h = strings.TrimPrefix(strings.ToLower(h), "www.")
		if h != "" && !notCompany.MatchString(h+".") && !notCompany.MatchString("."+h) && !crawl.Banned(h) && !contains(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	if r.SearxngURL != "" {
		for _, u := range r.search(ctx, name+" careers") {
			if p, err := url.Parse(u); err == nil {
				if _, _, isBoard := boards.Detect(u); !isBoard {
					add(p.Hostname())
				}
			}
			if len(hosts) >= 5 {
				break
			}
		}
	}
	for _, slug := range Slugs(name) {
		for _, tld := range []string{".com", ".in", ".io", ".ai", ".co"} {
			if len(hosts) < 12 {
				add(slug + tld)
			}
		}
	}

	words := strings.Fields(strings.ToLower(nonSlug.ReplaceAllString(strings.ToLower(suffix.ReplaceAllString(name, " ")), " ")))
	var out []Candidate
	for i, h := range hosts {
		res, err := r.Fetch.Fetch(ctx, "https://"+h)
		if err != nil || res.Status != 200 {
			continue
		}
		page := extract.Parse(res.Body, res.FinalURL)
		title := strings.ToLower(page.Title)
		score := 0
		for _, w := range words {
			if len(w) > 1 && strings.Contains(title, w) {
				score += 3 // the site's title names the company
			}
		}
		if len(extract.CareersLinks(page)) > 0 {
			score += 2 // it has a careers link
		}
		if len(page.Text) < 200 || regexp.MustCompile(`(?i)domain (is )?for sale|buy this domain|parked`).MatchString(page.Text+title) {
			score -= 4 // parked or empty
		}
		score -= i / 4 // earlier guesses (search results, .com) win ties
		if final, err := url.Parse(res.FinalURL); err == nil {
			h = strings.TrimPrefix(strings.ToLower(final.Hostname()), "www.")
		}
		if score >= 3 && !hasDomain(out, h) {
			out = append(out, Candidate{Domain: h, Title: page.Title, Score: score})
			r.step("info", fmt.Sprintf("candidate website: %s (%s)", h, page.Title), "https://"+h)
		}
	}
	for i := range out { // small insertion sort, best first
		for j := i; j > 0 && out[j].Score > out[j-1].Score; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func hasDomain(list []Candidate, d string) bool {
	for _, c := range list {
		if c.Domain == d {
			return true
		}
	}
	return false
}

// careers finds the careers entry point of res.Domain: links on the home
// page, then the usual paths, then the sitemap.
func (r *Resolver) careers(ctx context.Context, res *Result) error {
	home := "https://" + res.Domain
	var tries []string
	if hp, err := r.Fetch.Fetch(ctx, home); err == nil && hp.Status == 200 {
		page := extract.Parse(hp.Body, hp.FinalURL)
		if page.Board != "" {
			res.Board, res.BoardToken = page.Board, page.BoardTok
			r.step("ok", fmt.Sprintf("board detected on the home page: %s (%s)", page.Board, page.BoardTok), home)
			return nil
		}
		tries = append(tries, extract.CareersLinks(page)...)
	}
	for _, p := range []string{"/careers", "/jobs", "/join-us", "/careers/", "/company/careers", "/about/careers", "/work-with-us"} {
		tries = append(tries, home+p)
	}
	seen := map[string]bool{}
	for i, u := range tries {
		if seen[u] || i > 12 {
			continue
		}
		seen[u] = true
		if b, tok, ok := boards.Detect(u); ok { // the careers link goes straight to a board
			res.Board, res.BoardToken, res.CareersURL = b, tok, u
			r.step("ok", fmt.Sprintf("careers link points at %s (%s)", b, tok), u)
			return nil
		}
		pg, err := r.Fetch.Fetch(ctx, u)
		if err != nil || pg.Status != 200 || !strings.Contains(strings.ToLower(pg.ContentType), "html") {
			continue
		}
		res.CareersURL = pg.FinalURL
		r.step("ok", "careers page found", pg.FinalURL)
		page := extract.Parse(pg.Body, pg.FinalURL)
		if page.Board == "" {
			if b, tok, ok := boards.Detect(pg.FinalURL); ok {
				page.Board, page.BoardTok = b, tok
			}
		}
		if page.Board != "" {
			res.Board, res.BoardToken = page.Board, page.BoardTok
			r.step("ok", fmt.Sprintf("board detected: %s (%s)", page.Board, page.BoardTok), pg.FinalURL)
		} else {
			r.step("info", "no known board; the site itself will be crawled", pg.FinalURL)
		}
		return nil
	}
	// Last resort: the sitemap.
	if sm, err := r.Fetch.Fetch(ctx, home+"/sitemap.xml"); err == nil && sm.Status == 200 {
		for _, u := range extract.SitemapURLs(sm.Body) {
			if regexp.MustCompile(`(?i)/(careers?|jobs?|join-us|openings)(/|$)`).MatchString(u) && extract.SameSite(u, home) {
				res.CareersURL = u
				r.step("ok", "careers page found in the sitemap", u)
				return nil
			}
		}
	}
	return fmt.Errorf("no careers page found on %s", res.Domain)
}

// search queries a self-hosted SearXNG instance and returns result URLs.
func (r *Resolver) search(ctx context.Context, query string) []string {
	return Search(ctx, r.SearxngURL, query)
}

// Search returns result URLs from SearXNG's JSON API, or nil if it is not
// configured or fails. It is our own container, so it is called directly.
func Search(ctx context.Context, base, query string) []string {
	if base == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/search?format=json&q="+url.QueryEscape(query), nil)
	if err != nil {
		return nil
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	var out struct {
		Results []struct {
			URL string `json:"url"`
		} `json:"results"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&out) != nil {
		return nil
	}
	urls := make([]string, 0, len(out.Results))
	for _, x := range out.Results {
		urls = append(urls, x.URL)
	}
	return urls
}

// workdayTenant finds the career sites of a Workday host given without a site
// name, by reading the host's robots.txt (see boards.WorkdaySites). name is
// what the company is called in the list.
func (r *Resolver) workdayTenant(ctx context.Context, name, tenant string, hosts []string) (Result, error) {
	res := Result{Name: name}
	for _, host := range hosts {
		robots := "https://" + host + "/robots.txt"
		page, err := r.Fetch.Fetch(ctx, robots)
		if err != nil || page.Status != 200 {
			continue
		}
		sites := boards.WorkdaySites(page.Body)
		if len(sites) == 0 {
			continue
		}
		r.step("ok", fmt.Sprintf("workday career sites of %s: %s", tenant, strings.Join(sites, ", ")), robots)
		res.Board, res.BoardToken = model.BoardWorkday, boards.WorkdayToken(host, tenant, sites[0])
		for _, site := range sites[1:] {
			res.More = append(res.More, Result{
				Name:  name + " (" + strings.ReplaceAll(site, "_", " ") + ")",
				Board: model.BoardWorkday, BoardToken: boards.WorkdayToken(host, tenant, site),
			})
		}
		return res, nil
	}
	return res, fmt.Errorf("no public Workday career site found for %q; paste the full link including the site name", tenant)
}
