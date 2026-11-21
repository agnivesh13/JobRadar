package extract

import (
	"strings"
	"testing"
)

const detailPage = `<!doctype html><html><head><title>Backend Engineer | Acme</title>
<script type="application/ld+json">
{"@context":"https://schema.org","@graph":[{"@type":"Organization","name":"Acme"},
{"@type":"JobPosting","title":"Backend Engineer","datePosted":"2026-11-01",
 "employmentType":["FULL_TIME"],"description":"<p>Build APIs in <b>Go</b>.</p><ul><li>0-2 years</li></ul>",
 "hiringOrganization":{"@type":"Organization","name":"Acme"},
 "jobLocation":[{"@type":"Place","address":{"@type":"PostalAddress","addressLocality":"Hyderabad","addressRegion":"Telangana","addressCountry":{"@type":"Country","name":"IN"}}}],
 "baseSalary":{"@type":"MonetaryAmount","currency":"INR","value":{"@type":"QuantitativeValue","minValue":1200000,"maxValue":"18,00,000","unitText":"YEAR"}}}]}
</script></head>
<body><nav><a href="/">Home</a><a href="/blog/hello">Blog</a></nav>
<h1>Backend Engineer</h1><p>Join us.</p>
<a href="/careers/data-engineer-4412?utm_source=x#apply">Data Engineer</a>
<a href="https://other.example.org/jobs/1">Elsewhere</a>
<a href="/careers">All open roles</a>
<iframe src="https://boards.greenhouse.io/embed/job_board?for=acmeco"></iframe>
</body></html>`

func TestParseJSONLD(t *testing.T) {
	p := Parse([]byte(detailPage), "https://www.acme.com/careers/backend-engineer-1001")
	if len(p.Jobs) != 1 {
		t.Fatalf("want 1 JobPosting, got %d", len(p.Jobs))
	}
	j := p.Jobs[0]
	if j.Title != "Backend Engineer" || j.Location != "Hyderabad, Telangana, India" || j.EmploymentType != "FULL_TIME" {
		t.Errorf("unexpected job: %+v", j)
	}
	if j.Pay == nil || j.Pay.Min != 1200000 || j.Pay.Max != 1800000 || j.Pay.Currency != "INR" || j.Pay.Period != "year" {
		t.Errorf("baseSalary parsed wrong: %+v", j.Pay)
	}
	if j.PostedAt == nil || !strings.Contains(j.Description, "Build APIs in Go") || strings.Contains(j.Description, "<") {
		t.Errorf("date or description wrong: %v %q", j.PostedAt, j.Description)
	}
	if j.URL != "https://www.acme.com/careers/backend-engineer-1001" {
		t.Errorf("url should default to the page: %s", j.URL)
	}
	if p.Board != "greenhouse" || p.BoardTok != "acmeco" {
		t.Errorf("embedded board not detected: %q %q", p.Board, p.BoardTok)
	}
	if p.H1 != "Backend Engineer" || p.Title != "Backend Engineer | Acme" {
		t.Errorf("h1/title: %q / %q", p.H1, p.Title)
	}
}

func TestFollow(t *testing.T) {
	p := Parse([]byte(detailPage), "https://www.acme.com/careers/backend-engineer-1001")
	got := Follow(p, "https://www.acme.com/careers/backend-engineer-1001", 1)
	want := map[string]bool{
		"https://www.acme.com/careers/data-engineer-4412": true, // tracking parameter and fragment removed
		"https://www.acme.com/careers":                    true,
	}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, u := range got {
		if !want[u] {
			t.Errorf("should not follow %s (blog, home and off-site links are skipped)", u)
		}
	}
	if Follow(p, "https://www.acme.com/careers", MaxDepth) != nil {
		t.Error("nothing is followed at the depth limit")
	}
}

func TestHeuristicJob(t *testing.T) {
	body := `<html><head><title>ML Engineer - Acme Careers</title></head><body><h1>ML Engineer</h1>
<p>Location: Bengaluru, India</p><h2>Responsibilities</h2><ul><li>Train models</li></ul>
<h2>Requirements</h2><ul><li>Python, PyTorch</li></ul>` + strings.Repeat("<p>We build things that matter to millions of users every day.</p>", 8) + `<a href="/apply/7">Apply now</a></body></html>`
	p := Parse([]byte(body), "https://acme.com/careers/ml-engineer-7781")
	j, ok := HeuristicJob(p, "https://acme.com/careers/ml-engineer-7781")
	if !ok || j.Title != "ML Engineer" || j.Location != "Bengaluru, India" {
		t.Fatalf("posting not recognised: ok=%v %+v", ok, j)
	}
	if _, ok := HeuristicJob(p, "https://acme.com/careers"); ok {
		t.Error("a listing URL must not be taken for a posting")
	}
	about := Parse([]byte(`<html><body><h1>About us</h1>`+strings.Repeat("<p>Our story began long ago.</p>", 30)+`</body></html>`), "https://acme.com/careers/our-story")
	if _, ok := HeuristicJob(about, "https://acme.com/careers/our-story"); ok {
		t.Error("a page without posting language must not be taken for a posting")
	}
}

func TestSitemapAndEmpty(t *testing.T) {
	urls := SitemapURLs([]byte(`<?xml version="1.0"?><urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9"><url><loc>https://acme.com/careers/a-1</loc></url><url><loc> https://acme.com/about </loc></url></urlset>`))
	if len(urls) != 2 || urls[1] != "https://acme.com/about" {
		t.Errorf("sitemap: %v", urls)
	}
	shell := []byte(`<html><body><div id="root"></div><script src="/app.js"></script></body></html>`)
	if !LooksEmpty(Parse(shell, "https://acme.com/careers"), shell) {
		t.Error("a single-page-app shell should be flagged for rendering")
	}
}

func TestHostedCareers(t *testing.T) {
	body := []byte(`<link href="https://cdn.keka.com/careers/v/1/app.css"><a href="https://www.keka.com/">Powered by Keka</a>
<a class="btn" href="https://acme.keka.com/careers/">See open roles</a>`)
	if got := HostedCareers(body); got != "https://acme.keka.com/careers/" {
		t.Errorf("got %q", got)
	}
	if got := HostedCareers([]byte(`<a href="https://acme.com/careers">Careers</a>`)); got != "" {
		t.Errorf("no hosted site expected, got %q", got)
	}
}
