package boards

import (
	"strings"
	"testing"

	"jobradar/internal/model"
)

// Amazon and Microsoft (Eightfold). Fixtures are real responses saved on
// 17 Nov 2026 and trimmed.

func TestAmazon(t *testing.T) {
	const base = "https://www.amazon.jobs/en/search.json?result_limit=100"
	b, tok, ok := Detect("https://www.amazon.jobs/en/jobs/10606836/sales-strategy")
	if !ok || b != model.BoardAmazon || tok != "amazon" || DisplayName(b, tok) != "Amazon" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	first, _ := FeedURL(model.BoardAmazon, "amazon")
	if first != base+"&offset=0&sort=recent" || !IsListPage(model.BoardAmazon, first) {
		t.Fatalf("FeedURL = %q", first)
	}
	body := load(t, "amazon_list.json")

	// The first page narrows to the country, by its ISO code.
	list, err := ParseListPage(model.BoardAmazon, body, first, "India")
	if err != nil || list.Refine != base+"&offset=0&sort=recent&country=IND" || len(list.Jobs) != 0 {
		t.Fatalf("refine = %+v, %v", list, err)
	}
	// The narrowed list carries whole postings and names the next page (2066 hits).
	list, err = ParseListPage(model.BoardAmazon, body, list.Refine, "India")
	if err != nil || len(list.Jobs) != 3 || len(list.Postings) != 0 || list.Next != base+"&offset=100&sort=recent&country=IND" {
		t.Fatalf("list = %d jobs, next %q, %v", len(list.Jobs), list.Next, err)
	}
	j := list.Jobs[2]
	if j.Title != "Tax Analyst I, Indirect Taxation" || j.Location != "Bengaluru, KA, India" || j.EmploymentType != "full-time" {
		t.Errorf("unexpected job: %q @ %q (%s)", j.Title, j.Location, j.EmploymentType)
	}
	if j.URL != "https://www.amazon.jobs/en/jobs/10606878/tax-analyst-i-indirect-taxation" || j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-11-17" {
		t.Errorf("url = %q, posted = %v", j.URL, j.PostedAt)
	}
	if !strings.Contains(j.Description, "Basic qualifications") || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
	// The last page names no next page.
	if list, _ = ParseListPage(model.BoardAmazon, body, base+"&offset=2000&sort=recent&country=IND", "India"); list.Next != "" {
		t.Errorf("last page: next = %q", list.Next)
	}
	// A country without a known code reads the whole list.
	if list, _ = ParseListPage(model.BoardAmazon, body, first, "Atlantis"); list.Refine != "" || len(list.Jobs) != 3 || !strings.HasSuffix(list.Next, "&all=1") {
		t.Errorf("unknown country: %+v", list.Next)
	}
}

func TestApple(t *testing.T) {
	b, tok, ok := Detect("https://jobs.apple.com/en-in/search?location=india-INDC")
	if !ok || b != model.BoardApple || tok != "apple" || DisplayName(b, tok) != "Apple" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	if _, _, ok := Detect("https://jobs.apple.com/careers/in/"); ok {
		t.Error("a country landing page is not the job search")
	}
	first, _ := FeedURL(b, tok)
	if first != "https://jobs.apple.com/en-in/search?page=1" {
		t.Fatalf("FeedURL = %q", first)
	}
	body := load(t, "apple_list.html")
	list, err := ParseListPage(b, body, first, "India")
	if err != nil || list.Refine != "https://jobs.apple.com/en-in/search?page=1&location=india-INDC" {
		t.Fatalf("refine = %+v, %v", list, err)
	}
	// 190 postings, 20 per page.
	list, err = ParseListPage(b, body, list.Refine, "India")
	if err != nil || len(list.Postings) != 3 || list.Next != "https://jobs.apple.com/en-in/search?page=2&location=india-INDC" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	p := list.Postings[0]
	if p.Title != "IN-Business Expert" || p.URL != "https://jobs.apple.com/en-in/details/200313970/in-business-expert?team=APPST" || p.DetailURL != p.URL {
		t.Errorf("posting = %+v", p)
	}
	if !IsListPage(b, list.Next) || IsListPage(b, p.DetailURL) {
		t.Error("list page and posting page are confused")
	}
	if list, _ = ParseListPage(b, body, "https://jobs.apple.com/en-in/search?page=10&location=india-INDC", "India"); list.Next != "" {
		t.Errorf("last page: next = %q", list.Next)
	}
	if _, err := ParseListPage(b, []byte("<html><body>maintenance</body></html>"), first+"&all=1", ""); err == nil {
		t.Error("a page without its data must be an error, not an empty list")
	}

	j, err := ParseJobPage(b, load(t, "apple_job.html"), p.DetailURL)
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "IN-Business Expert" || j.Location != "India" || j.Department != "Apple Retail" || j.URL != p.URL {
		t.Errorf("unexpected job: %q @ %q (%s) %s", j.Title, j.Location, j.Department, j.URL)
	}
	if j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-11-17" {
		t.Errorf("posted = %v", j.PostedAt)
	}
	if !strings.Contains(j.Description, "Minimum qualifications") || len(j.Description) < 1000 || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
}

