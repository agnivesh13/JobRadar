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

	// Without one, it lists its postings and every further page up to the 2000 cap.
	list, err = ParseWorkdayList(body, base+"/jobs?offset=0", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Postings) != 3 || len(list.Next) != 99 || list.Next[98] != base+"/jobs?offset=1980" {
		t.Fatalf("postings = %d, next = %d", len(list.Postings), len(list.Next))
	}
	p := list.Postings[2]
	if p.Title != "Senior Memory System Architect" ||
		p.URL != "https://nvidia.wd5.myworkdayjobs.com/NVIDIAExternalCareerSite/job/US-CA-Santa-Clara/Senior-Memory-System-Architect_JR2022582" ||
		p.DetailURL != base+"/job/US-CA-Santa-Clara/Senior-Memory-System-Architect_JR2022582" {
		t.Errorf("unexpected posting: %+v", p)
	}

	// A later page never produces more pages or a new filter.
	list, _ = ParseWorkdayList(body, base+"/jobs?offset=20", "India")
	if list.Refine != "" || len(list.Next) != 0 || len(list.Postings) != 3 {
		t.Errorf("later page: %+v", list)
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
