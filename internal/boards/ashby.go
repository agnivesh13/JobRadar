package boards

import (
	"encoding/json"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Feed: GET https://api.ashbyhq.com/posting-api/job-board/{token}?includeCompensation=true
// Shape: {"jobs":[{...}],"apiVersion":"1"}
// Notes: compensation.summaryComponents holds structured pay; only the
// component with compensationType "Salary" is used. Intervals seen in live
// data: "1 YEAR", "1 MONTH", "1 HOUR", "NONE". Unlisted jobs (isListed=false)
// are skipped.
type ashbyFeed struct {
	Jobs []struct {
		Title              string `json:"title"`
		Department         string `json:"department"`
		Team               string `json:"team"`
		EmploymentType     string `json:"employmentType"`
		Location           string `json:"location"`
		PublishedAt        string `json:"publishedAt"`
		IsListed           *bool  `json:"isListed"`
		IsRemote           bool   `json:"isRemote"`
		JobURL             string `json:"jobUrl"`
		DescriptionPlain   string `json:"descriptionPlain"`
		DescriptionHTML    string `json:"descriptionHtml"`
		SecondaryLocations []struct {
			Location string `json:"location"`
		} `json:"secondaryLocations"`
		Compensation *struct {
			Summary    string `json:"scrapeableCompensationSalarySummary"`
			Components []struct {
				Type     string   `json:"compensationType"`
				Interval string   `json:"interval"`
				Currency *string  `json:"currencyCode"`
				Min      *float64 `json:"minValue"`
				Max      *float64 `json:"maxValue"`
			} `json:"summaryComponents"`
		} `json:"compensation"`
	} `json:"jobs"`
}

func parseAshby(body []byte) ([]model.RawJob, error) {
	var feed ashbyFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, err
	}
	jobs := make([]model.RawJob, 0, len(feed.Jobs))
	for _, j := range feed.Jobs {
		if j.JobURL == "" || strings.TrimSpace(j.Title) == "" || (j.IsListed != nil && !*j.IsListed) {
			continue
		}
		locations := []string{strings.TrimSpace(j.Location)}
		for _, l := range j.SecondaryLocations {
			locations = append(locations, strings.TrimSpace(l.Location))
		}
		desc := j.DescriptionPlain
		if desc == "" {
			desc = HTMLToText(j.DescriptionHTML)
		}
		job := model.RawJob{
			URL:            j.JobURL,
			Title:          strings.TrimSpace(j.Title),
			Location:       strings.Join(locations, " / "),
			Remote:         j.IsRemote,
			Department:     firstNonEmpty(j.Team, j.Department),
			EmploymentType: j.EmploymentType,
			Description:    clip(strings.TrimSpace(desc)),
		}
		if t, err := time.Parse(time.RFC3339, j.PublishedAt); err == nil {
			job.PostedAt = &t
		}
		if c := j.Compensation; c != nil {
			for _, comp := range c.Components {
				if comp.Type != "Salary" || comp.Currency == nil || (comp.Min == nil && comp.Max == nil) {
					continue
				}
				pay := &model.Pay{Currency: strings.ToUpper(*comp.Currency), Period: ashbyPeriod(comp.Interval), Text: c.Summary}
				if comp.Min != nil {
					pay.Min = *comp.Min
				}
				if comp.Max != nil {
					pay.Max = *comp.Max
				}
				job.Pay = pay
				break
			}
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func ashbyPeriod(interval string) string {
	switch {
	case strings.Contains(interval, "MONTH"):
		return "month"
	case strings.Contains(interval, "HOUR"):
		return "hour"
	case strings.Contains(interval, "WEEK"):
		return "week"
	}
	return "year"
}
