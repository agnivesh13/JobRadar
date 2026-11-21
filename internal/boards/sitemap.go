package boards

import (
	"bytes"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"

	"jobradar/internal/model"
)

// Career sites whose job list is their sitemap and whose job pages are plain
// HTML. Checked against live pages on 21 Nov 2026:
//
//   - Standard Chartered (jobs.standardchartered.com, SAP SuccessFactors):
//     the search page fills its list through /services/, which robots.txt
//     disallows, but /sitemap.xml lists every job page (1,124, 270 in India).
//     A job page has itemprop="title" and itemprop="description" plus
//     labelled fields ("Job Location:" "Bangalore, IND", "Posting Start
//     Date:" "10/11/2026" day first, "Employment Type:").
//   - Nomura (careers.nomura.com, SuccessFactors): the same layout; /sitemap.xml
//     lists 531 jobs under /Nomura/job/, and the location is in "City:"
//     "Mumbai" and "Country:" "IN" (the site crawl read titles as "Job Title: X").
//   - Swiss Re (careers.swissre.com, SuccessFactors, checked 22 Nov 2026):
//     /sitemap.xml lists 209 jobs under /job/; "Location:" "Bangalore, KA, IN".
//   - Tata AIG (careers.tataaig.com) and Worldline (jobs.worldline.com),
//     SuccessFactors, checked 22 Nov 2026: 67 and 202 jobs. Tata AIG has no
//     location label; like the others it carries schema.org microdata
//     (<meta itemprop="addressLocality|addressCountry|datePosted">), which is
//     the fallback for the location and the first choice for the date
//     ("Thu Nov 12 15:00:00 UTC 2026": unlike the labels, not ambiguous).
//     Worldline's label dates are month first ("10/19/26") and are not read.
//   - MSCI (globalcareers-msci.icims.com, iCIMS, checked 22 Nov 2026): the
//     sitemap lists 83 job pages under /jobs/, each with a JobPosting.
//   - Société Générale (careers.societegenerale.com): /search is disallowed;
//     /sitemap.xml lists 644 job offers under /en/job-offers/, and each page
//     carries a schema.org JobPosting (datePosted "2026/09/24").
//
// The list is one request; each new job is one more. Known jobs are not
// fetched again. The token is the host; only hosts checked live are known.

type sitemapSite struct {
	sitemap string // path of the sitemap
	jobPath string // path prefix of job pages in it
	name    string
}

var sitemapHosts = map[string]sitemapSite{
	"jobs.standardchartered.com":   {sitemap: "/sitemap.xml", jobPath: "/job/", name: "Standard Chartered"},
	"careers.societegenerale.com":  {sitemap: "/sitemap.xml", jobPath: "/en/job-offers/", name: "Société Générale"},
	"careers.nomura.com":           {sitemap: "/sitemap.xml", jobPath: "/Nomura/job/", name: "Nomura"},
	"careers.swissre.com":          {sitemap: "/sitemap.xml", jobPath: "/job/", name: "Swiss Re"},
	"careers.tataaig.com":          {sitemap: "/sitemap.xml", jobPath: "/job/", name: "Tata AIG"},
	"jobs.worldline.com":           {sitemap: "/sitemap.xml", jobPath: "/job/", name: "Worldline"},
	"globalcareers-msci.icims.com": {sitemap: "/sitemap.xml", jobPath: "/jobs/", name: "MSCI"},
}

var sitemapRe = regexp.MustCompile(`(?i)\b(jobs\.standardchartered\.com|careers\.societegenerale\.com|careers\.nomura\.com|careers\.swissre\.com|careers\.tataaig\.com|jobs\.worldline\.com|globalcareers-msci\.icims\.com)(?:/|$)`)

func sitemapFeedURL(host string) (string, error) {
	site, ok := sitemapHosts[host]
	if !ok {
		return "", fmt.Errorf("no sitemap known for %q", host)
	}
	return "https://" + host + site.sitemap, nil
}

func isSitemapList(u *url.URL) bool { return strings.HasSuffix(u.Path, ".xml") }

func parseSitemapList(body []byte, u *url.URL) (ListPage, error) {
	var out ListPage
	site, ok := sitemapHosts[strings.ToLower(u.Host)]
	if !ok {
		return out, fmt.Errorf("no sitemap known for %q", u.Host)
	}
	locs := SitemapURLs(body)
	if len(locs) == 0 {
		return out, fmt.Errorf("the sitemap lists nothing")
	}
	for _, loc := range locs {
		l, err := url.Parse(loc)
		if err != nil || !strings.EqualFold(l.Host, u.Host) || !strings.HasPrefix(l.Path, site.jobPath) || len(l.Path) <= len(site.jobPath) {
			continue
		}
		out.Postings = append(out.Postings, Posting{URL: loc, DetailURL: loc})
	}
	return out, nil
}

