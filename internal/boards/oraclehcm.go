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

// Oracle Recruiting Cloud ("Candidate Experience"), the careers sites at
// https://{pod}.fa.{dc}.oraclecloud.com/hcmUI/CandidateExperience/{lang}/sites/{site}/.
// Used by Oracle itself (eeho.fa.us2, site CX_45001, behind
// careers.oracle.com). Checked against live responses on 17 Nov 2026:
//
//	List:   GET https://{host}/hcmRestApi/resources/latest/recruitingCEJobRequisitions?onlyData=true
//	            &expand=requisitionList.secondaryLocations,locationsFacet&finder=findReqs;siteNumber={site},limit=25,offset=N,sortBy=POSTING_DATES_DESC
//	            [,facetsList=LOCATIONS][,locationId={id}]
//	        -> {"items":[{"TotalJobsCount":N,"requisitionList":[{Id,Title,PostedDate,PrimaryLocation}],
//	            "locationsFacet":[{Id,Name:"India",TotalCount}]}]}
//	Detail: GET https://{host}/hcmRestApi/resources/latest/recruitingCEJobRequisitionDetails?expand=all&onlyData=true
//	            &finder=ById;Id="{id}",siteNumber={site}
//	        -> {"items":[{Title,ExternalDescriptionStr,ExternalResponsibilitiesStr,ExternalQualificationsStr,
//	            PrimaryLocation,Category,ExternalPostedStartDate,WorkplaceType}]}
//
// Notes from the live check:
//   - These are the requests the public careers page makes. The host serves no
//     robots.txt (404), which allows everything.
//   - The finder's arguments are separated by ";" and ",", which Go's
//     url.Query() drops, so the finder is read from the raw query.
//   - keyword=India is a text search (47 hits); the LOCATIONS facet has a
//     country entry ("India", 328 postings) whose Id is the real filter.
//   - Without expand=requisitionList,... the response has no postings and
//     an empty facet: a silent empty list (found in the first live crawl).
//   - The list has only a short description: one more request per posting.
//   - JPMorgan Chase's host (jpmc.fa.oraclecloud.com) answers the crawler with
//     "W4S-402: Blocked by WAF4SaaS", so that company is manual.
//
// The token is "{host}/{site}".

const oraclePageSize = 25

var oracleRe = regexp.MustCompile(`(?i)\b([a-z0-9-]+\.fa\.[a-z0-9-]+\.oraclecloud\.com)/hcmUI/CandidateExperience/[a-z-]+/sites/([\w-]+)`)

func oracleToken(m []string) string { return strings.ToLower(m[1]) + "/" + m[2] }

func oracleParts(token string) (host, site string, err error) {
	host, site, ok := strings.Cut(token, "/")
	if !ok || host == "" || site == "" {
		return "", "", fmt.Errorf("bad oracle token %q (want host/site)", token)
	}
	return host, site, nil
}

const oracleAPI = "/hcmRestApi/resources/latest/"

func oracleListURL(host, site string, offset int, locationID string) string {
	f := "findReqs;siteNumber=" + site + ",limit=" + strconv.Itoa(oraclePageSize) + ",offset=" + strconv.Itoa(offset) + ",sortBy=POSTING_DATES_DESC"
	if locationID != "" {
		f += ",locationId=" + locationID
	} else if offset == 0 {
		f += ",facetsList=LOCATIONS"
	}
	return "https://" + host + oracleAPI + "recruitingCEJobRequisitions?onlyData=true&expand=requisitionList.secondaryLocations,locationsFacet&finder=" + f
}

// oracleFinder reads the finder's arguments from a raw query.
func oracleFinder(u *url.URL) map[string]string {
	args := map[string]string{}
	for _, part := range strings.Split(u.RawQuery, "&") {
		v, ok := strings.CutPrefix(part, "finder=")
		if !ok {
			continue
		}
		if s, err := url.PathUnescape(v); err == nil {
			v = s
		}
		_, v, _ = strings.Cut(v, ";")
		for _, kv := range strings.Split(v, ",") {
			if k, val, ok := strings.Cut(kv, "="); ok {
				args[k] = strings.Trim(val, `"`)
			}
		}
	}
	return args
}

func isOracleList(u *url.URL) bool {
	return strings.HasSuffix(u.Path, oracleAPI+"recruitingCEJobRequisitions")
}

