// Package extract turns an arbitrary career-site page into job records and
// links to follow: structured data first, HTML patterns second.
package extract

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"jobradar/internal/boards"
	"jobradar/internal/model"
)

type Link struct {
	URL  string
	Text string
}

// Page is everything read from one HTML document.
type Page struct {
	Title    string
	H1       string
	Text     string // visible text, one block per line
	Links    []Link // absolute, de-duplicated, fragment removed
	Jobs     []model.RawJob
	Board    string // board software the page loads or links to ("" if none)
	BoardTok string
}

// Parse reads an HTML page fetched from pageURL.
func Parse(body []byte, pageURL string) Page {
	var p Page
	base, err := url.Parse(pageURL)
	if err != nil {
		return p
	}
	// A board reference anywhere in the source (link, iframe, script, inline
	// config) means the real job list lives on that board.
	if b, tok, ok := boards.Detect(string(body)); ok {
		p.Board, p.BoardTok = b, tok
	}
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return p
	}
	var text strings.Builder
	seen := map[string]bool{}
	var walk func(n *html.Node, skip bool)
	walk = func(n *html.Node, skip bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "script":
				if attr(n, "type") == "application/ld+json" && n.FirstChild != nil {
					p.Jobs = append(p.Jobs, jobPostings([]byte(n.FirstChild.Data), base)...)
				}
				return
			case "style", "noscript", "svg", "template", "head":
				if n.Data == "head" {
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						walk(c, true)
					}
				}
				return
			case "title":
				if p.Title == "" {
					p.Title = clean(innerText(n))
				}
				return
			case "base":
				if h, err := base.Parse(attr(n, "href")); err == nil && attr(n, "href") != "" {
					base = h
				}
			case "h1":
				if p.H1 == "" {
					p.H1 = clean(innerText(n))
				}
				if !skip {
					text.WriteString("\n")
				}
			case "a", "iframe":
				href := attr(n, "href")
				if n.Data == "iframe" {
					href = attr(n, "src")
				}
				if u := resolve(base, href); u != "" && !seen[u] {
					seen[u] = true
					p.Links = append(p.Links, Link{URL: u, Text: clean(innerText(n))})
				}
			case "nav", "footer", "header":
				// Links are still collected, but boilerplate text is left out.
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					walk(c, true)
				}
				return
			case "p", "div", "li", "br", "tr", "h2", "h3", "h4", "h5", "section", "article":
				if !skip {
					text.WriteString("\n")
				}
			}
			if n.Data == "li" && !skip {
				text.WriteString("- ")
			}
		}
		if n.Type == html.TextNode && !skip {
			text.WriteString(n.Data)
			text.WriteString(" ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, skip)
		}
	}
	walk(doc, false)
	p.Text = tidy(text.String())
	return p
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return strings.TrimSpace(a.Val)
		}
	}
	return ""
}

func innerText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data + " ")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

var (
	wsRe    = regexp.MustCompile(`[ \t\r\x{00a0}]+`)
	blankRe = regexp.MustCompile(`\n[\s]*\n+`)
)

func clean(s string) string {
	return strings.TrimSpace(wsRe.ReplaceAllString(strings.ReplaceAll(s, "\n", " "), " "))
}

func tidy(s string) string {
	s = wsRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(strings.ReplaceAll(s, " \n", "\n"), "\n ", "\n")
	return strings.TrimSpace(blankRe.ReplaceAllString(s, "\n"))
}

// resolve makes href absolute and drops fragments, non-http schemes and
// tracking parameters.
func resolve(base *url.URL, href string) string {
	if href == "" || strings.HasPrefix(href, "#") {
		return ""
	}
	u, err := base.Parse(href)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.Fragment = ""
	if u.Path == "" {
		u.Path = "/" // "https://a.com" and "https://a.com/" are one page
	}
	q := u.Query()
	for k := range q {
		if strings.HasPrefix(k, "utm_") || k == "gclid" || k == "fbclid" || k == "ref" {
			q.Del(k)
		}
	}
	u.RawQuery = q.Encode()
	u.Host = strings.ToLower(u.Host)
	return u.String()
}

// ---- JobPosting structured data --------------------------------------------

