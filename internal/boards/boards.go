// Package boards reads the public JSON feeds of job-board products. One
// parser per product covers every company hosted on it.
//
// Feed URLs and response shapes were checked against live responses on
// 16 Nov 2026; trimmed copies are in testdata/ and the tests parse them.
package boards

import (
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strings"

	"jobradar/internal/model"
)

// FeedURL returns the URL of the JSON feed listing every job of one company.
func FeedURL(boardType, token string) (string, error) {
	t := url.PathEscape(token)
	switch boardType {
	case model.BoardGreenhouse:
		return "https://boards-api.greenhouse.io/v1/boards/" + t + "/jobs?content=true", nil
	case model.BoardLever:
		return "https://api.lever.co/v0/postings/" + t + "?mode=json", nil
	case model.BoardAshby:
		return "https://api.ashbyhq.com/posting-api/job-board/" + t + "?includeCompensation=true", nil
	case model.BoardWorkday:
		// The first list page; see workday.go for how the rest is reached.
		base, err := workdayBase(token)
		if err != nil {
			return "", err
		}
		return workdayListURL(base, 0, 0, ""), nil
	case model.BoardKeka:
		return "https://" + t + ".keka.com/careers/api/jobs/default/active", nil
	case model.BoardDarwinbox:
		// The first list page; see paged.go for how the rest is reached.
		return darwinboxListURL(token, 1), nil
	case model.BoardSmartRecruiters:
		return smartRecruitersListURL(token, 0, ""), nil
	case model.BoardAmazon:
		return amazonListURL(0, ""), nil
	case model.BoardEightfold:
		host, domain, err := eightfoldParts(token)
		if err != nil {
			return "", err
		}
		return eightfoldListURL(host, domain, 0, ""), nil
	case model.BoardApple:
		return appleListURL(1, ""), nil
	case model.BoardAtlassian:
		return "https://www.atlassian.com/endpoint/careers/listings", nil
	case model.BoardOracle:
		host, site, err := oracleParts(token)
		if err != nil {
			return "", err
		}
		return oracleListURL(host, site, 0, ""), nil
	case model.BoardPhenom:
		host, locale, err := phenomParts(token)
		if err != nil {
			return "", err
		}
		return phenomListURL(host, locale, 0), nil
	case model.BoardSitemap:
		return sitemapFeedURL(strings.ToLower(token))
	}
	return "", fmt.Errorf("no feed for board type %q", boardType)
}

// Parse turns a feed body into job records.
func Parse(boardType string, body []byte) ([]model.RawJob, error) {
	switch boardType {
	case model.BoardGreenhouse:
		return parseGreenhouse(body)
	case model.BoardLever:
		return parseLever(body)
	case model.BoardAshby:
		return parseAshby(body)
	case model.BoardAtlassian:
		return parseAtlassian(body)
	}
	return nil, fmt.Errorf("no parser for board type %q", boardType)
}

// Feed reports whether a board is read as one JSON feed per company.
func Feed(boardType string) bool {
	switch boardType {
	case model.BoardGreenhouse, model.BoardLever, model.BoardAshby, model.BoardKeka, model.BoardAtlassian:
		return true
	}
	return false
}

// Known reports whether a board has a reader (a feed or list pages).
func Known(boardType string) bool { return Feed(boardType) || Paged(boardType) }

// ParseFeed is Parse for boards whose job links are built from the feed's
// own address.
func ParseFeed(boardType, feedURL string, body []byte) ([]model.RawJob, error) {
	if boardType == model.BoardKeka {
		return parseKeka(body, feedURL)
	}
	return Parse(boardType, body)
}

// Detect recognises a board from any URL that points at it (a careers page
// link, an iframe, a script src) and returns the board type and company token.
func Detect(rawURL string) (boardType, token string, ok bool) {
	for _, d := range detectors {
		if m := d.re.FindStringSubmatch(rawURL); m != nil {
			tok := m[1]
			if d.token != nil {
				tok = d.token(m)
			}
			if reserved[strings.ToLower(m[len(m)-1])] {
				continue
			}
			return d.board, tok, true
		}
	}
	return "", "", false
}

