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

// Workday (https://{tenant}.wd{N}.myworkdayjobs.com/{site}) has no single
// feed. Checked against live responses on 17 Nov 2026:
//
//	List:   POST https://{host}/wday/cxs/{tenant}/{site}/jobs
//	        body {"appliedFacets":{},"limit":20,"offset":0,"searchText":""}
//	        -> {"total":N,"jobPostings":[{title,externalPath,locationsText,...}],"facets":[...]}
//	Detail: GET  https://{host}/wday/cxs/{tenant}/{site}{externalPath}
//	        -> {"jobPostingInfo":{title,jobDescription (HTML),location,additionalLocations,startDate,timeType,...}}
//
// Notes from the live check:
//   - "limit" above 20 is answered with HTTP 400.
//   - "total" is only filled in on the first page (offset 0); later pages say 0,
//     so the total travels in the page URL ("n").
//   - "total" is capped at 2000, and an offset past the end wraps around to
//     the first page, so paging must stop at the total, not at an empty page.
//   - The list carries no description, so each posting costs one more request.
//   - robots.txt of these hosts allows the site path and does not disallow /wday/cxs/.
//
// A crawl therefore runs through the same frontier as a whole-site crawl:
// list pages claim detail pages. The company token is "host/tenant/site".
//
// Not covered: the older "wd1.myworkdaysite.com/recruiting/{tenant}/{site}"
// address form (not checked against a live response).

const (
	workdayPageSize = 20
	workdayMaxTotal = 2000
	// WorkdayMaxPages bounds one crawl: 100 list pages plus 2000 postings.
	WorkdayMaxPages = 2200
)

var workdayRe = regexp.MustCompile(`(?i)\b([a-z0-9-]+)\.(wd\d+)\.myworkdayjobs\.com/(?:wday/cxs/[\w-]+/|[a-z]{2}-[a-z]{2}/)?([\w-]+)`)

func workdayToken(m []string) string {
	tenant := strings.ToLower(m[1])
	return tenant + "." + strings.ToLower(m[2]) + ".myworkdayjobs.com/" + tenant + "/" + m[3]
}

// workdayBase returns "https://host/wday/cxs/tenant/site" for a token.
func workdayBase(token string) (string, error) {
	p := strings.Split(token, "/")
	if len(p) != 3 || p[0] == "" || p[1] == "" || p[2] == "" {
		return "", fmt.Errorf("bad workday token %q (want host/tenant/site)", token)
	}
	return "https://" + p[0] + "/wday/cxs/" + url.PathEscape(p[1]) + "/" + url.PathEscape(p[2]), nil
}

// DisplayName is a readable company name for a board token.
func DisplayName(boardType, token string) string {
	switch boardType {
	case model.BoardWorkday:
		if p := strings.Split(token, "/"); len(p) == 3 {
			return p[1]
		}
	case model.BoardDarwinbox:
		return strings.SplitN(token, ".", 2)[0]
	case model.BoardAmazon:
		return "Amazon"
	case model.BoardApple:
		return "Apple"
	case model.BoardEightfold:
		if strings.HasSuffix(token, "/microsoft.com") {
			return "Microsoft"
		}
		_, domain, _ := strings.Cut(token, "/")
		return strings.SplitN(domain, ".", 2)[0]
	}
	return token
}

// workdayParts splits a task URL into its site base, the path below it and the query.
func workdayParts(taskURL string) (u *url.URL, base, rest string, ok bool) {
	u, err := url.Parse(taskURL)
	if err != nil {
		return nil, "", "", false
	}
	seg := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(seg) < 5 || seg[0] != "wday" || seg[1] != "cxs" {
		return nil, "", "", false
	}
	base = u.Scheme + "://" + u.Host + "/" + strings.Join(seg[:4], "/")
	return u, base, "/" + strings.Join(seg[4:], "/"), true
}

// IsWorkdayList reports whether a task URL is a list page (as opposed to one posting).
func IsWorkdayList(taskURL string) bool {
	_, _, rest, ok := workdayParts(taskURL)
	return ok && rest == "/jobs"
}

// workdayListURL names one list page. The offset and the facet filter live in
// the query string so every page is a distinct URL in the crawl frontier; the
// fetcher turns them into the POST body (see WorkdayRequest).
func workdayListURL(base string, offset, total int, facet string) string {
	q := "?offset=" + strconv.Itoa(offset)
	if offset > 0 {
		q += "&n=" + strconv.Itoa(total) // only the first page reports the total, so later pages carry it
	}
	if facet != "" {
		q += "&f=" + url.QueryEscape(facet)
	}
	return base + "/jobs" + q
}

