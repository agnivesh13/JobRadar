package boards

import (
	"strings"
	"testing"

	"jobradar/internal/model"
)

func TestWorkdayDetect(t *testing.T) {
	for in, want := range map[string]string{
		"https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite":                              "nvidia.wd5.myworkdayjobs.com/nvidia/NVIDIAExternalCareerSite",
		"https://NVIDIA.wd5.myworkdayjobs.com/en-US/NVIDIAExternalCareerSite/job/US-CA/Engineer_JR1": "nvidia.wd5.myworkdayjobs.com/nvidia/NVIDIAExternalCareerSite",
		`<a href="https://browserstack.wd3.myworkdayjobs.com/External">Jobs</a>`:                     "browserstack.wd3.myworkdayjobs.com/browserstack/External",
		"https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs":         "nvidia.wd5.myworkdayjobs.com/nvidia/NVIDIAExternalCareerSite",
	} {
		b, tok, ok := Detect(in)
		if !ok || b != model.BoardWorkday || tok != want {
			t.Errorf("Detect(%q) = %q %q %v, want workday %q", in, b, tok, ok, want)
		}
	}
	if _, _, ok := Detect("https://nvidia.wd5.myworkdayjobs.com/wday/authgwy/nvidia/login.htmld"); ok {
		t.Error("a Workday system path is not a career site")
	}
	if got := DisplayName(model.BoardWorkday, "nvidia.wd5.myworkdayjobs.com/nvidia/Site"); got != "nvidia" {
		t.Errorf("DisplayName = %q", got)
	}
}

func TestWorkdayRequest(t *testing.T) {
	first, err := FeedURL(model.BoardWorkday, "nvidia.wd5.myworkdayjobs.com/nvidia/NVIDIAExternalCareerSite")
	if err != nil || first != "https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs?offset=0" {
		t.Fatalf("FeedURL = %q, %v", first, err)
	}
	endpoint, body, ok := WorkdayRequest("https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs?offset=40&f=locationHierarchy1%3Aabc%2Cdef")
	if !ok || endpoint != "https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite/jobs" {
		t.Fatalf("endpoint = %q ok=%v", endpoint, ok)
	}
	if string(body) != `{"appliedFacets":{"locationHierarchy1":["abc","def"]},"limit":20,"offset":40,"searchText":""}` {
		t.Errorf("body = %s", body)
	}
	if _, _, ok := WorkdayRequest("https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite/job/US/Engineer_JR1"); ok {
		t.Error("a posting is a GET, not a list request")
	}
}

func TestWorkdayList(t *testing.T) {
	const base = "https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite"
	body := load(t, "workday_list.json")

	// With a country, the unfiltered first page only points at the filtered list.
	list, err := ParseWorkdayList(body, base+"/jobs?offset=0", "India")
	if err != nil {
		t.Fatal(err)
	}
	if list.Refine != base+"/jobs?offset=0&f=locationHierarchy1%3A2fcb99c455831013ea52b82135ba3266" || len(list.Postings) != 0 {
		t.Fatalf("refine = %q, postings = %d", list.Refine, len(list.Postings))
	}

	// Without one, it lists its postings and names the next page.
	list, err = ParseWorkdayList(body, base+"/jobs?offset=0", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Postings) != 3 || list.Next != base+"/jobs?offset=20&n=2000" {
		t.Fatalf("postings = %d, next = %q", len(list.Postings), list.Next)
	}
	p := list.Postings[2]
	if p.Title != "Senior Memory System Architect" ||
		p.URL != "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Senior-Memory-System-Architect_JR2022582" ||
		p.DetailURL != base+"/job/US-CA-Santa-Clara/Senior-Memory-System-Architect_JR2022582" {
		t.Errorf("unexpected posting: %+v", p)
	}

	// A later page takes the total from its URL (its own "total" is 0 on the
	// live site), keeps the filter, and never looks for a new one.
	list, _ = ParseWorkdayList(body, base+"/jobs?offset=20&n=45&f=x%3Ay", "India")
	if list.Refine != "" || list.Next != base+"/jobs?offset=40&n=45&f=x%3Ay" || len(list.Postings) != 3 {
		t.Errorf("later page: %+v", list)
	}
	// The last page names no next page; nor does a page with no total (an
	// offset past the end wraps to the first page on the live site).
	for _, q := range []string{"?offset=40&n=45", "?offset=20"} {
		if list, _ = ParseWorkdayList(body, base+"/jobs"+q, ""); list.Next != "" {
			t.Errorf("%s: next = %q", q, list.Next)
		}
	}
}

