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

// SmartRecruiters (https://careers.smartrecruiters.com/{Company}). Checked
// against live responses on 17 Nov 2026:
//
//	List:   GET https://careers.smartrecruiters.com/{Company}/api/more?page=N[&search=text]
//	        -> an HTML fragment: <li class="opening-job"><a href="https://jobs.smartrecruiters.com/{Company}/{id}-{slug}"><h4>title</h4>...
//	Detail: GET https://jobs.smartrecruiters.com/{Company}/{id}-{slug}
//	        -> the public posting page, marked up as schema.org JobPosting microdata
//
// Notes from the live check:
//   - The JSON API (api.smartrecruiters.com) disallows every crawler but one in
//     its robots.txt, so it is NOT used. The public careers pages have no
//     robots.txt (404 / a redirect to the home page), and these two requests
//     are the ones the careers page itself makes.
//   - Pages start at 0 (49 postings, then 24 per page, no overlap); a page past
//     the end is an empty body. There is no total.
//   - "search" is a text search that also matches the location, so it is used
//     to narrow a crawl to one country. location= and country= are ignored.
//   - The list has no location or description: one more request per posting.
//   - The posting page answers by the Accept header: JSON when JSON is
//     preferred (what the crawler sends), the HTML page otherwise. Both are read.
//     JSON: {jobTitle,jobAdLocation,employmentType,postedDate,content:{sections:{companyDescription,
//     jobDescription,qualifications,additionalInformation:{title,text (HTML)}}}}
//   - An unknown company answers 404. The company name is not case sensitive.
//   - Some companies redirect the careers page to their own domain; the
//     api/more address still answers.

var (
	smartRecruitersRe = regexp.MustCompile(`(?i)\b(?:jobs|careers)\.smartrecruiters\.com/([\w.-]+)`)
	srPostingRe       = regexp.MustCompile(`(?s)<a href="(https://jobs\.smartrecruiters\.com/[^"?#]+)"[^>]*>\s*<h4[^>]*>(.*?)</h4>`)
	srTitleRe         = regexp.MustCompile(`(?s)<h1[^>]*itemprop="title"[^>]*>(.*?)</h1>`)
	srMetaRe          = regexp.MustCompile(`<meta itemprop="(addressLocality|addressRegion|addressCountry|datePosted)" content="([^"]*)"`)
	srTypeRe          = regexp.MustCompile(`(?s)itemprop="employmentType"[^>]*>(.*?)<`)
	srSectionRe       = regexp.MustCompile(`(?s)<section class="job-section" id="st-[^"]*">(.*?)</section>`)
)

func smartRecruitersListURL(company string, page int, query string) string {
	u := "https://careers.smartrecruiters.com/" + url.PathEscape(company) + "/api/more?page=" + strconv.Itoa(page)
	if query != "" {
		u += "&" + query
	}
	return u
}

func isSmartRecruitersList(u *url.URL) bool {
	return strings.EqualFold(u.Host, "careers.smartrecruiters.com") && strings.HasSuffix(u.Path, "/api/more")
}

func parseSmartRecruitersList(body []byte, u *url.URL, country string) (ListPage, error) {
	var out ListPage
	company := strings.Split(strings.Trim(u.Path, "/"), "/")[0]
	q := u.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	search, all := q.Get("search"), q.Get("all")
	if page == 0 && search == "" && all == "" && country != "" {
		out.Refine = smartRecruitersListURL(company, 0, "search="+url.QueryEscape(country))
		out.Note = "listing narrowed to " + country
		return out, nil
	}
	for _, m := range srPostingRe.FindAllStringSubmatch(string(body), -1) {
		out.Postings = append(out.Postings, Posting{Title: strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(m[2], ""))), URL: m[1], DetailURL: m[1]})
	}
	switch {
	case len(out.Postings) > 0:
		rest := "all=1"
		if search != "" {
			rest = "search=" + url.QueryEscape(search)
		} else if all == "" {
			rest = ""
		}
		out.Next = smartRecruitersListURL(company, page+1, rest)
	case page == 0 && search != "":
		// The country search found nothing (the company may write its
		// locations differently): read the whole list instead.
		out.Refine = smartRecruitersListURL(company, 0, "all=1")
		out.Note = "nothing found for " + search + "; reading every posting"
	}
	return out, nil
}

func parseSmartRecruitersJob(body []byte, u *url.URL) (model.RawJob, error) {
	if strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
		return parseSmartRecruitersJobJSON(body, u)
	}
	s := string(body)
	m := srTitleRe.FindStringSubmatch(s)
	if m == nil {
		return model.RawJob{}, fmt.Errorf("no job title on the page")
	}
	meta := map[string]string{}
	for _, x := range srMetaRe.FindAllStringSubmatch(s, -1) {
		if _, seen := meta[x[1]]; !seen {
			meta[x[1]] = html.UnescapeString(x[2])
		}
	}
	var desc []string
	for _, x := range srSectionRe.FindAllStringSubmatch(s, -1) {
		desc = append(desc, HTMLToText(x[1]))
	}
	job := model.RawJob{
		URL:         u.Scheme + "://" + u.Host + u.Path,
		Title:       strings.TrimSpace(html.UnescapeString(tagRe.ReplaceAllString(m[1], ""))),
		Location:    joinNonEmpty(", ", meta["addressLocality"], meta["addressRegion"], meta["addressCountry"]),
		Remote:      strings.Contains(s, `workplaceType="remote"`),
		Description: clip(strings.Join(desc, "\n")),
	}
	if t := srTypeRe.FindStringSubmatch(s); t != nil {
		job.EmploymentType = strings.TrimSpace(html.UnescapeString(t[1]))
	}
	if t, err := time.Parse(time.RFC3339, meta["datePosted"]); err == nil {
		job.PostedAt = &t
	}
	return job, nil
}

func parseSmartRecruitersJobJSON(body []byte, u *url.URL) (model.RawJob, error) {
	type section struct {
		Title string `json:"title"`
		Text  string `json:"text"`
	}
	var doc struct {
		Title          string `json:"jobTitle"`
		Location       string `json:"jobAdLocation"`
		EmploymentType string `json:"employmentType"`
		PostedDate     string `json:"postedDate"`
		Content        struct {
			Sections struct {
				Company        section `json:"companyDescription"`
				Job            section `json:"jobDescription"`
				Qualifications section `json:"qualifications"`
				Additional     section `json:"additionalInformation"`
			} `json:"sections"`
		} `json:"content"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return model.RawJob{}, err
	}
	if strings.TrimSpace(doc.Title) == "" {
		return model.RawJob{}, fmt.Errorf("no job title in the response")
	}
	// The job itself first: the company blurb is the same on every posting.
	sec := doc.Content.Sections
	var desc []string
	for _, s := range []section{sec.Job, sec.Qualifications, sec.Additional, sec.Company} {
		if text := HTMLToText(s.Text); text != "" {
			desc = append(desc, joinNonEmpty("\n", s.Title, text))
		}
	}
	job := model.RawJob{
		URL:            u.Scheme + "://" + u.Host + u.Path,
		Title:          strings.TrimSpace(doc.Title),
		Location:       strings.TrimSpace(doc.Location),
		Remote:         looksRemote(doc.Location),
		EmploymentType: doc.EmploymentType,
		Description:    clip(strings.Join(desc, "\n")),
	}
	if t, err := time.Parse(time.RFC3339, doc.PostedDate); err == nil {
		job.PostedAt = &t
	}
	return job, nil
}
