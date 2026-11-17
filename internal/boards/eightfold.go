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

// Eightfold career sites ("PCSX"), as used by Microsoft
// (https://apply.careers.microsoft.com/careers). Checked against live
// responses on 17 Nov 2026:
//
//	List:   GET https://{host}/api/pcsx/search?domain={domain}&query=&start=N[&location=India]
//	        -> {"status":200,"data":{"count":N,"positions":[{id,name,locations,department,postedTs,positionUrl}]}}
//	Detail: GET https://{host}/api/pcsx/position_details?position_id={id}&domain={domain}&hl=en
//	        -> {"data":{name,jobDescription (HTML),locations,department,postedTs,publicUrl,
//	            efcustomTextEmploymentType,...}}
//
// Notes from the live check:
//   - These are the requests the public careers page makes. robots.txt
//     disallows "/" but allows /careers and /api/pcsx.
//   - 10 positions per page, "count" on every page (219 for location=India).
//   - The list has no description: one more request per position.
//   - The site sometimes answers 429 "Please try again later"; the client
//     backs off and the task is retried.
//
// Only hosts checked live are detected (eightfoldHosts). Qualcomm's
// (careers.qualcomm.com, checked 17 Nov 2026) has the same robots.txt and
// response shape as Microsoft's. The token is "{host}/{domain}".

const eightfoldPageSize = 10

// eightfoldHosts maps each checked host to its domain parameter and company name.
var eightfoldHosts = map[string][2]string{
	"apply.careers.microsoft.com": {"microsoft.com", "Microsoft"},
	"careers.qualcomm.com":        {"qualcomm.com", "Qualcomm"},
}

var eightfoldRe = regexp.MustCompile(`(?i)\b(apply\.careers\.microsoft\.com|careers\.qualcomm\.com)/(?:careers|api/pcsx)`)

func eightfoldToken(m []string) string {
	host := strings.ToLower(m[1])
	return host + "/" + eightfoldHosts[host][0]
}

func eightfoldParts(token string) (host, domain string, err error) {
	host, domain, ok := strings.Cut(token, "/")
	if !ok || host == "" || domain == "" {
		return "", "", fmt.Errorf("bad eightfold token %q (want host/domain)", token)
	}
	return host, domain, nil
}

func eightfoldListURL(host, domain string, start int, location string) string {
	u := "https://" + host + "/api/pcsx/search?domain=" + url.QueryEscape(domain) + "&query=&start=" + strconv.Itoa(start)
	if location != "" {
		u += "&location=" + url.QueryEscape(location)
	}
	return u
}

func isEightfoldList(u *url.URL) bool { return strings.HasSuffix(u.Path, "/api/pcsx/search") }

func parseEightfoldList(body []byte, u *url.URL, country string) (ListPage, error) {
	var out ListPage
	q := u.Query()
	domain, location := q.Get("domain"), q.Get("location")
	start, _ := strconv.Atoi(q.Get("start"))
	if start == 0 && location == "" && q.Get("all") == "" && country != "" {
		out.Refine = eightfoldListURL(u.Host, domain, 0, country)
		out.Note = "listing narrowed to " + country
		return out, nil
	}
	var doc struct {
		Data struct {
			Count     int `json:"count"`
			Positions []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			} `json:"positions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return out, err
	}
	for _, p := range doc.Data.Positions {
		if p.ID == 0 {
			continue
		}
		id := strconv.FormatInt(p.ID, 10)
		out.Postings = append(out.Postings, Posting{
			Title:     strings.TrimSpace(p.Name),
			URL:       "https://" + u.Host + "/careers/job/" + id,
			DetailURL: "https://" + u.Host + "/api/pcsx/position_details?position_id=" + id + "&domain=" + url.QueryEscape(domain) + "&hl=en",
		})
	}
	if len(doc.Data.Positions) > 0 && start+eightfoldPageSize < doc.Data.Count {
		out.Next = eightfoldListURL(u.Host, domain, start+eightfoldPageSize, location)
	}
	return out, nil
}

func parseEightfoldJob(body []byte, u *url.URL) (model.RawJob, error) {
	var doc struct {
		Data struct {
			ID             int64    `json:"id"`
			Name           string   `json:"name"`
			JobDescription string   `json:"jobDescription"`
			Locations      []string `json:"locations"`
			Department     string   `json:"department"`
			PostedTs       int64    `json:"postedTs"`
			PublicURL      string   `json:"publicUrl"`
			WorkOption     string   `json:"workLocationOption"`
			EmploymentType []string `json:"efcustomTextEmploymentType"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return model.RawJob{}, err
	}
	d := doc.Data
	if strings.TrimSpace(d.Name) == "" {
		return model.RawJob{}, fmt.Errorf("no position in the response")
	}
	link := d.PublicURL
	if link == "" {
		link = "https://" + u.Host + "/careers/job/" + u.Query().Get("position_id")
	}
	var locations []string
	for _, l := range d.Locations {
		// "India, Multiple Locations, Multiple Locations" reads better as "India (multiple locations)".
		l = strings.TrimSpace(strings.ReplaceAll(l, ", Multiple Locations", ""))
		if l != "" && !containsString(locations, l) {
			locations = append(locations, l)
		}
	}
	job := model.RawJob{
		URL:         link,
		Title:       strings.TrimSpace(d.Name),
		Location:    strings.Join(locations, " / "),
		Remote:      strings.EqualFold(d.WorkOption, "remote") || looksRemote(locations...),
		Department:  d.Department,
		Description: clip(HTMLToText(d.JobDescription)),
	}
	if len(d.EmploymentType) > 0 {
		job.EmploymentType = d.EmploymentType[0]
	}
	if d.PostedTs > 0 {
		t := time.Unix(d.PostedTs, 0).UTC()
		job.PostedAt = &t
	}
	return job, nil
}