var reserved = map[string]bool{"embed": true, "v1": true, "api": true, "www": true, "js": true, "css": true, "static": true, "wday": true, "cdn": true, "app": true, "oneclick-ui": true}

var detectors = []struct {
	board string
	re    *regexp.Regexp
	token func(m []string) string // builds the token when it is not simply the first group
}{
	{board: model.BoardWorkday, re: workdayRe, token: workdayToken},
	{board: model.BoardGreenhouse, re: regexp.MustCompile(`(?i)(?:boards|job-boards)(?:\.eu)?\.greenhouse\.io/embed/job_board(?:/js)?\?(?:[^"'\s]*&)?for=([\w-]+)`)},
	{board: model.BoardGreenhouse, re: regexp.MustCompile(`(?i)boards-api\.greenhouse\.io/v1/boards/([\w-]+)`)},
	{board: model.BoardGreenhouse, re: regexp.MustCompile(`(?i)(?:boards|job-boards)(?:\.eu)?\.greenhouse\.io/([\w-]+)`)},
	{board: model.BoardLever, re: regexp.MustCompile(`(?i)(?:jobs|api)(?:\.eu)?\.lever\.co/(?:v0/postings/)?([\w.-]+)`)},
	{board: model.BoardAshby, re: regexp.MustCompile(`(?i)jobs\.ashbyhq\.com/([\w.%-]+)`)},
	{board: model.BoardAshby, re: regexp.MustCompile(`(?i)api\.ashbyhq\.com/posting-api/job-board/([\w.%-]+)`)},
	{board: model.BoardKeka, re: kekaRe, token: lowerToken},
	{board: model.BoardDarwinbox, re: darwinboxRe, token: lowerToken},
	{board: model.BoardSmartRecruiters, re: smartRecruitersRe},
	{board: model.BoardAmazon, re: amazonRe, token: func([]string) string { return "amazon" }},
	{board: model.BoardEightfold, re: eightfoldRe, token: eightfoldToken},
	{board: model.BoardApple, re: appleRe, token: func([]string) string { return "apple" }},
	{board: model.BoardAtlassian, re: atlassianRe, token: lowerToken},
	{board: model.BoardOracle, re: oracleRe, token: oracleToken},
	{board: model.BoardPhenom, re: phenomRe, token: phenomToken},
	{board: model.BoardSitemap, re: sitemapRe, token: lowerToken},
}

func lowerToken(m []string) string { return strings.ToLower(m[1]) }

var (
	tagBreakRe = regexp.MustCompile(`(?i)<\s*(?:br|/p|/div|/li|/h[1-6]|/tr)\s*/?>`)
	liRe       = regexp.MustCompile(`(?i)<\s*li[^>]*>`)
	dropRe     = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
	tagRe      = regexp.MustCompile(`(?s)<[^>]*>`)
	spaceRe    = regexp.MustCompile(`[ \t\x{00a0}]+`)
	blankRe    = regexp.MustCompile(`\n\s*\n+`)
)

// HTMLToText flattens HTML into readable plain text, keeping paragraph and
// list breaks so salary sentences stay on their own lines.
func HTMLToText(s string) string {
	s = dropRe.ReplaceAllString(s, " ")
	s = tagBreakRe.ReplaceAllString(s, "\n")
	s = liRe.ReplaceAllString(s, "\n- ")
	s = tagRe.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = spaceRe.ReplaceAllString(s, " ")
	s = strings.ReplaceAll(strings.ReplaceAll(s, " \n", "\n"), "\n ", "\n")
	s = blankRe.ReplaceAllString(s, "\n")
	return strings.TrimSpace(s)
}

// maxDescription keeps one job message far below the NATS message limit.
const maxDescription = 30_000

func clip(s string) string {
	if len(s) <= maxDescription {
		return s
	}
	return strings.ToValidUTF8(s[:maxDescription], "")
}

func looksRemote(parts ...string) bool {
	for _, p := range parts {
		if strings.Contains(strings.ToLower(p), "remote") {
			return true
		}
	}
	return false
}