// WorkdayRequest turns a list-page task URL into the POST it stands for.
// ok is false for anything else (a posting is a plain GET of its URL).
func WorkdayRequest(taskURL string) (endpoint string, body []byte, ok bool) {
	u, base, rest, isWD := workdayParts(taskURL)
	if !isWD || rest != "/jobs" {
		return "", nil, false
	}
	offset, _ := strconv.Atoi(u.Query().Get("offset"))
	facets := map[string][]string{}
	if param, ids, found := strings.Cut(u.Query().Get("f"), ":"); found && param != "" {
		facets[param] = strings.Split(ids, ",")
	}
	body, _ = json.Marshal(map[string]any{"appliedFacets": facets, "limit": workdayPageSize, "offset": offset, "searchText": ""})
	return base + "/jobs", body, true
}

type workdayFacet struct {
	FacetParameter string         `json:"facetParameter"`
	Descriptor     string         `json:"descriptor"`
	ID             string         `json:"id"`
	Values         []workdayFacet `json:"values"`
}

// WorkdayPosting is one row of a list page.
type WorkdayPosting struct {
	Title     string
	URL       string // public page of the posting
	DetailURL string // JSON detail to fetch
}

// WorkdayList is what one list page yields.
type WorkdayList struct {
	Postings []WorkdayPosting
	Next     string // the list page after this one ("" on the last)
	// Refine is set instead of everything else when the unfiltered first page
	// offers a location filter for the wanted country: crawl that list instead.
	Refine string
	// Note says why Refine was chosen, for the live view.
	Note string
	// Jobs are complete postings read from the list itself, on boards whose
	// list carries the description (Amazon); they need no posting page.
	Jobs []model.RawJob
}

// ParseWorkdayList reads one list page. country ("" = no filter) narrows the
// crawl to one country when the site offers a matching location filter.
func ParseWorkdayList(body []byte, taskURL, country string) (WorkdayList, error) {
	var out WorkdayList
	u, base, rest, ok := workdayParts(taskURL)
	if !ok || rest != "/jobs" {
		return out, fmt.Errorf("not a workday list url: %s", taskURL)
	}
	var doc struct {
		Total       int `json:"total"`
		JobPostings []struct {
			Title        string `json:"title"`
			ExternalPath string `json:"externalPath"`
		} `json:"jobPostings"`
		Facets []workdayFacet `json:"facets"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return out, err
	}
	offset, _ := strconv.Atoi(u.Query().Get("offset"))
	facet := u.Query().Get("f")
	if offset == 0 && facet == "" && country != "" {
		if f := workdayCountryFacet(doc.Facets, country); f != "" {
			out.Refine = workdayListURL(base, 0, 0, f)
			return out, nil
		}
	}
	public := workdayPublicBase(base)
	for _, p := range doc.JobPostings {
		if p.ExternalPath == "" || strings.TrimSpace(p.Title) == "" {
			continue
		}
		out.Postings = append(out.Postings, WorkdayPosting{Title: strings.TrimSpace(p.Title), URL: public + p.ExternalPath, DetailURL: base + p.ExternalPath})
	}
	// Pages are chained (each names only the one after it) rather than all
	// claimed at once: a 2000-posting site then never has more than one page
	// of work in the queue, and other companies are fetched in between.
	total := doc.Total
	if offset > 0 {
		total, _ = strconv.Atoi(u.Query().Get("n"))
	}
	if next := offset + workdayPageSize; next < min(total, workdayMaxTotal) {
		out.Next = workdayListURL(base, next, total, facet)
	}
	return out, nil
}

// workdayPublicBase maps "https://host/wday/cxs/tenant/site" to "https://host/site".
func workdayPublicBase(base string) string {
	i := strings.Index(base, "/wday/cxs/")
	return base[:i] + base[strings.LastIndex(base, "/"):]
}

// workdayCountryFacet finds the location filter for a country and returns it
// as "parameter:id[,id...]". Sites name their location filters differently:
// some have a country level ("India"), others only list sites ("India,
// Bengaluru", "India Remote", "Bangalore, India"), in which case every site
// that names the country is selected.
func workdayCountryFacet(facets []workdayFacet, country string) string {
	want := strings.ToLower(strings.TrimSpace(country))
	var exact, prefix string
	var walk func(fs []workdayFacet)
	walk = func(fs []workdayFacet) {
		for _, f := range fs {
			var ids []string
			var nested []workdayFacet
			for _, v := range f.Values {
				if v.FacetParameter != "" {
					nested = append(nested, v)
					continue
				}
				d := strings.ToLower(strings.TrimSpace(v.Descriptor))
				switch {
				case v.ID == "" || f.FacetParameter == "":
				case d == want:
					if exact == "" {
						exact = f.FacetParameter + ":" + v.ID
					}
				case hasWord(d, want):
					ids = append(ids, v.ID)
				}
			}
			if prefix == "" && len(ids) > 0 {
				prefix = f.FacetParameter + ":" + strings.Join(ids, ",")
			}
			walk(nested)
		}
	}
	walk(facets)
	if exact != "" {
		return exact
	}
	return prefix
}

func isLetter(b byte) bool { return b >= 'a' && b <= 'z' }

// hasWord reports whether s contains word as a whole word: "bangalore, india"
// and "india remote" name India; "indianapolis, indiana, us" does not.
func hasWord(s, word string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], word)
		if i < 0 {
			return false
		}
		start, end := from+i, from+i+len(word)
		if (start == 0 || !isLetter(s[start-1])) && (end == len(s) || !isLetter(s[end])) {
			return true
		}
		from = end
	}
}

// ParseWorkdayJob reads one posting's detail document.
func ParseWorkdayJob(body []byte, taskURL string) (model.RawJob, error) {
	var job model.RawJob
	_, base, rest, ok := workdayParts(taskURL)
	if !ok {
		return job, fmt.Errorf("not a workday posting url: %s", taskURL)
	}
	var doc struct {
		Info struct {
			Title               string   `json:"title"`
			JobDescription      string   `json:"jobDescription"`
			Location            string   `json:"location"`
			AdditionalLocations []string `json:"additionalLocations"`
			StartDate           string   `json:"startDate"`
			TimeType            string   `json:"timeType"`
			RemoteType          string   `json:"remoteType"`
		} `json:"jobPostingInfo"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return job, err
	}
	in := doc.Info
	if strings.TrimSpace(in.Title) == "" {
		return job, fmt.Errorf("posting has no title")
	}
	location := strings.TrimSpace(in.Location)
	for _, l := range in.AdditionalLocations {
		if l = strings.TrimSpace(l); l != "" {
			location += " / " + l
		}
	}
	job = model.RawJob{
		URL:            workdayPublicBase(base) + rest,
		Title:          strings.TrimSpace(in.Title),
		Location:       strings.TrimPrefix(location, " / "),
		Remote:         looksRemote(location, in.RemoteType),
		EmploymentType: in.TimeType,
		Description:    clip(HTMLToText(in.JobDescription)),
	}
	if t, err := time.Parse("2006-01-02", in.StartDate); err == nil {
		job.PostedAt = &t
	}
	return job, nil
}

