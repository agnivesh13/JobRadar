package boards

import (
	"strings"
	"testing"

	"jobradar/internal/model"
)

// Keka, Darwinbox and SmartRecruiters. The fixtures are real responses saved
// on 17 Nov 2026 and trimmed.

func TestHostedDetect(t *testing.T) {
	for in, want := range map[string][2]string{
		"https://inito.keka.com/careers/jobdetails/169397":                             {model.BoardKeka, "inito"},
		`<a href="https://Acme.keka.com/careers/">See open roles</a>`:                  {model.BoardKeka, "acme"},
		"https://cars24.darwinbox.in/ms/candidate/careers":                             {model.BoardDarwinbox, "cars24.darwinbox.in"},
		"https://delhivery.darwinbox.in/ms/candidateapi/job?page=1&limit=50":           {model.BoardDarwinbox, "delhivery.darwinbox.in"},
		"https://jobs.smartrecruiters.com/Freshworks/744000161428419-senior-product-m": {model.BoardSmartRecruiters, "Freshworks"},
		"https://careers.smartrecruiters.com/BoschGroup":                               {model.BoardSmartRecruiters, "BoschGroup"},
	} {
		b, tok, ok := Detect(in)
		if !ok || b != want[0] || tok != want[1] {
			t.Errorf("Detect(%q) = %q %q %v, want %v", in, b, tok, ok, want)
		}
	}
	for _, in := range []string{
		"https://cdn.keka.com/careers/v/2026/css/bootstrap.css", // the product's own assets
		"https://www.keka.com/",
		"https://www.smartrecruiters.com/",
	} {
		if b, tok, ok := Detect(in); ok {
			t.Errorf("Detect(%q) = %q %q, want no board", in, b, tok)
		}
	}
	if got := DisplayName(model.BoardDarwinbox, "cars24.darwinbox.in"); got != "cars24" {
		t.Errorf("DisplayName = %q", got)
	}
	if Paged(model.BoardKeka) || !Paged(model.BoardDarwinbox) || !Paged(model.BoardSmartRecruiters) || !Paged(model.BoardWorkday) {
		t.Error("Keka is one feed; the other three are list pages plus postings")
	}
}

func TestKeka(t *testing.T) {
	feed, err := FeedURL(model.BoardKeka, "inito")
	if err != nil || feed != "https://inito.keka.com/careers/api/jobs/default/active" {
		t.Fatalf("FeedURL = %q, %v", feed, err)
	}
	jobs, err := ParseFeed(model.BoardKeka, feed, load(t, "keka.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Title != "Travel Co-ordinator" || j.Location != "Bengaluru, KA, India" || j.Department != "Admin" || j.EmploymentType != "Full time" {
		t.Errorf("unexpected job: %q @ %q (%s, %s)", j.Title, j.Location, j.Department, j.EmploymentType)
	}
	if j.URL != "https://inito.keka.com/careers/jobdetails/169397" {
		t.Errorf("url = %q", j.URL)
	}
	if j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-11-06" {
		t.Errorf("posted = %v", j.PostedAt)
	}
	if !strings.HasPrefix(j.Description, "Experience: 3-5 years\n") || len(j.Description) < 300 || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
}

func TestDarwinbox(t *testing.T) {
	const host = "https://delhivery.darwinbox.in"
	first, err := FeedURL(model.BoardDarwinbox, "delhivery.darwinbox.in")
	if err != nil || first != host+"/ms/candidateapi/job?page=1&limit=50" {
		t.Fatalf("FeedURL = %q, %v", first, err)
	}
	if !IsListPage(model.BoardDarwinbox, first) || IsListPage(model.BoardDarwinbox, host+"/ms/candidateapi/job/a67dd32d8adae1") {
		t.Error("list page and posting page are confused")
	}
	// The fixture is 3 postings of 7: fewer than a page, so this is the last page.
	list, err := ParseListPage(model.BoardDarwinbox, load(t, "darwinbox_list.json"), first, "India")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Postings) != 3 || list.Next != "" || list.Refine != "" {
		t.Fatalf("list = %+v", list)
	}
	p := list.Postings[0]
	if p.Title != "Associate" || p.URL != host+"/ms/candidate/careers/a67dd32d8adae1" || p.DetailURL != host+"/ms/candidateapi/job/a67dd32d8adae1" {
		t.Errorf("posting = %+v", p)
	}
	// More postings than one page holds: the next page is named.
	many := []byte(`{"status":"success","message":{"jobscount":120,"jobs":[{"id":"a1","title":"Engineer"}]}}`)
	if list, _ = ParseListPage(model.BoardDarwinbox, many, host+"/ms/candidateapi/job?page=2&limit=50", ""); list.Next != host+"/ms/candidateapi/job?page=3&limit=50" {
		t.Errorf("next = %q", list.Next)
	}
	if list, _ = ParseListPage(model.BoardDarwinbox, many, host+"/ms/candidateapi/job?page=3&limit=50", ""); list.Next != "" {
		t.Errorf("page 3 of 120 is the last: next = %q", list.Next)
	}
	// As answered for an unknown company.
	if _, err := ParseListPage(model.BoardDarwinbox, []byte(`{"status":"error","data":{"message":"Internal Server Error - Invalid subdomain: x"}}`), first, ""); err == nil {
		t.Error("an error answer must not parse as an empty list")
	}

	j, err := ParseJobPage(model.BoardDarwinbox, load(t, "darwinbox_job.json"), "https://cars24.darwinbox.in/ms/candidateapi/job/a6ad6faa9243f8")
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Builder - Procurement" || j.Location != "Gurgaon, Haryana, India" || j.Department != "Business" || j.EmploymentType != "Permanent" {
		t.Errorf("unexpected job: %q @ %q (%s, %s)", j.Title, j.Location, j.Department, j.EmploymentType)
	}
	if !strings.HasPrefix(j.URL, "https://cars24.darwinbox.in/ms/candidate/careers/") || j.PostedAt == nil {
		t.Errorf("url = %q, posted = %v", j.URL, j.PostedAt)
	}
	if !strings.HasPrefix(j.Description, "Experience: 0 - 6 Years\n") || !strings.Contains(j.Description, "Role Overview") || strings.ContainsAny(j.Description, "<>") || strings.Contains(j.Description, "&lt;") {
		t.Errorf("description: %.200q", j.Description)
	}
}