func TestWorkdayListWithoutCountryLevel(t *testing.T) {
	// This site has no country filter, only sites such as "India Remote".
	const base = "https://browserstack.wd3.myworkdayjobs.com/wday/cxs/browserstack/External"
	list, err := ParseWorkdayList(load(t, "workday_list_nocountry.json"), base+"/jobs?offset=0", "India")
	if err != nil {
		t.Fatal(err)
	}
	if list.Refine != base+"/jobs?offset=0&f=locations%3A07564020a4451005a3027f2ca2d60000" {
		t.Errorf("refine = %q", list.Refine)
	}
}

func TestWorkdayCountryAsSuffix(t *testing.T) {
	// Location names as served by cisco.wd5.myworkdayjobs.com on 17 Nov 2026.
	facets := []workdayFacet{{FacetParameter: "locationMainGroup", Values: []workdayFacet{{FacetParameter: "locations", Values: []workdayFacet{
		{Descriptor: "Bangalore, India", ID: "a"},
		{Descriptor: "Indianapolis, Indiana, US", ID: "b"},
		{Descriptor: "Hyderabad, India", ID: "c"},
		{Descriptor: "Remote - Indiana, USA", ID: "d"},
	}}}}}
	if got := workdayCountryFacet(facets, "India"); got != "locations:a,c" {
		t.Errorf("facet = %q", got)
	}
}

func TestWorkdayJob(t *testing.T) {
	j, err := ParseWorkdayJob(load(t, "workday_job.json"), "https://nvidia.wd5.myworkdayjobs.com/wday/cxs/nvidia/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Senior-Memory-System-Architect_JR2022582")
	if err != nil {
		t.Fatal(err)
	}
	if j.Title != "Senior Memory System Architect" || j.Location != "US, CA, Santa Clara / US, TX, Austin / US, NC, Durham" || j.EmploymentType != "Full time" {
		t.Errorf("unexpected job: %q @ %q (%s)", j.Title, j.Location, j.EmploymentType)
	}
	if j.URL != "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Senior-Memory-System-Architect_JR2022582" {
		t.Errorf("url = %q", j.URL)
	}
	if j.PostedAt == nil || j.PostedAt.Format("2006-01-02") != "2026-11-16" {
		t.Errorf("posted = %v", j.PostedAt)
	}
	if len(j.Description) < 500 || strings.ContainsAny(j.Description, "<>") {
		t.Errorf("description not cleaned: %.200q", j.Description)
	}
}

func TestWorkdaySites(t *testing.T) {
	tenant, hosts, ok := WorkdayHosts("http://lseg.wd5.myworkdayjobs.com/")
	if !ok || tenant != "lseg" || hosts[0] != "lseg.wd5.myworkdayjobs.com" || len(hosts) != 6 {
		t.Fatalf("hosts = %v (%v)", hosts, ok)
	}
	if _, _, ok := WorkdayHosts("https://lseg.wd3.myworkdayjobs.com/Careers"); ok {
		t.Error("a host with a site name is not a bare host")
	}
	// robots.txt as served by lseg.wd3.myworkdayjobs.com and nvidia.wd5.myworkdayjobs.com on 17 Nov 2026.
	sites := WorkdaySites([]byte("Sitemap: https://lseg.wd3.myworkdayjobs.com/Careers/siteMap.xml\nSitemap: https://lseg.wd3.myworkdayjobs.com/Graduate_Careers/siteMap.xml\n\nUser-agent: *\nAllow: /Careers/\nAllow: /Graduate_Careers/\nDisallow: /refreshFacet/\n"))
	if len(sites) != 2 || sites[0] != "Careers" || sites[1] != "Graduate_Careers" {
		t.Errorf("sites = %v", sites)
	}
	sites = WorkdaySites([]byte("Sitemap: https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/siteMap.xml\r\n\r\nUser-agent: *\r\nAllow: /NVIDIAExternalCareerSite/\r\nDisallow: /NVIDIAExternalCareerSiteResearch/\r\nDisallow: /refreshFacet/\r\n"))
	if len(sites) != 1 || sites[0] != "NVIDIAExternalCareerSite" {
		t.Errorf("a disallowed site must not be listed: %v", sites)
	}
	if len(WorkdaySites([]byte(`{"errorCode":"HTTP_422"}`))) != 0 {
		t.Error("an error body names no sites")
	}
}
