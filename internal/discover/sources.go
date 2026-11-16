// Package discover finds companies that are not in the list yet: funding
// news, the Hacker News hiring thread, the Y Combinator directory and search
// queries. Each source only produces candidates; resolving a candidate into
// something crawlable is the resolver's job.
//
// Source URLs and response shapes were checked against live responses on
// 17 Nov 2026 (see learning/phase-6).
package discover

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"jobradar/internal/boards"
	"jobradar/internal/crawl"
)

// Candidate is a company a source mentioned.
type Candidate struct {
	Name         string
	Website      string
	BoardType    string
	BoardToken   string
	Source       string
	Detail       string
	FundingStage string
}

type Fetcher interface {
	Fetch(ctx context.Context, url string) (*crawl.Result, error)
}

// FundingFeeds are RSS feeds of startup funding news.
var FundingFeeds = []string{
	"https://inc42.com/feed/",
	"https://yourstory.com/feed",
	"https://techcrunch.com/category/venture/feed/",
	"https://entrackr.com/rss",
}

// ---- funding news ---------------------------------------------------------

var (
	// "<Company> raises/bags/secures ...": the company is whatever precedes the verb.
	raiseRe = regexp.MustCompile(`(?i)^(.{2,70}?)\s+(?:raises|raised|bags|bagged|secures|secured|closes|lands|nets|snags|gets|mops up|picks up|scores|pockets|garners)\s+(?:(?:over|nearly|about|around|another|fresh|its|a|an|\$|₹|rs\.?|inr|usd)\s*)*[\d$₹]`)
	// Words in front of the name that describe it: "Fintech startup Acme", "Bengaluru-based Acme".
	leadIn  = regexp.MustCompile(`(?i)^(?:.*\b(?:startup|start-up|firm|platform|company|unicorn|maker|player|brand|provider|lender|backed|based|founded|exclusive:?|funding:?|\w+tech)\s+)+`)
	stageRe = regexp.MustCompile(`(?i)\b(pre-?seed|seed|pre-?series [a-h]|series [a-h]\d?|bridge|angel)\b`)
	// A "name" that is really an investor or a sentence.
	notName = regexp.MustCompile(`(?i)\b(ventures?|capital|partners|fund|vc|investors?|backer|govt|government|ministry|sebi|rbi|court|india|how|why|what|who|these|this|after|as)\b`)
)

// NameFromHeadline pulls the company out of a funding headline such as
// "Dreamteam Bags $53 Mn To Disrupt The CRM Space With AI".
func NameFromHeadline(title string) (name, stage string, ok bool) {
	title = strings.TrimSpace(html.UnescapeString(title))
	m := raiseRe.FindStringSubmatch(title)
	// "X closes $4 Bn global fund" is an investor raising a fund, not a startup raising a round.
	if m == nil || regexp.MustCompile(`(?i)\bfund\b|\baum\b`).MatchString(title) {
		return "", "", false
	}
	name = strings.TrimSpace(leadIn.ReplaceAllString(m[1], ""))
	name = strings.Trim(name, " ,:'\"’‘-")
	words := strings.Fields(name)
	if name == "" || len(words) > 4 || notName.MatchString(name) || !startsUpper(name) {
		return "", "", false
	}
	if s := stageRe.FindString(title); s != "" {
		stage = strings.ToLower(s)
	}
	return name, stage, true
}