func TestEightfold(t *testing.T) {
	const base = "https://apply.careers.microsoft.com/api/pcsx/search?domain=microsoft.com&query="
	b, tok, ok := Detect("https://apply.careers.microsoft.com/careers?domain=microsoft.com&location=India")
	if !ok || b != model.BoardEightfold || tok != "apply.careers.microsoft.com/microsoft.com" || DisplayName(b, tok) != "Microsoft" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	first, err := FeedURL(b, tok)
	if err != nil || first != base+"&start=0" {
		t.Fatalf("FeedURL = %q, %v", first, err)
	}
	body := load(t, "eightfold_list.json")

	list, err := ParseListPage(b, body, first, "India")
	if err != nil || list.Refine != base+"&start=0&location=India" {
		t.Fatalf("refine = %+v, %v", list, err)
	}
	list, err = ParseListPage(b, body, list.Refine, "India")
	if err != nil || len(list.Postings) != 3 || list.Next != base+"&start=10&location=India" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	p := list.Postings[0]
	if p.Title != "Platform Engineer" || p.URL != "https://apply.careers.microsoft.com/careers/job/1970393557070761" ||
		p.DetailURL != "https://apply.careers.microsoft.com/api/pcsx/position_details?position_id=1970393557070761&domain=microsoft.com&hl=en" {
		t.Errorf("posting = %+v", p)
	}
	if !IsListPage(b, list.Next) || IsListPage(b, p.DetailURL) {
		t.Error("list page and posting page are confused")
	}
	// 219 positions: the page at 210 is the last.
	if list, _ = ParseListPage(b, body, base+"&start=210&location=India", "India"); list.Next != "" {
		t.Errorf("last page: next = %q", list.Next)
	}

	j, err := ParseJobPage(b, load(t, "eightfold_job.json"), p.DetailURL)
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Platform Engineer" || j.Location != "India" || j.Department != "Service Engineering" || j.EmploymentType != "Full-Time" {
		t.Errorf("unexpected job: %q @ %q (%s, %s)", j.Title, j.Location, j.Department, j.EmploymentType)
	}
	if j.URL != p.URL || j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-11-16" {
		t.Errorf("url = %q, posted = %v", j.URL, j.PostedAt)
	}
	if len(j.Description) < 500 || !strings.Contains(j.Description, "Overview") || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
}

func TestAtlassian(t *testing.T) {
	b, tok, ok := Detect("https://www.atlassian.com/company/careers/all-jobs")
	if !ok || b != model.BoardAtlassian || tok != "atlassian" || DisplayName(b, tok) != "Atlassian" || !Feed(b) || Paged(b) {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	if _, _, ok := Detect("https://www.atlassian.com/software/jira"); ok {
		t.Error("a product page is not the careers feed")
	}
	jobs, err := Parse(b, load(t, "atlassian.json"))
	if err != nil || len(jobs) != 2 {
		t.Fatalf("jobs = %d, %v", len(jobs), err)
	}
	j := jobs[0]
	if j.Title != "Data Platform Engineer" || j.URL != "https://www.atlassian.com/company/careers/details/27589" || j.EmploymentType != "Full-Time" {
		t.Errorf("unexpected job: %+v", j)
	}
	if j.Location != "Bengaluru - India - Bengaluru, 560071 India / Remote - Remote" || !j.Remote {
		t.Errorf("location = %q, remote %v", j.Location, j.Remote)
	}
	if j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-10-29" {
		t.Errorf("posted = %v", j.PostedAt)
	}
	if !strings.Contains(j.Description, "Qualifications") || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
}

