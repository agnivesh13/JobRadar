package boards

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Keka Hire (https://{company}.keka.com/careers/). Checked against a live
// response on 17 Nov 2026:
//
//	Feed: GET https://{company}.keka.com/careers/api/jobs/default/active
//	      -> [{id,title,description (HTML),departmentName,jobLocations:[{name,city,state,countryName}],
//	           jobType,experience,salaryRange,publishedOn}]
//
// Notes from the live check:
//   - It is the request the public careers page makes; no login, no key.
//   - robots.txt disallows "/" but allows "/careers", which covers the feed.
//   - The whole list, descriptions included, comes in one response.
//   - jobType: 1 = part time, 2 = full time (from the page's own script).
//   - salaryRange was a placeholder (0 to 1) on every posting seen, so it is
//     not read; pay is taken from the description like everywhere else.
//   - An unknown company redirects to .../Content/TenantNotFound.html.
//
// Not covered: named portals ("/careers/{portal}/"); only the default one is read.

var kekaRe = regexp.MustCompile(`(?i)\b([a-z0-9-]+)\.keka\.com/careers\b`)

type kekaJob struct {
	ID             int64  `json:"id"`
	Title          string `json:"title"`
	Description    string `json:"description"`
	DepartmentName string `json:"departmentName"`
	JobType        int    `json:"jobType"`
	Experience     string `json:"experience"`
	PublishedOn    string `json:"publishedOn"`
	JobLocations   []struct {
		Name        string `json:"name"`
		City        string `json:"city"`
		State       string `json:"state"`
		CountryName string `json:"countryName"`
	} `json:"jobLocations"`
}

func parseKeka(body []byte, feedURL string) ([]model.RawJob, error) {
	var feed []kekaJob
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, err
	}
	u, err := url.Parse(feedURL)
	if err != nil {
		return nil, err
	}
	base := u.Scheme + "://" + u.Host + "/careers/jobdetails/"
	jobs := make([]model.RawJob, 0, len(feed))
	for _, j := range feed {
		if j.ID == 0 || strings.TrimSpace(j.Title) == "" {
			continue
		}
		var locations []string
		for _, l := range j.JobLocations {
			loc := joinNonEmpty(", ", firstNonEmpty(l.City, l.Name), l.State, l.CountryName)
			if loc != "" && !containsString(locations, loc) {
				locations = append(locations, loc)
			}
		}
		desc := HTMLToText(j.Description)
		if exp := strings.TrimSpace(j.Experience); exp != "" {
			desc = "Experience: " + exp + " years\n" + desc
		}
		job := model.RawJob{
			URL:            base + strconv.FormatInt(j.ID, 10),
			Title:          strings.TrimSpace(j.Title),
			Location:       strings.Join(locations, " / "),
			Remote:         looksRemote(locations...),
			Department:     j.DepartmentName,
			EmploymentType: map[int]string{1: "Part time", 2: "Full time"}[j.JobType],
			Description:    clip(desc),
		}
		if t, err := time.Parse(time.RFC3339, j.PublishedOn); err == nil {
			job.PostedAt = &t
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func joinNonEmpty(sep string, parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, sep)
}
