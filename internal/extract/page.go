// Package extract turns an arbitrary career-site page into job records and
// links to follow: structured data first, HTML patterns second.
package extract

import (
	"bytes"
	"net/url"
	"regexp"
	"strings"

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
					p.Jobs = append(p.Jobs, boards.JobPostings([]byte(n.FirstChild.Data), base)...)
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
func resolve(base *url.URL, href string) string { return boards.CleanURL(base, href) }

// SitemapURLs returns the <loc> entries of a sitemap or sitemap index.
func SitemapURLs(body []byte) []string { return boards.SitemapURLs(body) }
