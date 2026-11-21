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

// Swiss Re (22 Nov 2026): one "Location:" label with state and country codes.
func TestSitemapSwissRe(t *testing.T) {
	b, tok, ok := Detect("https://careers.swissre.com/search/?q=")
	if !ok || b != model.BoardSitemap || tok != "careers.swissre.com" || DisplayName(b, tok) != "Swiss Re" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	const page = "https://careers.swissre.com/job/Bangalore-Enterprise-Architect-KA/1447979933/"
	j, err := ParseJobPage(b, load(t, "sitemap_swissre_job.html"), page)
	if err != nil || j.Title != "Enterprise Architect" || j.Location != "Bangalore, KA, India" || j.URL != page || len(j.Description) < 500 {
		t.Errorf("job = %+v, %v", j, err)
	}
}

// BFSI hosts checked on 22 Nov 2026.

func TestSitemapTataAIGMicrodata(t *testing.T) {
	b, tok, ok := Detect("https://careers.tataaig.com/")
	if !ok || b != model.BoardSitemap || tok != "careers.tataaig.com" || DisplayName(b, tok) != "Tata AIG" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	const page = "https://careers.tataaig.com/job/Lucknow-Area-Sales-Manager-HOM-Luck/1368835966/"
	j, err := ParseJobPage(b, load(t, "sitemap_tataaig_job.html"), page)
	// No location label: the location and date come from the page's microdata.
	if err != nil || j.Title != "Area Sales Manager - HOM" || j.Location != "Lucknow, India" ||
		j.PostedAt == nil || !j.PostedAt.Equal(time.Date(2026, 11, 12, 15, 0, 0, 0, time.UTC)) {
		t.Errorf("job = %+v, %v", j, err)
	}
}

func TestSitemapWorldline(t *testing.T) {
	b, tok, ok := Detect("https://jobs.worldline.com/search/")
	if !ok || b != model.BoardSitemap || tok != "jobs.worldline.com" || DisplayName(b, tok) != "Worldline" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	j, err := ParseJobPage(b, load(t, "sitemap_worldline_job.html"), "https://jobs.worldline.com/job/Pune-Senior-L&D-Manager-MH/1363173857/")
	if err != nil || j.Title != "Senior L&D Manager" || j.Location != "India - Pune" {
		t.Errorf("job = %+v, %v", j, err)
	}
}

func TestSitemapMSCIiCIMS(t *testing.T) {
	b, tok, ok := Detect("https://globalcareers-msci.icims.com/jobs/search?ss=1")
	if !ok || b != model.BoardSitemap || tok != "globalcareers-msci.icims.com" || DisplayName(b, tok) != "MSCI" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	const page = "https://globalcareers-msci.icims.com/jobs/6078/software-engineer/job"
	j, err := ParseJobPage(b, load(t, "sitemap_msci_job.html"), page)
	// iCIMS gives the country as "US": it is spelled out.
	if err != nil || j.Title != "Software Engineer" || !strings.HasSuffix(j.Location, "United States") || j.URL != page || j.PostedAt == nil {
		t.Errorf("job = %+v, %v", j, err)
	}
}

func TestPhenomMarshCountryFilter(t *testing.T) {
	b, tok, ok := Detect("https://careers.marsh.com/global/en/search-results")
	if !ok || b != model.BoardPhenom || tok != "careers.marsh.com/global/en" || DisplayName(b, tok) != "Marsh McLennan" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	first, _ := FeedURL(b, tok)
	body := load(t, "phenom_marsh_list.html")
	// Both jobs on this page are in Mexico: with India wanted, none is fetched, but paging goes on.
	list, err := ParseListPage(b, body, first, "India")
	if err != nil || len(list.Postings) != 0 || list.Next != "https://careers.marsh.com/global/en/search-results?from=2&s=1" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if all, _ := ParseListPage(b, body, first, ""); len(all.Postings) != 2 {
		t.Errorf("without a country every job is listed: %+v", all)
	}
}

func TestEightfoldMillennium(t *testing.T) {
	b, tok, ok := Detect("https://mlp.eightfold.ai/careers")
	if !ok || b != model.BoardEightfold || tok != "mlp.eightfold.ai/mlp.com" || DisplayName(b, tok) != "Millennium" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	list, err := ParseListPage(b, load(t, "eightfold_mlp_list.json"), "https://mlp.eightfold.ai/api/apply/v2/jobs?domain=mlp.com&start=0&num=10&location=India", "India")
	if err != nil || len(list.Postings) != 2 || list.Next == "" || !strings.Contains(list.Postings[0].DetailURL, "/api/apply/v2/jobs/") {
		t.Fatalf("list = %+v, %v", list, err)
	}
}
