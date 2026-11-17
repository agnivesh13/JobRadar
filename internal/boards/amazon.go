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

// Amazon (https://www.amazon.jobs). Checked against live responses on
// 17 Nov 2026:
//
//	List: GET https://www.amazon.jobs/en/search.json?result_limit=100&offset=N&sort=recent[&country=IND]
//	      -> {"hits":N,"jobs":[{title,job_path,city,state,country_code,description,
//	          basic_qualifications,preferred_qualifications,posted_date,job_schedule_type,job_category}],"error":null}
//
// Notes from the live check:
//   - It is the request the public search page makes; robots.txt disallows
//     only the /internal paths and one named bot.
//   - The list carries the full description, so a page of 100 postings is one
//     request and there are no posting pages to fetch.
//   - "country" takes an ISO 3166 alpha-3 code (IND: 2066 postings). Without
//     it "hits" is capped at 10000. loc_query=India matches text, not country.
//   - An offset past the end is an empty list (with the same "hits").
//
// There is one company, so the token is always "amazon". The country comes
// from WORKDAY_COUNTRY through the same refine step as the other paged boards.

const amazonPageSize = 100

var amazonRe = regexp.MustCompile(`(?i)\b(?:www\.)?(amazon)\.jobs\b`)

// isoCountry maps a few country names to the alpha-3 codes Amazon filters by.
var isoCountry = map[string]string{
	"india": "IND", "united states": "USA", "usa": "USA", "united kingdom": "GBR", "uk": "GBR",
	"canada": "CAN", "germany": "DEU", "ireland": "IRL", "singapore": "SGP", "australia": "AUS",
	"japan": "JPN", "france": "FRA", "netherlands": "NLD", "spain": "ESP", "poland": "POL",
	"brazil": "BRA", "mexico": "MEX", "china": "CHN", "united arab emirates": "ARE", "israel": "ISR",
}

// countryName is the reverse, for readable locations.
var countryName = map[string]string{
	"IND": "India", "USA": "United States", "GBR": "United Kingdom", "CAN": "Canada", "DEU": "Germany",
	"IRL": "Ireland", "SGP": "Singapore", "AUS": "Australia", "JPN": "Japan", "FRA": "France",
	"NLD": "Netherlands", "ESP": "Spain", "POL": "Poland", "BRA": "Brazil", "MEX": "Mexico",
	"CHN": "China", "ARE": "United Arab Emirates", "ISR": "Israel",
}

func amazonListURL(offset int, country string) string {
	u := "https://www.amazon.jobs/en/search.json?result_limit=" + strconv.Itoa(amazonPageSize) + "&offset=" + strconv.Itoa(offset) + "&sort=recent"
	if country != "" {
		u += "&country=" + url.QueryEscape(country)
	}
	return u
}

func isAmazonList(u *url.URL) bool { return strings.HasSuffix(u.Path, "/search.json") }

func parseAmazonList(body []byte, u *url.URL, country string) (ListPage, error) {
	var out ListPage
	q := u.Query()
	offset, _ := strconv.Atoi(q.Get("offset"))
	code := q.Get("country")
	if offset == 0 && code == "" && q.Get("all") == "" && country != "" {
		if c := isoCountry[strings.ToLower(strings.TrimSpace(country))]; c != "" {
			out.Refine = amazonListURL(0, c)
			out.Note = "listing narrowed to " + country
			return out, nil
		}
	}
	var doc struct {
		Error any `json:"error"`
		Hits  int `json:"hits"`
		Jobs  []struct {
			Title          string `json:"title"`
			JobPath        string `json:"job_path"`
			City           string `json:"city"`
			State          string `json:"state"`
			CountryCode    string `json:"country_code"`
			Description    string `json:"description"`
			Basic          string `json:"basic_qualifications"`
			Preferred      string `json:"preferred_qualifications"`
			PostedDate     string `json:"posted_date"`
			ScheduleType   string `json:"job_schedule_type"`
			JobCategory    string `json:"job_category"`
			BusinessCatego string `json:"business_category"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return out, err
	}
	if doc.Error != nil {
		return out, fmt.Errorf("amazon answered an error: %v", doc.Error)
	}
	for _, j := range doc.Jobs {
		if j.JobPath == "" || strings.TrimSpace(j.Title) == "" {
			continue
		}
		var desc []string
		for _, s := range [][2]string{{"", j.Description}, {"Basic qualifications", j.Basic}, {"Preferred qualifications", j.Preferred}} {
			if text := HTMLToText(s[1]); text != "" {
				desc = append(desc, joinNonEmpty("\n", s[0], text))
			}
		}
		job := model.RawJob{
			URL:            "https://www.amazon.jobs" + j.JobPath,
			Title:          strings.TrimSpace(j.Title),
			Location:       joinNonEmpty(", ", j.City, j.State, firstNonEmpty(countryName[j.CountryCode], j.CountryCode)),
			Department:     j.JobCategory,
			EmploymentType: j.ScheduleType,
			Description:    clip(strings.Join(desc, "\n")),
		}
		job.Remote = looksRemote(job.Location)
		if t, err := time.Parse("January 2, 2006", j.PostedDate); err == nil {
			job.PostedAt = &t
		}
		out.Jobs = append(out.Jobs, job)
	}
	if len(doc.Jobs) > 0 && offset+amazonPageSize < min(doc.Hits, 10000) {
		out.Next = amazonListURL(offset+amazonPageSize, code)
		if code == "" {
			out.Next += "&all=1"
		}
	}
	return out, nil
}