func parseOracleList(body []byte, u *url.URL, country string) (ListPage, error) {
	var out ListPage
	f := oracleFinder(u)
	site, locationID := f["siteNumber"], f["locationId"]
	offset, _ := strconv.Atoi(f["offset"])
	var doc struct {
		Items []struct {
			Total int `json:"TotalJobsCount"`
			Reqs  []struct {
				ID    string `json:"Id"`
				Title string `json:"Title"`
			} `json:"requisitionList"`
			Locations []struct {
				ID   int64  `json:"Id"`
				Name string `json:"Name"`
			} `json:"locationsFacet"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return out, err
	}
	if len(doc.Items) == 0 {
		return out, fmt.Errorf("no search result in the response")
	}
	it := doc.Items[0]
	if offset == 0 && locationID == "" && country != "" {
		for _, l := range it.Locations {
			if strings.EqualFold(strings.TrimSpace(l.Name), strings.TrimSpace(country)) {
				out.Refine = oracleListURL(u.Host, site, 0, strconv.FormatInt(l.ID, 10))
				out.Note = "listing narrowed to " + country
				return out, nil
			}
		}
		// No such country: this page already is the whole list.
	}
	if len(it.Reqs) == 0 && it.Total > offset {
		return out, fmt.Errorf("the search reports %d postings but the page holds none", it.Total)
	}
	for _, r := range it.Reqs {
		if r.ID == "" || strings.TrimSpace(r.Title) == "" {
			continue
		}
		out.Postings = append(out.Postings, Posting{
			Title:     strings.TrimSpace(r.Title),
			URL:       "https://" + u.Host + "/hcmUI/CandidateExperience/en/sites/" + site + "/job/" + url.PathEscape(r.ID),
			DetailURL: "https://" + u.Host + oracleAPI + "recruitingCEJobRequisitionDetails?expand=all&onlyData=true&finder=ById;Id=%22" + url.QueryEscape(r.ID) + "%22,siteNumber=" + site,
		})
	}
	if len(it.Reqs) > 0 && offset+oraclePageSize < it.Total {
		out.Next = oracleListURL(u.Host, site, offset+oraclePageSize, locationID)
	}
	return out, nil
}

func parseOracleJob(body []byte, u *url.URL) (model.RawJob, error) {
	var doc struct {
		Items []struct {
			ID               string `json:"Id"`
			Title            string `json:"Title"`
			Category         string `json:"Category"`
			Description      string `json:"ExternalDescriptionStr"`
			Responsibilities string `json:"ExternalResponsibilitiesStr"`
			Qualifications   string `json:"ExternalQualificationsStr"`
			PrimaryLocation  string `json:"PrimaryLocation"`
			PostedStart      string `json:"ExternalPostedStartDate"`
			WorkplaceType    string `json:"WorkplaceType"`
			JobSchedule      string `json:"JobSchedule"`
			SecondaryLocs    []struct {
				Name string `json:"Name"`
			} `json:"secondaryLocations"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return model.RawJob{}, err
	}
	if len(doc.Items) == 0 || strings.TrimSpace(doc.Items[0].Title) == "" {
		return model.RawJob{}, fmt.Errorf("no requisition in the response")
	}
	d := doc.Items[0]
	f := oracleFinder(u)
	id := firstNonEmpty(d.ID, f["Id"])
	locations := []string{strings.TrimSpace(d.PrimaryLocation)}
	for _, l := range d.SecondaryLocs {
		if l.Name = strings.TrimSpace(l.Name); l.Name != "" && !containsString(locations, l.Name) {
			locations = append(locations, l.Name)
		}
	}
	var desc []string
	for _, s := range [][2]string{{"", d.Description}, {"Responsibilities", d.Responsibilities}, {"Qualifications", d.Qualifications}} {
		if text := HTMLToText(s[1]); text != "" {
			desc = append(desc, joinNonEmpty("\n", s[0], text))
		}
	}
	job := model.RawJob{
		URL:            "https://" + u.Host + "/hcmUI/CandidateExperience/en/sites/" + f["siteNumber"] + "/job/" + url.PathEscape(id),
		Title:          strings.TrimSpace(d.Title),
		Location:       strings.Join(locations, " / "),
		Department:     d.Category,
		EmploymentType: d.JobSchedule,
		Description:    clip(strings.Join(desc, "\n")),
	}
	job.Remote = strings.Contains(strings.ToLower(d.WorkplaceType), "remote") || looksRemote(locations...)
	if t, err := time.Parse(time.RFC3339, d.PostedStart); err == nil {
		job.PostedAt = &t
	}
	return job, nil
}
