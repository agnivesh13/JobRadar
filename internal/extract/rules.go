package extract

import (
	"net/url"
	"regexp"
	"strings"

	"jobradar/internal/model"
)

// Limits of a whole-site crawl (PLAN.md, "Company deep search", step 4).
const (
	MaxDepth = 4
	MaxPages = 500
)

var (
	// Paths that never hold jobs.
	skipPath = regexp.MustCompile(`(?i)/(blog|blogs|news|press|media|docs|documentation|help|support|legal|privacy|terms|cookie|cookies|pricing|login|signin|signup|register|cart|checkout|events|webinars?|podcasts?|case-stud|customers|resources|changelog|community|forum|tag|category|author|wp-content|wp-json|cdn-cgi|static|assets)(/|$)`)
	skipExt  = regexp.MustCompile(`(?i)\.(pdf|png|jpe?g|gif|svg|webp|ico|css|js|mjs|json|xml|zip|gz|mp4|mp3|woff2?|ttf|docx?|xlsx?|pptx?)$`)
	// Signs that a link leads towards jobs.
	jobPath = regexp.MustCompile(`(?i)(career|job|opening|position|vacanc|join|hiring|work-with|work-at|workwith|opportunit|recruit|life-at|internship|roles?(/|$))`)
	jobText = regexp.MustCompile(`(?i)\b(careers?|jobs?|openings?|open positions?|open roles?|vacanc\w+|join (us|our team|the team)|we.?re hiring|work with us|view (all )?(jobs|roles|openings|positions)|see (all )?(jobs|roles|openings|positions)|apply)\b`)
	// A path that looks like one posting: a job-ish section followed by a slug or id.
	detailPath = regexp.MustCompile(`(?i)/(careers?|jobs?|openings?|positions?|vacanc\w*|roles?|opportunit\w*|join-us|join)/(?:[\w%.-]+/)*[\w%.-]*(?:\d{3,}|[a-z]+-[a-z-]+)[\w%.-]*/?$`)
	// Body phrases a real posting nearly always has.
	postingBody = regexp.MustCompile(`(?i)\b(responsibilit\w+|requirements?|qualifications?|what you.?ll do|what you will do|about the role|about this role|who you are|you will|we.?re looking for|we are looking for|must have|nice to have|job description|key skills|experience (in|with))\b`)
	applyWord   = regexp.MustCompile(`(?i)\bapply\b`)
	locationRe  = regexp.MustCompile(`(?i)\b(?:location|based in|work location|job location)\s*[:\-–]\s*([^\n|•]{2,60})`)
	titleSplit  = regexp.MustCompile(`\s+[|\-–—·•@]\s+|\s+at\s+`)
)

// SameSite reports whether link stays on the site being crawled (same host,
// ignoring a leading "www.").
func SameSite(link, site string) bool {
	a, err1 := url.Parse(link)
	b, err2 := url.Parse(site)
	if err1 != nil || err2 != nil {
		return false
	}
	return strings.TrimPrefix(strings.ToLower(a.Hostname()), "www.") == strings.TrimPrefix(strings.ToLower(b.Hostname()), "www.")
}

// Follow chooses which of a page's links a site crawl should fetch next.
// Only same-site links that look job-related are followed, which keeps a
// whole-site crawl to the careers section instead of the entire website.
func Follow(p Page, pageURL string, depth int) []string {
	if depth >= MaxDepth {
		return nil
	}
	var out []string
	for _, l := range p.Links {
		u, err := url.Parse(l.URL)
		if err != nil || !SameSite(l.URL, pageURL) || l.URL == pageURL {
			continue
		}
		if skipPath.MatchString(u.Path) || skipExt.MatchString(u.Path) {
			continue
		}
		if jobPath.MatchString(u.Path) || jobText.MatchString(l.Text) {
			out = append(out, l.URL)
		}
	}
	return out
}

