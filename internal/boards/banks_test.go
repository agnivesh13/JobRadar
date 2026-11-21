package boards

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"jobradar/internal/model"
)

// Fixtures are real responses saved on 21 Nov 2026 from the URLs the code
// builds, trimmed: the Phenom ones to the fields read (the full detail carries
// recruiter names and e-mail addresses), the sitemaps to a few entries, the
// job pages without scripts and styles.

func TestPhenomIDFC(t *testing.T) {
	b, tok, ok := Detect("https://careers.idfcfirst.bank.in/in/en/search-results")
	if !ok || b != model.BoardPhenom || tok != "careers.idfcfirst.bank.in/in/en" || DisplayName(b, tok) != "IDFC FIRST Bank" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	first, err := FeedURL(b, tok)
	if err != nil || first != "https://careers.idfcfirst.bank.in/in/en/search-results?from=0&s=1" || !IsListPage(b, first) {
		t.Fatalf("FeedURL = %q, %v", first, err)
	}
	list, err := ParseListPage(b, load(t, "phenom_list.html"), "https://careers.idfcfirst.bank.in/in/en/search-results?from=10&s=1", "India")
	if err != nil || len(list.Postings) != 2 || list.Next != "https://careers.idfcfirst.bank.in/in/en/search-results?from=12&s=1" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	p := list.Postings[0]
	if p.Title != "Cyber Security Analyst" || p.URL != "https://careers.idfcfirst.bank.in/in/en/job/IFBAINP209313ENIN" || p.DetailURL != p.URL || IsListPage(b, p.URL) {
		t.Errorf("posting = %+v", p)
	}
	j, err := ParseJobPage(b, load(t, "phenom_job.html"), p.DetailURL)
	if err != nil || j.Title != "Cyber Security Analyst" || !strings.Contains(j.Location, "Navi Mumbai, Maharashtra, India") ||
		j.URL != p.URL || j.EmploymentType != "Full-Time" || len(j.Description) < 1000 ||
		j.PostedAt == nil || !j.PostedAt.Equal(time.Date(2026, 4, 16, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("job = %+v, %v", j, err)
	}
	empty := []byte(`<script>phApp.ddo = {"eagerLoadRefineSearch":{"totalHits":1373,"data":{"jobs":[]}}};</script>`)
	if _, err := ParseListPage(b, empty, first, ""); err == nil {
		t.Error("a page that reports jobs but holds none must be an error")
	}
}

func TestSitemapStandardChartered(t *testing.T) {
	b, tok, ok := Detect("https://jobs.standardchartered.com/search/?q=&locationsearch=India")
	if !ok || b != model.BoardSitemap || tok != "jobs.standardchartered.com" || DisplayName(b, tok) != "Standard Chartered" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	first, err := FeedURL(b, tok)
	if err != nil || first != "https://jobs.standardchartered.com/sitemap.xml" || !IsListPage(b, first) {
		t.Fatalf("FeedURL = %q, %v", first, err)
	}
	list, err := ParseListPage(b, load(t, "sitemap_sc.xml"), first, "India")
	if err != nil || len(list.Postings) != 2 || list.Next != "" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	p := list.Postings[1]
	if p.URL != "https://jobs.standardchartered.com/job/Bangalore-Mgr%2C-Stress-Testing/1368749857/" || p.DetailURL != p.URL || IsListPage(b, p.URL) {
		t.Errorf("posting = %+v", p)
	}
	j, err := ParseJobPage(b, load(t, "sitemap_sc_job.html"), p.DetailURL)
	if err != nil || j.Title != "Mgr, Stress Testing" || j.Location != "Bangalore, India" || j.EmploymentType != "Permanent" ||
		j.URL != p.URL || len(j.Description) < 500 || strings.Contains(j.Description, "<") ||
		j.PostedAt == nil || !j.PostedAt.Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("job = %+v, %v", j, err)
	}
}

func TestSitemapSocieteGenerale(t *testing.T) {
	b, tok, ok := Detect("https://careers.societegenerale.com/")
	if !ok || b != model.BoardSitemap || tok != "careers.societegenerale.com" || DisplayName(b, tok) != "Société Générale" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	first, _ := FeedURL(b, tok)
	list, err := ParseListPage(b, load(t, "sitemap_socgen.xml"), first, "India")
	// The home page, the contact page and the /en/jobs landing page are not job offers.
	if err != nil || len(list.Postings) != 1 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	p := list.Postings[0]
	j, err := ParseJobPage(b, load(t, "sitemap_socgen_job.html"), p.DetailURL)
	if err != nil || !strings.HasPrefix(j.Title, "Trainee") || !strings.Contains(j.Location, "India") || j.URL != p.URL ||
		len(j.Description) < 500 || j.PostedAt == nil || !j.PostedAt.Equal(time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("job = %+v, %v", j, err)
	}
	if _, err := ParseJobPage(b, []byte(`<html><body><p>Not found</p></body></html>`), p.DetailURL); err == nil {
		t.Error("a page with no job data must be an error")
	}
}

// Nomura has the same SuccessFactors layout with "City:" and "Country:" labels.
func TestSitemapNomura(t *testing.T) {
	b, tok, ok := Detect("https://careers.nomura.com/Nomura/search/?q=&locationsearch=India")
	if !ok || b != model.BoardSitemap || tok != "careers.nomura.com" || DisplayName(b, tok) != "Nomura" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	const page = "https://careers.nomura.com/Nomura/job/Mumbai-HR-Mgt%2CCAO-&-Admin-services/1444405900/"
	j, err := ParseJobPage(b, load(t, "sitemap_nomura_job.html"), page)
	if err != nil || j.Title != "HR-Mgt,CAO & Admin services" || j.Location != "Mumbai, India" || j.URL != page || len(j.Description) < 500 {
		t.Errorf("job = %+v, %v", j, err)
	}
}

// Société Générale HTML-escapes titles inside its JSON-LD.
func TestJobPostingsUnescapeTitle(t *testing.T) {
	base, _ := url.Parse("https://careers.societegenerale.com/en/job-offers/x")
	jobs := JobPostings([]byte(`{"@type":"JobPosting","title":"Regulatory Reporting &amp; Data Management"}`), base)
	if len(jobs) != 1 || jobs[0].Title != "Regulatory Reporting & Data Management" {
		t.Errorf("jobs = %+v", jobs)
	}
}
