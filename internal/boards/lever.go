package boards

import (
	"encoding/json"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Feed: GET https://api.lever.co/v0/postings/{token}?mode=json
// Shape: a bare JSON array of postings.
// Notes: the description is split over "descriptionPlain", "lists" (titled
// HTML sections) and "additionalPlain". "createdAt" is epoch milliseconds.
// "salaryRange" appears only on postings where the company filled it in.
type leverPosting struct {
	Text             string `json:"text"`
	HostedURL        string `json:"hostedUrl"`
	CreatedAt        int64  `json:"createdAt"`
	WorkplaceType    string `json:"workplaceType"`
	DescriptionPlain string `json:"descriptionPlain"`
	AdditionalPlain  string `json:"additionalPlain"`
	Categories       struct {
		Commitment   string   `json:"commitment"`
		Department   string   `json:"department"`
		Team         string   `json:"team"`
		Location     string   `json:"location"`
		AllLocations []string `json:"allLocations"`
	} `json:"categories"`
	Lists []struct {
		Text    string `json:"text"`
		Content string `json:"content"`
	} `json:"lists"`
	SalaryRange *struct {
		Min      float64 `json:"min"`
		Max      float64 `json:"max"`
		Currency string  `json:"currency"`
		Interval string  `json:"interval"`
	} `json:"salaryRange"`
	SalaryDescriptionPlain string `json:"salaryDescriptionPlain"`
}

func parseLever(body []byte) ([]model.RawJob, error) {
	var postings []leverPosting
	if err := json.Unmarshal(body, &postings); err != nil {
		return nil, err
	}
	jobs := make([]model.RawJob, 0, len(postings))
	for _, p := range postings {
		if p.HostedURL == "" || strings.TrimSpace(p.Text) == "" {
			continue
		}
		var desc strings.Builder
		desc.WriteString(p.DescriptionPlain)
		for _, l := range p.Lists {
			desc.WriteString("\n" + l.Text + "\n" + HTMLToText(l.Content))
		}
		desc.WriteString("\n" + p.AdditionalPlain)
		if p.SalaryDescriptionPlain != "" {
			desc.WriteString("\n" + p.SalaryDescriptionPlain)
		}

		location := p.Categories.Location
		if len(p.Categories.AllLocations) > 1 {
			location = strings.Join(p.Categories.AllLocations, " / ")
		}
		job := model.RawJob{
			URL:            p.HostedURL,
			Title:          strings.TrimSpace(p.Text),
			Location:       strings.TrimSpace(location),
			Remote:         strings.EqualFold(p.WorkplaceType, "remote") || looksRemote(location),
			Department:     firstNonEmpty(p.Categories.Team, p.Categories.Department),
			EmploymentType: p.Categories.Commitment,
			Description:    clip(strings.TrimSpace(desc.String())),
		}
		if p.CreatedAt > 0 {
			t := time.UnixMilli(p.CreatedAt).UTC()
			job.PostedAt = &t
		}
		if s := p.SalaryRange; s != nil && (s.Min > 0 || s.Max > 0) {
			job.Pay = &model.Pay{Min: s.Min, Max: s.Max, Currency: strings.ToUpper(s.Currency), Period: leverPeriod(s.Interval)}
		}
		jobs = append(jobs, job)
	}
	return jobs, nil
}

func leverPeriod(interval string) string {
	i := strings.ToLower(interval)
	switch {
	case strings.Contains(i, "month"):
		return "month"
	case strings.Contains(i, "hour"):
		return "hour"
	}
	return "year"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
