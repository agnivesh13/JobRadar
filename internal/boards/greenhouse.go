package boards

import (
	"encoding/json"
	"html"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Feed: GET https://boards-api.greenhouse.io/v1/boards/{token}/jobs?content=true
// Shape: {"jobs":[{...}],"meta":{"total":N}}
// Notes: "content" is HTML that is itself HTML-escaped, so it is unescaped
// before tags are stripped. The bulk feed carries no structured pay field
// (checked with pay_transparency=true: no job had one), so salary for
// Greenhouse jobs is always read from the description text.
type greenhouseFeed struct {
	Jobs []struct {
		AbsoluteURL    string `json:"absolute_url"`
		Title          string `json:"title"`
		Content        string `json:"content"`
		FirstPublished string `json:"first_published"`
		UpdatedAt      string `json:"updated_at"`
		Location       struct {
			Name string `json:"name"`
		} `json:"location"`
		Departments []struct {
			Name string `json:"name"`
		} `json:"departments"`
		Offices []struct {
			Name string `json:"name"`
		} `json:"offices"`
	} `json:"jobs"`
}

func parseGreenhouse(body []byte) ([]model.RawJob, error) {
	var feed greenhouseFeed
	if err := json.Unmarshal(body, &feed); err != nil {
		return nil, err
	}
	jobs := make([]model.RawJob, 0, len(feed.Jobs))
	for _, j := range feed.Jobs {
		if j.AbsoluteURL == "" || strings.TrimSpace(j.Title) == "" {
			continue
		}
		job := model.RawJob{
			URL:         j.AbsoluteURL,
			Title:       strings.TrimSpace(j.Title),
			Location:    strings.TrimSpace(j.Location.Name),
			Description: clip(HTMLToText(html.UnescapeString(j.Content))),
		}
		if len(j.Departments) > 0 {
			job.Department = j.Departments[0].Name
		}
		officeNames := make([]string, 0, len(j.Offices))
		for _, o := range j.Offices {
			officeNames = append(officeNames, o.Name)
		}
		job.Remote = looksRemote(append(officeNames, job.Location)...)
		for _, ts := range []string{j.FirstPublished, j.UpdatedAt} {
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				job.PostedAt = &t
				break
			}
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}