func startsUpper(s string) bool {
	r := []rune(s)[0]
	return (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

type rss struct {
	Items []struct {
		Title string `xml:"title"`
		Link  string `xml:"link"`
	} `xml:"channel>item"`
}

// Funding reads one RSS feed and returns the companies that just raised money.
func Funding(ctx context.Context, f Fetcher, feedURL string) ([]Candidate, error) {
	res, err := f.Fetch(ctx, feedURL)
	if err != nil {
		return nil, err
	}
	if res.Status != 200 {
		return nil, fmt.Errorf("feed %s: http %d", feedURL, res.Status)
	}
	return ParseFunding(res.Body)
}

func ParseFunding(body []byte) ([]Candidate, error) {
	var feed rss
	if err := xml.Unmarshal(body, &feed); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, it := range feed.Items {
		if name, stage, ok := NameFromHeadline(it.Title); ok {
			out = append(out, Candidate{Name: name, Source: "rss", Detail: strings.TrimSpace(html.UnescapeString(it.Title)), FundingStage: stage})
		}
	}
	return out, nil
}

// ---- Hacker News "Who is hiring" --------------------------------------------

const hnAPI = "https://hn.algolia.com/api/v1/"

var (
	hrefRe  = regexp.MustCompile(`href="([^"]+)"`)
	tagsRe  = regexp.MustCompile(`<[^>]+>`)
	indiaRe = regexp.MustCompile(`(?i)\b(india|bangalore|bengaluru|hyderabad|mumbai|pune|delhi|gurgaon|gurugram|chennai|noida|remote \(?(?:global|worldwide|anywhere|apac|asia)|worldwide|anywhere)\b`)
)

// HackerNews reads the latest monthly "Who is hiring?" thread. Top-level
// comments conventionally start "Company | Role | Location | ...". Only
// postings that mention India or worldwide remote are kept.
func HackerNews(ctx context.Context, f Fetcher) ([]Candidate, error) {
	res, err := f.Fetch(ctx, hnAPI+"search_by_date?tags=story,author_whoishiring&hitsPerPage=10")
	if err != nil {
		return nil, err
	}
	var stories struct {
		Hits []struct {
			ObjectID string `json:"objectID"`
			Title    string `json:"title"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(res.Body, &stories); err != nil {
		return nil, err
	}
	storyID := ""
	for _, h := range stories.Hits {
		if strings.Contains(strings.ToLower(h.Title), "who is hiring") {
			storyID = h.ObjectID
			break
		}
	}
	if storyID == "" {
		return nil, fmt.Errorf("no hiring thread found")
	}
	var out []Candidate
	for page := 0; page < 3; page++ {
		res, err := f.Fetch(ctx, fmt.Sprintf("%ssearch?tags=comment,story_%s&hitsPerPage=500&page=%d", hnAPI, storyID, page))
		if err != nil {
			return out, err
		}
		cands, more, err := ParseHN(res.Body, storyID)
		if err != nil {
			return out, err
		}
		out = append(out, cands...)
		if !more {
			break
		}
	}
	return out, nil
}

// ParseHN reads one page of the thread's comments.
func ParseHN(body []byte, storyID string) (cands []Candidate, more bool, err error) {
	var page struct {
		Hits []struct {
			Text     string `json:"comment_text"`
			ParentID int64  `json:"parent_id"`
		} `json:"hits"`
		Page    int `json:"page"`
		NbPages int `json:"nbPages"`
	}
	if err := json.Unmarshal(body, &page); err != nil {
		return nil, false, err
	}
	for _, h := range page.Hits {
		if fmt.Sprint(h.ParentID) != storyID { // replies are not postings
			continue
		}
		text := html.UnescapeString(h.Text)
		if !indiaRe.MatchString(tagsRe.ReplaceAllString(text, " ")) {
			continue
		}
		first := strings.TrimSpace(tagsRe.ReplaceAllString(strings.SplitN(text, "<p>", 2)[0], " "))
		name := strings.TrimSpace(strings.SplitN(first, "|", 2)[0])
		name = strings.TrimSpace(regexp.MustCompile(`\s*\(.*?\)\s*`).ReplaceAllString(name, " "))
		// Comments that ignore the "Company | Role | ..." convention start with
		// things like "Location: REMOTE"; those are not company names.
		if !strings.Contains(first, "|") || len(name) < 2 || len(name) > 50 || len(strings.Fields(name)) > 5 ||
			strings.ContainsAny(name, ":/@") || regexp.MustCompile(`(?i)^(remote|hiring|location|seeking|full[- ]?time|onsite|hybrid)\b`).MatchString(name) {
			continue
		}
		c := Candidate{Name: name, Source: "hn", Detail: clip(first, 200)}
		for _, m := range hrefRe.FindAllStringSubmatch(text, -1) {
			link := html.UnescapeString(m[1])
			if b, tok, ok := boards.Detect(link); ok {
				c.BoardType, c.BoardToken = b, tok
				break
			}
			if u, err := url.Parse(link); err == nil && u.Hostname() != "" && c.Website == "" && !crawl.Banned(u.Hostname()) &&
				!regexp.MustCompile(`(?i)(ycombinator|github|google|youtube|twitter|x\.com|notion|docs\.|forms\.|calendly|medium)`).MatchString(u.Hostname()) {
				c.Website = "https://" + u.Hostname()
			}
		}
		cands = append(cands, c)
	}
	return cands, page.Page+1 < page.NbPages, nil
}

// ---- Y Combinator directory -------------------------------------------------

// YCHiring is a public JSON export of the YC directory, limited to companies
// marked as hiring.
const YCHiring = "https://yc-oss.github.io/api/companies/hiring.json"

// YCombinator returns YC companies that are hiring and have a presence in India.
func YCombinator(ctx context.Context, f Fetcher) ([]Candidate, error) {
	res, err := f.Fetch(ctx, YCHiring)
	if err != nil {
		return nil, err
	}
	if res.Status != 200 {
		return nil, fmt.Errorf("yc directory: http %d", res.Status)
	}
	return ParseYC(res.Body)
}

func ParseYC(body []byte) ([]Candidate, error) {
	var list []struct {
		Name      string   `json:"name"`
		Website   string   `json:"website"`
		Locations string   `json:"all_locations"`
		Regions   []string `json:"regions"`
		Batch     string   `json:"batch"`
		Stage     string   `json:"stage"`
		Status    string   `json:"status"`
		IsHiring  bool     `json:"isHiring"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, c := range list {
		where := c.Locations + " " + strings.Join(c.Regions, " ")
		if !c.IsHiring || c.Website == "" || strings.EqualFold(c.Status, "Inactive") {
			continue
		}
		// "Remote" is listed for most of the directory, so only India counts.
		if !regexp.MustCompile(`(?i)\bindia\b`).MatchString(where) {
			continue
		}
		out = append(out, Candidate{Name: strings.TrimSpace(c.Name), Website: c.Website, Source: "yc",
			Detail: strings.TrimSpace("YC " + c.Batch + " · " + c.Locations), FundingStage: strings.ToLower(c.Stage)})
	}
	return out, nil
}

// ---- search queries -----------------------------------------------------------

// FromSearchResults turns search-result URLs into candidates: a board link
// names its company directly; any other careers-looking URL gives a website.
func FromSearchResults(urls []string, query string) []Candidate {
	var out []Candidate
	seen := map[string]bool{}
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" || crawl.Banned(u.Hostname()) {
			continue
		}
		if b, tok, ok := boards.Detect(raw); ok {
			if !seen[b+tok] {
				seen[b+tok] = true
				out = append(out, Candidate{Name: tok, BoardType: b, BoardToken: tok, Source: "search", Detail: query})
			}
			continue
		}
		host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
		if !regexp.MustCompile(`(?i)(career|jobs|join|hiring)`).MatchString(u.Host+u.Path) || seen[host] {
			continue
		}
		seen[host] = true
		label := strings.SplitN(strings.TrimPrefix(strings.TrimPrefix(host, "careers."), "jobs."), ".", 2)[0]
		out = append(out, Candidate{Name: label, Website: "https://" + host, Source: "search", Detail: query})
	}
	return out
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "")
}
