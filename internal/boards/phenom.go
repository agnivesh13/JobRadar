package boards

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Phenom career sites, as used by IDFC FIRST Bank
// (https://careers.idfcfirst.bank.in/in/en/search-results). Checked against
// live pages on 21 Nov 2026:
//
//	List:   GET https://{host}/{locale}/search-results?from=N&s=1
//	        -> HTML with "phApp.ddo = {...}"; ddo.eagerLoadRefineSearch is
//	           {totalHits, hits, data: {jobs: [{jobSeqNo, title, location, ...}]}}
//	Detail: GET https://{host}/{locale}/job/{jobSeqNo}
//	        -> HTML with ddo.jobDetail.data.job: {title, description (HTML),
//	           location, multi_location, postedDate, type, ...}
//
// Notes from the live check:
//   - The page renders in the browser, but the data it renders is in the
//     HTML: no script is run and no API outside robots.txt is called
//     (robots.txt disallows only apply, chatbot, widget and tracking paths).
//   - 10 jobs per page; the last page (from=1370 of 1373) answered normally.
//   - The job page works without the title slug, so the job URL is stable.
//   - The detail data carries recruiter fields (names, e-mail addresses);
//     only the posting's own fields are read.
//
// Only hosts checked live are detected. The token is "{host}/{locale}".

const phenomPageSize = 10

type phenomSite struct{ locale, name string }

var phenomHosts = map[string]phenomSite{
	"careers.idfcfirst.bank.in": {locale: "in/en", name: "IDFC FIRST Bank"},
}

var phenomRe = regexp.MustCompile(`(?i)\b(careers\.idfcfirst\.bank\.in)/`)

func phenomToken(m []string) string {
	host := strings.ToLower(m[1])
	return host + "/" + phenomHosts[host].locale
}

func phenomParts(token string) (host, locale string, err error) {
	host, locale, ok := strings.Cut(token, "/")
	if !ok || host == "" || locale == "" {
		return "", "", fmt.Errorf("bad phenom token %q (want host/locale)", token)
	}
	return host, strings.Trim(locale, "/"), nil
}

func phenomListURL(host, locale string, from int) string {
	return "https://" + host + "/" + locale + "/search-results?from=" + strconv.Itoa(from) + "&s=1"
}

func isPhenomList(u *url.URL) bool { return strings.HasSuffix(u.Path, "/search-results") }

// phenomDDO returns the JSON object assigned to phApp.ddo in a page.
func phenomDDO(body []byte) ([]byte, error) {
	s := string(body)
	i := strings.Index(s, "phApp.ddo = ")
	if i < 0 {
		return nil, fmt.Errorf("no phApp.ddo in the page")
	}
	s = s[i+len("phApp.ddo = "):]
	depth, inStr, esc := 0, false, false
	for k := 0; k < len(s); k++ {
		c := s[k]
		switch {
		case inStr:
			if esc {
				esc = false
			} else if c == '\\' {
				esc = true
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return []byte(s[:k+1]), nil
			}
		}
	}
	return nil, fmt.Errorf("phApp.ddo is cut off")
}

// phenomLocale is the locale part of a Phenom URL path ("/in/en/job/X" -> "in/en").
func phenomLocale(u *url.URL) string {
	p := strings.Trim(u.Path, "/")
	for _, cut := range []string{"/search-results", "/job/"} {
		if i := strings.Index(p, cut); i >= 0 {
			return p[:i]
		}
	}
	return p
}

func parsePhenomList(body []byte, u *url.URL) (ListPage, error) {
	var out ListPage
	raw, err := phenomDDO(body)
	if err != nil {
		return out, err
	}
	var doc struct {
		Search struct {
			TotalHits int `json:"totalHits"`
			Data      struct {
				Jobs []struct {
					SeqNo string `json:"jobSeqNo"`
					Title string `json:"title"`
				} `json:"jobs"`
			} `json:"data"`
		} `json:"eagerLoadRefineSearch"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return out, err
	}
	from, _ := strconv.Atoi(u.Query().Get("from"))
	jobs := doc.Search.Data.Jobs
	if doc.Search.TotalHits > from && len(jobs) == 0 {
		return out, fmt.Errorf("the site reports %d jobs but this page holds none", doc.Search.TotalHits)
	}
	locale := phenomLocale(u)
	for _, j := range jobs {
		if j.SeqNo == "" {
			continue
		}
		link := "https://" + u.Host + "/" + locale + "/job/" + url.PathEscape(j.SeqNo)
		out.Postings = append(out.Postings, Posting{Title: strings.TrimSpace(j.Title), URL: link, DetailURL: link})
	}
	if len(jobs) > 0 && from+len(jobs) < doc.Search.TotalHits {
		out.Next = phenomListURL(u.Host, locale, from+len(jobs))
	}
	return out, nil
}

func parsePhenomJob(body []byte, u *url.URL) (model.RawJob, error) {
	raw, err := phenomDDO(body)
	if err != nil {
		return model.RawJob{}, err
	}
	var doc struct {
		Detail struct {
			Data struct {
				Job struct {
					Title         string `json:"title"`
					Description   string `json:"description"`
					Location      string `json:"location"`
					MultiLocation []struct {
						Location string `json:"location"`
					} `json:"multi_location"` // objects here; plain strings on the list page
					PostedDate string `json:"postedDate"`
					Type       string `json:"type"`
					Category   string `json:"category"`
				} `json:"job"`
			} `json:"data"`
		} `json:"jobDetail"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return model.RawJob{}, err
	}
	d := doc.Detail.Data.Job
	if strings.TrimSpace(d.Title) == "" {
		return model.RawJob{}, fmt.Errorf("no job in the page")
	}
	var locations []string
	places := []string{d.Location}
	for _, m := range d.MultiLocation {
		places = append(places, m.Location)
	}
	for _, l := range places {
		if l = strings.TrimSpace(l); l != "" && !containsString(locations, l) {
			locations = append(locations, l)
		}
	}
	job := model.RawJob{
		URL:            u.String(),
		Title:          strings.TrimSpace(d.Title),
		Location:       strings.Join(locations, " / "),
		Remote:         looksRemote(locations...),
		Department:     d.Category,
		EmploymentType: d.Type,
		Description:    clip(HTMLToText(d.Description)),
	}
	if t, err := time.Parse("2006-01-02T15:04:05.000-0700", d.PostedDate); err == nil {
		t = t.UTC()
		job.PostedAt = &t
	}
	return job, nil
}
