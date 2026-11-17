package boards

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Atlassian (https://www.atlassian.com/company/careers). Checked against a
// live response on 17 Nov 2026:
//
//	Feed: GET https://www.atlassian.com/endpoint/careers/listings
//	      -> [{id,title,type,locations:[...],category,overview,responsibilities,qualifications (HTML),
//	           portalJobPost:{updatedDate:"2026-11-09 12:21 PM"}}]
//
// Notes from the live check:
//   - It is the request the public careers page makes; robots.txt does not
//     disallow /endpoint/ or /company/careers.
//   - Every opening worldwide (266) comes in one response of about 2 MB, with
//     the full description, so there are no posting pages to fetch. India
//     postings are kept by the location filter like any other feed.
//   - Locations read "Bengaluru - India -   Bengaluru,  560071 India".
//   - The public posting page is /company/careers/details/{id}.
//
// There is one company, so the token is always "atlassian".

var atlassianRe = regexp.MustCompile(`(?i)\bwww\.(atlassian)\.com/(?:company/careers|endpoint/careers)`)

func parseAtlassian(body []byte) ([]model.RawJob, error) {
	var feed []struct {
		ID               int64    `json:"id"`
		Title            string   `json:"title"`
		Type             string   `json:"type"`
		Locations        []string `json:"locations"`
		Category         string   `json:"category"`
		Overview         string   `json:"overview"`
		Responsibilities string   `json:"responsibilities"`
		Qualifications   string   `json:"qualifications"`
		Portal           struct {
			UpdatedDate string `json:"updatedDate"`
		} `json:"portalJobPost"`
	}
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, err
	}
	jobs := make([]model.RawJob, 0, len(feed))
	for _, j := range feed {
		if j.ID == 0 || strings.TrimSpace(j.Title) == "" {
			continue
		}
		locs := make([]string, 0, len(j.Locations))
		for _, l := range j.Locations {
			locs = append(locs, strings.Join(strings.Fields(l), " "))
		}
		var desc []string
		for _, s := range [][2]string{{"", j.Overview}, {"Responsibilities", j.Responsibilities}, {"Qualifications", j.Qualifications}} {
			if text := HTMLToText(s[1]); text != "" {
				desc = append(desc, joinNonEmpty("\n", s[0], text))
			}
		}
		job := model.RawJob{
			URL:            "https://www.atlassian.com/company/careers/details/" + strconv.FormatInt(j.ID, 10),
			Title:          strings.TrimSpace(j.Title),
			Location:       strings.Join(locs, " / "),
			Department:     j.Category,
			EmploymentType: j.Type,
			Description:    clip(strings.Join(desc, "\n")),
		}
		job.Remote = looksRemote(locs...)
		if t, err := time.Parse("2006-01-02 3:04 PM", j.Portal.UpdatedDate); err == nil {
			job.PostedAt = &t
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