var ldJSONRe = regexp.MustCompile(`(?is)<script[^>]*application/ld\+json[^>]*>(.*?)</script>`)

// parseSitemapJob reads a job page: schema.org JobPosting data first, then
// SuccessFactors' labelled fields.
func parseSitemapJob(body []byte, u *url.URL) (model.RawJob, error) {
	for _, m := range ldJSONRe.FindAllSubmatch(body, -1) {
		if jobs := JobPostings(m[1], u); len(jobs) > 0 {
			job := jobs[0]
			job.URL = u.String() // the sitemap's address, so a known job is recognised next time
			job.Description = clip(job.Description)
			return job, nil
		}
	}
	return parseSuccessFactorsJob(body, u)
}

// ISO 3166 codes SuccessFactors gives as the country ("Bangalore, IND"; "IN").
var countryCodes = map[string]string{
	"IND": "India", "SGP": "Singapore", "GBR": "United Kingdom", "USA": "United States", "CHN": "China",
	"HKG": "Hong Kong", "MYS": "Malaysia", "ARE": "United Arab Emirates", "KEN": "Kenya", "PAK": "Pakistan",
	"BGD": "Bangladesh", "POL": "Poland", "KOR": "South Korea", "TWN": "Taiwan", "JPN": "Japan", "VNM": "Vietnam",
	"IN": "India", "SG": "Singapore", "GB": "United Kingdom", "US": "United States", "CN": "China", "HK": "Hong Kong",
	"JP": "Japan", "MY": "Malaysia", "AE": "United Arab Emirates", "FR": "France", "DE": "Germany", "AU": "Australia",
}

func parseSuccessFactorsJob(body []byte, u *url.URL) (model.RawJob, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return model.RawJob{}, err
	}
	var title, desc string
	fields := map[string]string{}
	micro := map[string]string{} // schema.org microdata in <meta itemprop content>
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if prop := attrOf(n, "itemprop"); n.Data == "meta" && prop != "" {
				if _, seen := micro[prop]; !seen {
					micro[prop] = strings.TrimSpace(attrOf(n, "content"))
				}
			}
			switch attrOf(n, "itemprop") {
			case "title":
				if title == "" {
					title = nodeText(n)
				}
			case "description":
				if desc == "" {
					var b bytes.Buffer
					for c := n.FirstChild; c != nil; c = c.NextSibling {
						html.Render(&b, c)
					}
					desc = b.String()
				}
			}
			if strings.Contains(attrOf(n, "class"), "joblayouttoken-label") {
				label := strings.TrimSuffix(strings.TrimSpace(nodeText(n)), ":")
				for s := n.NextSibling; s != nil; s = s.NextSibling {
					if s.Type == html.ElementNode {
						if _, seen := fields[label]; !seen {
							fields[label] = strings.TrimSpace(nodeText(s))
						}
						break
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(doc)
	if title == "" {
		title = fields["Job Title"]
	}
	if title == "" {
		return model.RawJob{}, fmt.Errorf("no job data on the page")
	}
	location := fields["Job Location"]
	if location == "" {
		location = fields["Location"]
	}
	if location == "" {
		var parts []string
		for _, key := range []string{"City", "Country"} {
			if v := fields[key]; v != "" {
				parts = append(parts, v)
			}
		}
		location = strings.Join(parts, ", ")
	}
	if location == "" {
		var parts []string
		for _, key := range []string{"addressLocality", "addressCountry"} {
			if v := micro[key]; v != "" {
				parts = append(parts, v)
			}
		}
		location = strings.Join(parts, ", ")
	}
	if i := strings.LastIndex(location, ", "); i >= 0 {
		location = location[:i] + ", " + spellCountry(location[i+2:])
	}
	job := model.RawJob{
		URL:            u.String(),
		Title:          title,
		Location:       location,
		Remote:         looksRemote(location),
		EmploymentType: fields["Employment Type"],
		Description:    clip(HTMLToText(desc)),
	}
	if t, err := time.Parse(time.UnixDate, micro["datePosted"]); err == nil {
		t = t.UTC()
		job.PostedAt = &t
	}
	for _, key := range []string{"Posting Start Date", "Date"} {
		if job.PostedAt != nil {
			break
		}
		// Checked on Standard Chartered (en_GB): day first.
		if t, err := time.Parse("02/01/2006", fields[key]); err == nil {
			job.PostedAt = &t
			break
		}
	}
	return job, nil
}

// spellCountry spells out an ISO country code; anything else is returned as is.
func spellCountry(code string) string {
	code = strings.TrimSpace(code)
	if name, ok := countryCodes[strings.ToUpper(code)]; ok && len(code) <= 3 {
		return name
	}
	return code
}

func attrOf(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}
