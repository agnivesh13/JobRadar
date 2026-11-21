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
//
// Some tenants have PCSX turned off ("PCSX is not enabled for this user") and
// serve the older API instead. HSBC (hsbc.eightfold.ai), checked 21 Nov 2026:
//
//	List:   GET https://{host}/api/apply/v2/jobs?domain={domain}&start=N&num=10[&location=India]
//	        -> {"count":N,"positions":[{id,name,location,locations,department,t_create,canonicalPositionUrl}]}
//	Detail: GET https://{host}/api/apply/v2/jobs/{id}?domain={domain}
//	        -> {id,name,job_description (HTML),location,locations,department,t_create,
//	            canonicalPositionUrl,work_location_option}
//
// Its robots.txt disallows "/" but allows /careers and /api/apply.

const eightfoldPageSize = 10

type eightfoldSite struct {
	domain, name string
	applyV2      bool // the older /api/apply/v2 API instead of PCSX
}

// eightfoldHosts maps each checked host to its domain parameter and company name.
var eightfoldHosts = map[string]eightfoldSite{
	"apply.careers.microsoft.com": {domain: "microsoft.com", name: "Microsoft"},
	"careers.qualcomm.com":        {domain: "qualcomm.com", name: "Qualcomm"},
	"hsbc.eightfold.ai":           {domain: "hsbc.com", name: "HSBC", applyV2: true},
	"mlp.eightfold.ai":            {domain: "mlp.com", name: "Millennium", applyV2: true}, // checked 22 Nov 2026
}

var eightfoldRe = regexp.MustCompile(`(?i)\b(apply\.careers\.microsoft\.com|careers\.qualcomm\.com|hsbc\.eightfold\.ai|mlp\.eightfold\.ai)/(?:careers|api/pcsx|api/apply)`)

func eightfoldToken(m []string) string {
	host := strings.ToLower(m[1])
	return host + "/" + eightfoldHosts[host].domain
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
	if eightfoldHosts[host].applyV2 {
		u = "https://" + host + "/api/apply/v2/jobs?domain=" + url.QueryEscape(domain) + "&start=" + strconv.Itoa(start) +
			"&num=" + strconv.Itoa(eightfoldPageSize)
	}
	if location != "" {
		u += "&location=" + url.QueryEscape(location)
	}
	return u
}

func isEightfoldList(u *url.URL) bool {
	return strings.HasSuffix(u.Path, "/api/pcsx/search") || strings.HasSuffix(u.Path, "/api/apply/v2/jobs")
}

// isApplyV2 tells the older API's URLs apart from PCSX ones.
func isApplyV2(u *url.URL) bool { return strings.Contains(u.Path, "/api/apply/v2/jobs") }

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
	type position struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
		URL  string `json:"canonicalPositionUrl"` // older API only
	}
	var list struct {
		Count     int        `json:"count"`
		Positions []position `json:"positions"`
	}
	v2 := isApplyV2(u)
	if v2 {
		if err := json.Unmarshal(body, &list); err != nil {
			return out, err
		}
	} else {
		var doc struct {
			Data struct {
				Count     int        `json:"count"`
				Positions []position `json:"positions"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			return out, err
		}
		list.Count, list.Positions = doc.Data.Count, doc.Data.Positions
	}
	if list.Count > start && len(list.Positions) == 0 {
		return out, fmt.Errorf("the site reports %d positions but this page holds none", list.Count)
	}
	for _, p := range list.Positions {
		if p.ID == 0 {
			continue
		}
		id := strconv.FormatInt(p.ID, 10)
		post := Posting{
			Title:     strings.TrimSpace(p.Name),
			URL:       "https://" + u.Host + "/careers/job/" + id,
			DetailURL: "https://" + u.Host + "/api/pcsx/position_details?position_id=" + id + "&domain=" + url.QueryEscape(domain) + "&hl=en",
		}
		if v2 {
			post.DetailURL = "https://" + u.Host + "/api/apply/v2/jobs/" + id + "?domain=" + url.QueryEscape(domain)
			if p.URL != "" {
				post.URL = p.URL
			}
		}
		out.Postings = append(out.Postings, post)
	}
	if len(list.Positions) > 0 && start+eightfoldPageSize < list.Count {
		out.Next = eightfoldListURL(u.Host, domain, start+eightfoldPageSize, location)
	}
	return out, nil
}

func parseEightfoldJob(body []byte, u *url.URL) (model.RawJob, error) {
	if isApplyV2(u) {
		return parseEightfoldApplyJob(body, u)
	}
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

// parseEightfoldApplyJob reads one position from the older /api/apply/v2 API.
func parseEightfoldApplyJob(body []byte, u *url.URL) (model.RawJob, error) {
	var d struct {
		ID          int64    `json:"id"`
		Name        string   `json:"name"`
		Description string   `json:"job_description"`
		Location    string   `json:"location"`
		Locations   []string `json:"locations"`
		Department  string   `json:"department"`
		Created     int64    `json:"t_create"`
		URL         string   `json:"canonicalPositionUrl"`
		WorkOption  string   `json:"work_location_option"`
	}
	if err := json.Unmarshal(body, &d); err != nil {
		return model.RawJob{}, err
	}
	if strings.TrimSpace(d.Name) == "" {
		return model.RawJob{}, fmt.Errorf("no position in the response")
	}
	link := d.URL
	if link == "" {
		link = "https://" + u.Host + "/careers/job/" + strconv.FormatInt(d.ID, 10)
	}
	var locations []string
	for _, l := range append(d.Locations, d.Location) {
		if l = strings.TrimSpace(l); l != "" && !containsString(locations, l) {
			locations = append(locations, l)
		}
	}
	job := model.RawJob{
		URL:         link,
		Title:       strings.TrimSpace(d.Name),
		Location:    strings.Join(locations, " / "),
		Remote:      strings.EqualFold(d.WorkOption, "remote") || looksRemote(locations...),
		Department:  d.Department,
		Description: clip(HTMLToText(d.Description)),
	}
	if d.Created > 0 {
		t := time.Unix(d.Created, 0).UTC()
		job.PostedAt = &t
	}
	return job, nil
}
