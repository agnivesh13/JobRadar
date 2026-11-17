package boards

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jobradar/internal/model"
)

// Darwinbox (https://{company}.darwinbox.in/ms/candidate/careers). Checked
// against live responses on 17 Nov 2026:
//
//	List:   GET https://{host}/ms/candidateapi/job?page=1&limit=50
//	        -> {"status":"success","message":{"jobscount":N,"jobs":[{id,title,...}]}}
//	Detail: GET https://{host}/ms/candidateapi/job/{id}
//	        -> {"status":"success","message":{"job":[{title,jd,experience,officelocations_without_area,
//	            department,emp_type,is_remote,posted_on,...}]}}
//
// Notes from the live check:
//   - It is the request the public careers page makes; no login, no key.
//   - Pages start at 1; "jobscount" is the total on every page; a page past
//     the end is an empty list.
//   - The list has no description, so each posting costs one more request.
//   - "jd" is HTML that is itself HTML-escaped ("&lt;div&gt;...").
//   - Locations carry a stray carriage return ("Gurgaon, Haryana\r, India").
//   - An unknown company answers HTTP 500 "Invalid subdomain".
//   - /robots.txt redirects to the login page, i.e. the host publishes no rules.
//
// Live result: the same requests sent with JobRadar's own User-Agent (which
// says it is a crawler) are refused with HTTP 403, while a neutral client is
// served. The User-Agent stays honest, so Darwinbox companies end up marked
// "manual" until Darwinbox lets declared crawlers in. The parser is kept for
// that day and is tested against the saved responses.
//
// The company token is the host ("acme.darwinbox.in").

const darwinboxPageSize = 50

var darwinboxRe = regexp.MustCompile(`(?i)\b([a-z0-9-]+\.darwinbox\.(?:in|com))/ms/candidate`)

func darwinboxListURL(host string, page int) string {
	return "https://" + host + "/ms/candidateapi/job?page=" + strconv.Itoa(page) + "&limit=" + strconv.Itoa(darwinboxPageSize)
}

func isDarwinboxList(u *url.URL) bool {
	return strings.TrimSuffix(u.Path, "/") == "/ms/candidateapi/job"
}

func parseDarwinboxList(body []byte, u *url.URL) (ListPage, error) {
	var out ListPage
	var doc struct {
		Status  string `json:"status"`
		Message struct {
			Count int `json:"jobscount"`
			Jobs  []struct {
				ID    string `json:"id"`
				Title string `json:"title"`
			} `json:"jobs"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return out, err
	}
	if doc.Status != "success" {
		return out, fmt.Errorf("darwinbox answered status %q", doc.Status)
	}
	for _, j := range doc.Message.Jobs {
		if j.ID == "" {
			continue
		}
		out.Postings = append(out.Postings, Posting{
			Title:     strings.TrimSpace(j.Title),
			URL:       "https://" + u.Host + "/ms/candidate/careers/" + url.PathEscape(j.ID),
			DetailURL: "https://" + u.Host + "/ms/candidateapi/job/" + url.PathEscape(j.ID),
		})
	}
	page, _ := strconv.Atoi(u.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	if len(out.Postings) > 0 && page*darwinboxPageSize < doc.Message.Count {
		out.Next = darwinboxListURL(u.Host, page+1)
	}
	return out, nil
}

func parseDarwinboxJob(body []byte, u *url.URL) (model.RawJob, error) {
	var doc struct {
		Message struct {
			Job []struct {
				ID          string          `json:"id"`
				Title       string          `json:"title"`
				Designation string          `json:"designation_name"`
				JD          string          `json:"jd"`
				Experience  string          `json:"experience"`
				Department  string          `json:"department"`
				EmpType     string          `json:"emp_type"`
				Locations   json.RawMessage `json:"officelocations_without_area"`
				IsRemote    json.RawMessage `json:"is_remote"`
				PostedOn    json.RawMessage `json:"posted_on"`
			} `json:"job"`
		} `json:"message"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return model.RawJob{}, err
	}
	if len(doc.Message.Job) == 0 {
		return model.RawJob{}, fmt.Errorf("no job in the response")
	}
	j := doc.Message.Job[0]
	title := firstNonEmpty(strings.TrimSpace(j.Title), strings.TrimSpace(j.Designation))
	if title == "" {
		return model.RawJob{}, fmt.Errorf("posting has no title")
	}
	var raw, locations []string
	json.Unmarshal(j.Locations, &raw) // not a list on some postings: then no location
	for _, l := range raw {
		l = strings.ReplaceAll(strings.Join(strings.Fields(l), " "), " ,", ",")
		if l != "" && !containsString(locations, l) {
			locations = append(locations, l)
		}
	}
	desc := HTMLToText(html.UnescapeString(j.JD))
	if exp := strings.TrimSpace(j.Experience); exp != "" {
		desc = "Experience: " + exp + "\n" + desc
	}
	remote := strings.Trim(string(j.IsRemote), `"`)
	job := model.RawJob{
		URL:            "https://" + u.Host + "/ms/candidate/careers/" + url.PathEscape(firstNonEmpty(j.ID, u.Path[strings.LastIndex(u.Path, "/")+1:])),
		Title:          title,
		Location:       strings.Join(locations, " / "),
		Remote:         remote == "1" || remote == "true" || looksRemote(locations...),
		Department:     j.Department,
		EmploymentType: j.EmpType,
		Description:    clip(strings.TrimSpace(desc)),
	}
	if secs, err := strconv.ParseInt(strings.Trim(string(j.PostedOn), `"`), 10, 64); err == nil && secs > 0 {
		t := time.Unix(secs, 0).UTC()
		job.PostedAt = &t
	}
	return job, nil
}