// jobPostings finds every schema.org JobPosting in one JSON-LD block,
// wherever it is nested (top level, an array, or an @graph).
func jobPostings(data []byte, base *url.URL) []model.RawJob {
	var v any
	if err := json.Unmarshal(bytes.TrimSpace(data), &v); err != nil {
		return nil
	}
	var out []model.RawJob
	var walk func(any, int)
	walk = func(v any, depth int) {
		if depth > 6 {
			return
		}
		switch t := v.(type) {
		case []any:
			for _, x := range t {
				walk(x, depth+1)
			}
		case map[string]any:
			if isType(t["@type"], "JobPosting") {
				if j, ok := toJob(t, base); ok {
					out = append(out, j)
				}
				return
			}
			for _, x := range t {
				walk(x, depth+1)
			}
		}
	}
	walk(v, 0)
	return out
}

func isType(v any, want string) bool {
	switch t := v.(type) {
	case string:
		return strings.EqualFold(t, want)
	case []any:
		for _, x := range t {
			if isType(x, want) {
				return true
			}
		}
	}
	return false
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case []any:
		parts := []string{}
		for _, x := range t {
			if s := str(x); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case map[string]any:
		return str(t["name"])
	}
	return ""
}

func num(v any) float64 {
	switch t := v.(type) {
	case float64:
		return t
	case string:
		var f float64
		json.Unmarshal([]byte(strings.ReplaceAll(strings.TrimSpace(t), ",", "")), &f)
		return f
	}
	return 0
}

func toJob(m map[string]any, base *url.URL) (model.RawJob, bool) {
	j := model.RawJob{Title: str(m["title"]), EmploymentType: str(m["employmentType"])}
	if j.Title == "" {
		return j, false
	}
	j.URL = resolve(base, str(m["url"]))
	if j.URL == "" {
		j.URL = base.String()
	}
	j.Description = boards.HTMLToText(str(m["description"]))
	if len(j.Description) > 30_000 {
		j.Description = strings.ToValidUTF8(j.Description[:30_000], "")
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, str(m["datePosted"])); err == nil {
			j.PostedAt = &t
			break
		}
	}
	var places []string
	addPlace := func(v any) {
		loc, _ := v.(map[string]any)
		if loc == nil {
			if s := str(v); s != "" {
				places = append(places, s)
			}
			return
		}
		addr, _ := loc["address"].(map[string]any)
		if addr == nil {
			if s := str(loc["address"]); s != "" {
				places = append(places, s)
			} else if s := str(loc["name"]); s != "" {
				places = append(places, s)
			}
			return
		}
		var parts []string
		for _, k := range []string{"addressLocality", "addressRegion", "addressCountry"} {
			if s := str(addr[k]); s != "" {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			places = append(places, strings.Join(parts, ", "))
		}
	}
	switch t := m["jobLocation"].(type) {
	case []any:
		for _, x := range t {
			addPlace(x)
		}
	default:
		addPlace(t)
	}
	if strings.Contains(strings.ToUpper(str(m["jobLocationType"])), "TELECOMMUTE") {
		j.Remote = true
		where := str(m["applicantLocationRequirements"])
		places = append(places, strings.TrimSpace("Remote "+where))
	}
	j.Location = strings.Join(places, " / ")

	if sal, _ := m["baseSalary"].(map[string]any); sal != nil {
		pay := model.Pay{Currency: strings.ToUpper(str(sal["currency"])), Period: "year"}
		unit := ""
		switch val := sal["value"].(type) {
		case map[string]any:
			pay.Min, pay.Max = num(val["minValue"]), num(val["maxValue"])
			if pay.Min == 0 && pay.Max == 0 {
				pay.Min = num(val["value"])
				pay.Max = pay.Min
			}
			unit = strings.ToUpper(str(val["unitText"]))
		default:
			pay.Min = num(val)
			pay.Max = pay.Min
		}
		switch {
		case strings.Contains(unit, "MONTH"):
			pay.Period = "month"
		case strings.Contains(unit, "HOUR"):
			pay.Period = "hour"
		case strings.Contains(unit, "WEEK"):
			pay.Period = "week"
		}
		if pay.Currency != "" && (pay.Min > 0 || pay.Max > 0) {
			j.Pay = &pay
		}
	}
	return j, true
}

// ---- sitemap ------------------------------------------------------------

// SitemapURLs returns the <loc> entries of a sitemap or sitemap index.
func SitemapURLs(body []byte) []string {
	var doc struct {
		URLs []struct {
			Loc string `xml:"loc"`
		} `xml:"url"`
		Sitemaps []struct {
			Loc string `xml:"loc"`
		} `xml:"sitemap"`
	}
	if err := xml.Unmarshal(body, &doc); err != nil {
		return nil
	}
	var out []string
	for _, u := range append(doc.URLs, doc.Sitemaps...) {
		if loc := strings.TrimSpace(u.Loc); loc != "" {
			out = append(out, loc)
		}
	}
	return out
}