func TestSmartRecruiters(t *testing.T) {
	const base = "https://careers.smartrecruiters.com/Freshworks/api/more"
	first, err := FeedURL(model.BoardSmartRecruiters, "Freshworks")
	if err != nil || first != base+"?page=0" {
		t.Fatalf("FeedURL = %q, %v", first, err)
	}
	body := load(t, "smartrecruiters_list.html")

	// With a country, the first page only points at the searched list.
	list, err := ParseListPage(model.BoardSmartRecruiters, body, first, "India")
	if err != nil || list.Refine != base+"?page=0&search=India" || len(list.Postings) != 0 {
		t.Fatalf("refine = %+v, %v", list, err)
	}
	// The searched list names its postings and the page after it.
	list, _ = ParseListPage(model.BoardSmartRecruiters, body, list.Refine, "India")
	if len(list.Postings) != 3 || list.Next != base+"?page=1&search=India" || list.Refine != "" {
		t.Fatalf("list = %+v", list)
	}
	p := list.Postings[0]
	if p.Title != "Senior Product Manager" || p.URL != "https://jobs.smartrecruiters.com/Freshworks/744000161428419-senior-product-manager" || p.DetailURL != p.URL {
		t.Errorf("posting = %+v", p)
	}
	if !IsListPage(model.BoardSmartRecruiters, list.Next) || IsListPage(model.BoardSmartRecruiters, p.DetailURL) {
		t.Error("list page and posting page are confused")
	}
	// A page past the end is an empty body: the crawl stops there.
	if list, _ = ParseListPage(model.BoardSmartRecruiters, nil, base+"?page=2&search=India", "India"); list.Next != "" || list.Refine != "" {
		t.Errorf("past the end: %+v", list)
	}
	// A country search that finds nothing falls back to the whole list, once.
	if list, _ = ParseListPage(model.BoardSmartRecruiters, nil, base+"?page=0&search=India", "India"); list.Refine != base+"?page=0&all=1" {
		t.Errorf("fallback = %+v", list)
	}
	if list, _ = ParseListPage(model.BoardSmartRecruiters, body, base+"?page=0&all=1", "India"); list.Refine != "" || list.Next != base+"?page=1&all=1" {
		t.Errorf("whole list = %+v", list)
	}
	if list, _ = ParseListPage(model.BoardSmartRecruiters, nil, base+"?page=0&all=1", "India"); list.Refine != "" || list.Next != "" {
		t.Errorf("an empty whole list must end the crawl: %+v", list)
	}

	const jobURL = "https://jobs.smartrecruiters.com/Freshworks/744000144138769-account-executive-enterprise-sled-"
	j, err := ParseJobPage(model.BoardSmartRecruiters, load(t, "smartrecruiters_job.html"), jobURL+"?trid=abc")
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Account Executive - Enterprise (SLED)" || j.Location != "Austin, United States" || j.EmploymentType != "Full-time" || !j.Remote {
		t.Errorf("unexpected job: %q @ %q (%s, remote %v)", j.Title, j.Location, j.EmploymentType, j.Remote)
	}
	if j.URL != jobURL || j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-08-18" {
		t.Errorf("url = %q, posted = %v", j.URL, j.PostedAt)
	}
	if len(j.Description) < 500 || !strings.Contains(j.Description, "Company Description") || !strings.Contains(j.Description, "Additional Information") || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
	// The same address answers JSON when JSON is preferred, as the crawler does.
	j, err = ParseJobPage(model.BoardSmartRecruiters, load(t, "smartrecruiters_job.json"), "https://jobs.smartrecruiters.com/Freshworks/744000134600399-staff-engineer-full-stack")
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Staff Engineer - Full Stack" || j.Location != "Hyderabad, India" || j.EmploymentType != "Full-time" || j.PostedAt == nil {
		t.Errorf("unexpected job: %q @ %q (%s, %v)", j.Title, j.Location, j.EmploymentType, j.PostedAt)
	}
	if !strings.HasPrefix(j.Description, "Job Description\n") || !strings.Contains(j.Description, "Qualifications") || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
	if _, err := ParseJobPage(model.BoardSmartRecruiters, []byte("<html><body>Not found</body></html>"), jobURL); err == nil {
		t.Error("a page with no posting must be an error")
	}
}
