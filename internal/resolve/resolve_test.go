package resolve

import (
	"context"
	"strings"
	"testing"

	"jobradar/internal/crawl"
)

// fakeWeb answers from a map, so the resolver is tested without a network.
type fakeWeb map[string]string

func (f fakeWeb) Fetch(_ context.Context, u string) (*crawl.Result, error) {
	body, ok := f[u]
	if !ok {
		return &crawl.Result{Status: 404, FinalURL: u}, nil
	}
	ct := "text/html"
	if strings.HasPrefix(body, "{") || strings.HasPrefix(body, "[") {
		ct = "application/json"
	}
	return &crawl.Result{Status: 200, Body: []byte(body), FinalURL: u, ContentType: ct}, nil
}

const filler = "<p>We build software for millions of people and we care about craft, ownership and kindness in everything we ship.</p>"

func home(title, links string) string {
	return "<html><head><title>" + title + "</title></head><body>" + strings.Repeat(filler, 4) + links + "</body></html>"
}

func TestSlugs(t *testing.T) {
	got := Slugs("Acme Technologies Pvt. Ltd.")
	if got[0] != "acme" || !contains(got, "acmetechnologiespvtltd") {
		t.Errorf("slugs: %v", got)
	}
}

func TestBoardBySlugWins(t *testing.T) {
	web := fakeWeb{"https://api.lever.co/v0/postings/acme?mode=json": `[{"text":"Backend Engineer","hostedUrl":"https://jobs.lever.co/acme/1","categories":{"location":"Hyderabad"}}]`}
	res, err := (&Resolver{Fetch: web}).Resolve(context.Background(), "Acme", "")
	if err != nil || res.Board != "lever" || res.BoardToken != "acme" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestDomainThenEmbeddedBoard(t *testing.T) {
	web := fakeWeb{
		"https://acme.com":         home("Acme - payments for everyone", `<a href="/careers">Careers</a>`),
		"https://acme.com/careers": home("Careers at Acme", `<iframe src="https://boards.greenhouse.io/embed/job_board?for=acmepay"></iframe>`),
	}
	var steps []string
	r := &Resolver{Fetch: web, Step: func(_, d, _ string) { steps = append(steps, d) }}
	res, err := r.Resolve(context.Background(), "Acme", "")
	if err != nil || res.Domain != "acme.com" || res.Board != "greenhouse" || res.BoardToken != "acmepay" {
		t.Fatalf("got %+v, %v", res, err)
	}
	if len(steps) < 3 {
		t.Errorf("progress steps should be reported: %v", steps)
	}
}

func TestCustomSiteAndAmbiguity(t *testing.T) {
	web := fakeWeb{
		"https://acme.com":        home("Acme - payments", `<a href="/join">We're hiring</a>`),
		"https://acme.com/join":   home("Join Acme", `<a href="/join/backend-engineer-12">Backend Engineer</a>`),
		"https://acme.in":         home("Acme India - steel pipes", `<a href="/careers">Careers</a>`),
		"https://acme.in/careers": home("Careers", ""),
	}
	r := &Resolver{Fetch: web}
	res, err := r.Resolve(context.Background(), "Acme", "")
	if err != nil || !res.Ambiguous || len(res.Candidates) != 2 {
		t.Fatalf("two equally plausible sites should ask the user: %+v, %v", res, err)
	}
	res, err = r.Resolve(context.Background(), "Acme", "acme.com")
	if err != nil || res.CareersURL != "https://acme.com/join" || res.Board != "" {
		t.Fatalf("after picking: %+v, %v", res, err)
	}
	// Typing a domain skips the guessing entirely.
	res, err = r.Resolve(context.Background(), "acme.in", "")
	if err != nil || res.Domain != "acme.in" || res.CareersURL != "https://acme.in/careers" {
		t.Fatalf("domain input: %+v, %v", res, err)
	}
	if _, err := r.Resolve(context.Background(), "Nonexistent Co", ""); err == nil {
		t.Error("an unknown company should be an error with advice")
	}
}

func TestBareWorkdayHost(t *testing.T) {
	// The tenant is on wd3, not on the wd5 that was typed.
	web := fakeWeb{"https://lseg.wd3.myworkdayjobs.com/robots.txt": "Sitemap: https://lseg.wd3.myworkdayjobs.com/Careers/siteMap.xml\nSitemap: https://lseg.wd3.myworkdayjobs.com/Graduate_Careers/siteMap.xml\n\nUser-agent: *\nAllow: /Careers/\n"}
	res, err := (&Resolver{Fetch: web}).Resolve(context.Background(), "http://lseg.wd5.myworkdayjobs.com/", "")
	if err != nil || res.Board != "workday" || res.BoardToken != "lseg.wd3.myworkdayjobs.com/lseg/Careers" {
		t.Fatalf("got %+v, %v", res, err)
	}
	if len(res.More) != 1 || res.More[0].BoardToken != "lseg.wd3.myworkdayjobs.com/lseg/Graduate_Careers" || res.More[0].Name != "lseg (Graduate Careers)" {
		t.Errorf("more = %+v", res.More)
	}
	if _, err := (&Resolver{Fetch: fakeWeb{}}).Resolve(context.Background(), "nobody.wd1.myworkdayjobs.com", ""); err == nil {
		t.Error("a tenant with no public site must fail")
	}
}