// CareersLinks picks, from a home page, the links most likely to be the
// careers entry point, best first. Off-site links are allowed here: many
// companies link straight to careers.example.com or to a job board.
func CareersLinks(p Page) []string {
	var strong, weak []string
	for _, l := range p.Links {
		u, err := url.Parse(l.URL)
		if err != nil || skipExt.MatchString(u.Path) {
			continue
		}
		txt := strings.ToLower(l.Text)
		switch {
		case strings.Contains(txt, "career") || strings.Contains(txt, "jobs") || strings.Contains(txt, "we're hiring") || strings.Contains(txt, "join us") || strings.Contains(txt, "open roles") || strings.Contains(txt, "open positions"):
			strong = append(strong, l.URL)
		case regexp.MustCompile(`(?i)(career|jobs|join-us|work-with-us|hiring)`).MatchString(u.Host + u.Path):
			weak = append(weak, l.URL)
		}
	}
	return append(strong, weak...)
}

// HeuristicJob recognises a posting on a page with no structured data: the
// URL looks like one posting, there is a heading, and the body reads like a
// job description with a way to apply.
func HeuristicJob(p Page, pageURL string) (model.RawJob, bool) {
	u, err := url.Parse(pageURL)
	if err != nil || !detailPath.MatchString(u.Path) {
		return model.RawJob{}, false
	}
	title := p.H1
	if title == "" && p.Title != "" {
		title = titleSplit.Split(p.Title, 2)[0]
	}
	title = strings.TrimSpace(title)
	if len(title) < 4 || len(title) > 140 || len(p.Text) < 400 {
		return model.RawJob{}, false
	}
	if len(postingBody.FindAllStringIndex(p.Text, 3)) < 2 || !applyWord.MatchString(p.Text) {
		return model.RawJob{}, false
	}
	j := model.RawJob{URL: pageURL, Title: title, Description: p.Text}
	if len(j.Description) > 30_000 {
		j.Description = strings.ToValidUTF8(j.Description[:30_000], "")
	}
	if m := locationRe.FindStringSubmatch(p.Text); m != nil {
		j.Location = strings.TrimSpace(m[1])
	}
	j.Remote = strings.Contains(strings.ToLower(j.Location), "remote")
	return j, true
}

// LooksEmpty reports whether a page probably needs JavaScript to show its
// content: almost no visible text, or a known single-page-app shell.
func LooksEmpty(p Page, body []byte) bool {
	if len(p.Jobs) > 0 || p.Board != "" {
		return false
	}
	if len(p.Text) < 300 {
		return true
	}
	b := strings.ToLower(string(body[:min(len(body), 200_000)]))
	shell := strings.Contains(b, `id="root"`) || strings.Contains(b, `id="app"`) || strings.Contains(b, `id="__next"`) || strings.Contains(b, "ng-version") || strings.Contains(b, "data-reactroot")
	return shell && len(p.Text) < 1500
}

// hostedRe matches a company's careers site on a hiring product JobRadar has
// no feed parser for. These are found in the data (Keka, Kula, Workday and
// Dover all appeared in the first discovery run); the crawl continues on
// that site, through the renderer where it is a JavaScript app.
var hostedRe = regexp.MustCompile(`(?i)https?://[a-z0-9.-]*\b(?:keka\.com|kula\.ai|dover\.com|myworkdayjobs\.com|recruitee\.com|zohorecruit\.(?:com|in)|darwinbox\.(?:in|com)|freshteam\.com|workable\.com|smartrecruiters\.com|teamtailor\.com|bamboohr\.com|breezy\.hr|personio\.(?:com|de)|pinpointhq\.com|turbohire\.co)/[^\s"'<>\\)]*`)

// HostedCareers returns the first link in a page's source that leads to the
// company's careers site on a hosted hiring product, or "".
func HostedCareers(body []byte) string {
	for _, m := range hostedRe.FindAll(body, 20) {
		u := strings.TrimRight(string(m), ".,;")
		low := strings.ToLower(u)
		// Skip the product's own marketing site, assets and API hosts.
		if strings.Contains(low, "//www.") || strings.Contains(low, "//cdn.") || strings.Contains(low, "//api.") || skipExt.MatchString(strings.SplitN(low, "?", 2)[0]) {
			continue
		}
		return u
	}
	return ""
}