func TestOracle(t *testing.T) {
	const host = "https://eeho.fa.us2.oraclecloud.com"
	const api = host + "/hcmRestApi/resources/latest/recruitingCEJobRequisitions?onlyData=true&expand=requisitionList.secondaryLocations,locationsFacet&finder=findReqs;siteNumber=CX_45001,limit=25,"
	b, tok, ok := Detect(host + "/hcmUI/CandidateExperience/en/sites/CX_45001/job/346195")
	if !ok || b != model.BoardOracle || tok != "eeho.fa.us2.oraclecloud.com/CX_45001" || DisplayName(b, tok) != "Oracle" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	first, err := FeedURL(b, tok)
	if err != nil || first != api+"offset=0,sortBy=POSTING_DATES_DESC,facetsList=LOCATIONS" || !IsListPage(b, first) {
		t.Fatalf("FeedURL = %q, %v", first, err)
	}
	body := load(t, "oracle_list.json")

	// The first page narrows to the country through the LOCATIONS facet.
	list, err := ParseListPage(b, body, first, "India")
	if err != nil || list.Refine != api+"offset=0,sortBy=POSTING_DATES_DESC,locationId=300000000106947" {
		t.Fatalf("refine = %+v, %v", list, err)
	}
	// The narrowed list names its postings and the next page (2843 in the fixture).
	list, err = ParseListPage(b, body, list.Refine, "India")
	if err != nil || len(list.Postings) != 2 || list.Next != api+"offset=25,sortBy=POSTING_DATES_DESC,locationId=300000000106947" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	p := list.Postings[0]
	if p.Title != "Oracle Fusion Finance Functional Consultant" || p.URL != host+"/hcmUI/CandidateExperience/en/sites/CX_45001/job/338214" ||
		p.DetailURL != host+`/hcmRestApi/resources/latest/recruitingCEJobRequisitionDetails?expand=all&onlyData=true&finder=ById;Id=%22338214%22,siteNumber=CX_45001` {
		t.Errorf("posting = %+v", p)
	}
	if !IsListPage(b, list.Next) || IsListPage(b, p.DetailURL) {
		t.Error("list page and posting page are confused")
	}
	// A country with no facet entry reads the unfiltered page as the list.
	if list, _ = ParseListPage(b, body, first, "Atlantis"); list.Refine != "" || len(list.Postings) != 2 {
		t.Errorf("unknown country: %+v", list)
	}
	if list, _ = ParseListPage(b, body, api+"offset=2825,sortBy=POSTING_DATES_DESC", ""); list.Next != "" {
		t.Errorf("last page: next = %q", list.Next)
	}
	// A count without postings (the response to a request missing "expand") is an error, not an empty list.
	if _, err := ParseListPage(b, []byte(`{"items":[{"TotalJobsCount":2843,"locationsFacet":[]}]}`), first, "India"); err == nil {
		t.Error("postings missing from a non-empty search must be an error")
	}

	detail := host + `/hcmRestApi/resources/latest/recruitingCEJobRequisitionDetails?expand=all&onlyData=true&finder=ById;Id=%22346195%22,siteNumber=CX_45001`
	j, err := ParseJobPage(b, load(t, "oracle_job.json"), detail)
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Credit & Collections Analyst 2-Fin(Portuguese and Spanish)" || j.Location != "BENGALURU, KARNATAKA, India" || j.Department != "Finance" {
		t.Errorf("unexpected job: %q @ %q (%s)", j.Title, j.Location, j.Department)
	}
	if j.URL != host+"/hcmUI/CandidateExperience/en/sites/CX_45001/job/346195" || j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-11-17" {
		t.Errorf("url = %q, posted = %v", j.URL, j.PostedAt)
	}
	if !strings.Contains(j.Description, "Responsibilities") || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description: %.200q", j.Description)
	}
}

func TestEightfoldQualcomm(t *testing.T) {
	b, tok, ok := Detect("https://careers.qualcomm.com/careers?location=India")
	if !ok || b != model.BoardEightfold || tok != "careers.qualcomm.com/qualcomm.com" || DisplayName(b, tok) != "Qualcomm" {
		t.Fatalf("Detect = %q %q %v", b, tok, ok)
	}
	const base = "https://careers.qualcomm.com/api/pcsx/search?domain=qualcomm.com&query="
	list, err := ParseListPage(b, load(t, "eightfold_qualcomm_list.json"), base+"&start=0&location=India", "India")
	if err != nil || len(list.Postings) != 2 || list.Next != base+"&start=10&location=India" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if p := list.Postings[0]; p.Title != "Senior Lead Engineer - Linux Kernel Development" || p.URL != "https://careers.qualcomm.com/careers/job/446720449165" {
		t.Errorf("posting = %+v", p)
	}
}
