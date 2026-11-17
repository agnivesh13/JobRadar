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

// Apple (https://jobs.apple.com). Checked against live responses on
// 17 Nov 2026:
//
//	List:   GET https://jobs.apple.com/en-in/search?page=N[&location=india-INDC]
//	Detail: GET https://jobs.apple.com/en-in/details/{positionId}/{slug}?team={teamCode}
//
// Both are ordinary HTML pages that carry their data as JSON in
// `window.__staticRouterHydrationData = JSON.parse("...")`:
//   - search:  loaderData.search.{totalRecords, searchResults:[{positionId,postingTitle,
//     transformedPostingTitle,team:{teamCode}}]}, 20 per page
//   - details: loaderData.jobDetails.jobsData.{postingTitle,jobSummary,description,
//     minimumQualifications,preferredQualifications,locations:[{name}],postDateInGMT,teamNames}
//
// Notes from the live check:
//   - jobs.apple.com has no robots.txt (it redirects to a "page not found").
//   - Location filters are Apple's own ids ("india-INDC"); only India's was
//     looked up, other countries read the whole list.
//   - A whole-site crawl of the same pages found nothing: the links on the
//     page lead to location pickers and country pages, the postings are only
//     in the JSON.
//
// There is one company, so the token is always "apple".

var (
	appleRe       = regexp.MustCompile(`(?i)\b(jobs\.apple\.com)/[a-z]{2}-[a-z]{2}/(?:search|details)`)
	appleHydrate  = regexp.MustCompile(`window\.__staticRouterHydrationData\s*=\s*JSON\.parse\("((?:[^"\\]|\\.)*)"\)`)
	appleLocation = map[string]string{"india": "india-INDC"}
)

const applePageSize = 20

func appleListURL(page int, location string) string {
	u := "https://jobs.apple.com/en-in/search?page=" + strconv.Itoa(page)
	if location != "" {
		u += "&location=" + url.QueryEscape(location)
	}
	return u
}

func isAppleList(u *url.URL) bool { return strings.HasSuffix(u.Path, "/search") }

// appleData decodes the hydration JSON of a jobs.apple.com page into v.
func appleData(body []byte, v any) error {
	m := appleHydrate.FindSubmatch(body)
	if m == nil {
		return fmt.Errorf("no page data found")
	}
	var raw string // the argument is a JavaScript string literal holding JSON
	if err := json.Unmarshal([]byte(`"`+string(m[1])+`"`), &raw); err != nil {
		return err
	}
	return json.Unmarshal([]byte(raw), v)
}

func parseAppleList(body []byte, u *url.URL, country string) (ListPage, error) {
	var out ListPage
	q := u.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	location := q.Get("location")
	if page == 1 && location == "" && q.Get("all") == "" && country != "" {
		if loc := appleLocation[strings.ToLower(strings.TrimSpace(country))]; loc != "" {
			out.Refine = appleListURL(1, loc)
			out.Note = "listing narrowed to " + country
			return out, nil
		}
	}
	var doc struct {
		LoaderData struct {
			Search struct {
				Total   int `json:"totalRecords"`
				Results []struct {
					PositionID string `json:"positionId"`
					Title      string `json:"postingTitle"`
					Slug       string `json:"transformedPostingTitle"`
					Team       struct {
						Code string `json:"teamCode"`
					} `json:"team"`
				} `json:"searchResults"`
			} `json:"search"`
		} `json:"loaderData"`
	}
	if err := appleData(body, &doc); err != nil {
		return out, err
	}
	s := doc.LoaderData.Search
	for _, r := range s.Results {
		if r.PositionID == "" {
			continue
		}
		link := "https://jobs.apple.com/en-in/details/" + url.PathEscape(r.PositionID) + "/" + url.PathEscape(firstNonEmpty(r.Slug, "job"))
		if r.Team.Code != "" {
			link += "?team=" + url.QueryEscape(r.Team.Code)
		}
		out.Postings = append(out.Postings, Posting{Title: strings.TrimSpace(r.Title), URL: link, DetailURL: link})
	}
	if len(s.Results) > 0 && page*applePageSize < s.Total {
		out.Next = appleListURL(page+1, location)
		if location == "" {
			out.Next += "&all=1"
		}
	}
	return out, nil
}

func parseAppleJob(body []byte, u *url.URL) (model.RawJob, error) {
	var doc struct {
		LoaderData struct {
			JobDetails struct {
				Data struct {
					Title     string   `json:"postingTitle"`
					Summary   string   `json:"jobSummary"`
					Desc      string   `json:"description"`
					Minimum   string   `json:"minimumQualifications"`
					Preferred string   `json:"preferredQualifications"`
					Teams     []string `json:"teamNames"`
					PostDate  string   `json:"postDateInGMT"`
					Home      bool     `json:"homeOffice"`
					Locations []struct {
						Name        string `json:"name"`
						CountryName string `json:"countryName"`
					} `json:"locations"`
				} `json:"jobsData"`
			} `json:"jobDetails"`
		} `json:"loaderData"`
	}
	if err := appleData(body, &doc); err != nil {
		return model.RawJob{}, err
	}
	d := doc.LoaderData.JobDetails.Data
	if strings.TrimSpace(d.Title) == "" {
		return model.RawJob{}, fmt.Errorf("no posting on the page")
	}
	var locations []string
	for _, l := range d.Locations {
		loc := l.Name
		if l.CountryName != "" && !strings.Contains(loc, l.CountryName) {
			loc = joinNonEmpty(", ", loc, l.CountryName)
		}
		if loc != "" && !containsString(locations, loc) {
			locations = append(locations, loc)
		}
	}
	var desc []string
	for _, s := range [][2]string{{"Summary", d.Summary}, {"Description", d.Desc}, {"Minimum qualifications", d.Minimum}, {"Preferred qualifications", d.Preferred}} {
		if text := strings.TrimSpace(HTMLToText(s[1])); text != "" {
			desc = append(desc, s[0]+"\n"+text)
		}
	}
	job := model.RawJob{
		URL:         u.String(), // as the list names it, so a repeat crawl recognises it
		Title:       strings.TrimSpace(d.Title),
		Location:    strings.Join(locations, " / "),
		Remote:      d.Home || looksRemote(locations...),
		Description: clip(strings.Join(desc, "\n")),
	}
	if len(d.Teams) > 0 {
		job.Department = d.Teams[0]
	}
	if t, err := time.Parse(time.RFC3339, d.PostDate); err == nil {
		job.PostedAt = &t
	}
	return job, nil
}