// ---- finding the career sites of a tenant ---------------------------------
//
// A bare host such as https://cisco.wd5.myworkdayjobs.com/ answers 406: the
// site name is required. Checked live on 17 Nov 2026: the host's robots.txt
// names every public career site in its "Sitemap:" and "Allow:" lines, e.g.
//
//	Sitemap: https://lseg.wd3.myworkdayjobs.com/Careers/siteMap.xml
//	Sitemap: https://lseg.wd3.myworkdayjobs.com/Graduate_Careers/siteMap.xml
//
// A tenant lives on one data centre ("wd3"); the same robots.txt on any other
// number answers 422. People often quote the wrong number, so the others are tried.

var (
	workdayHostRe     = regexp.MustCompile(`(?i)^(?:https?://)?([a-z0-9-]+)\.wd(\d+)\.myworkdayjobs\.com/?$`)
	workdaySiteLineRe = regexp.MustCompile(`(?im)^[ \t]*(?:Sitemap:[ \t]*https?://[^/\s]+|Allow:[ \t]*)/([\w-]+)/(?:siteMap\.xml)?[ \t]*\r?$`)
)

// WorkdayHosts recognises a Workday host given without a site name and
// returns the hosts to ask for robots.txt: the one given first, then the
// tenant on the other common data centres.
func WorkdayHosts(input string) (tenant string, hosts []string, ok bool) {
	m := workdayHostRe.FindStringSubmatch(strings.TrimSpace(input))
	if m == nil {
		return "", nil, false
	}
	tenant = strings.ToLower(m[1])
	for _, n := range []string{m[2], "1", "3", "5", "12", "10", "103"} {
		h := tenant + ".wd" + n + ".myworkdayjobs.com"
		if len(hosts) == 0 || !containsString(hosts, h) {
			hosts = append(hosts, h)
		}
	}
	return tenant, hosts, true
}

// WorkdaySites lists the career sites named in a Workday host's robots.txt.
func WorkdaySites(robots []byte) []string {
	var sites []string
	for _, m := range workdaySiteLineRe.FindAllStringSubmatch(string(robots), -1) {
		if !containsString(sites, m[1]) && !reserved[strings.ToLower(m[1])] {
			sites = append(sites, m[1])
		}
	}
	return sites
}

// WorkdayToken builds the company token for one site of a tenant.
func WorkdayToken(host, tenant, site string) string { return host + "/" + tenant + "/" + site }

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
