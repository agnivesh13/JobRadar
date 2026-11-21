package boards

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Shared by the site crawler (package extract) and the sitemap board: URL
// cleaning, schema.org JobPosting data and sitemaps.

// CleanURL makes href absolute and drops fragments, non-http schemes and
// tracking parameters.
func CleanURL(base *url.URL, href string) string {
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

// JobPostings finds every schema.org JobPosting in one JSON-LD block,
// wherever it is nested (top level, an array, or an @graph).
func JobPostings(data []byte, base *url.URL) []model.RawJob {
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
	// Some sites HTML-escape the title inside the JSON ("Reporting &amp; Data").
	j := model.RawJob{Title: html.UnescapeString(str(m["title"])), EmploymentType: str(m["employmentType"])}
	if j.Title == "" {
		return j, false
	}
	j.URL = CleanURL(base, str(m["url"]))
	if j.URL == "" {
		j.URL = base.String()
	}
	j.Description = HTMLToText(str(m["description"]))
	if len(j.Description) > 30_000 {
		j.Description = strings.ToValidUTF8(j.Description[:30_000], "")
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02", "2006/01/02"} {
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
