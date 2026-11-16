package discover

import (
	"os"
	"strings"
	"testing"
)

func TestNameFromHeadline(t *testing.T) {
	cases := []struct {
		title, name, stage string
	}{
		{"Dreamteam Bags $53 Mn To Disrupt The CRM Space With AI", "Dreamteam", ""},
		{"Fintech startup Acme Pay raises $12 Mn in Series A led by Peak", "Acme Pay", "series a"},
		{"Bengaluru-based Zylo secures ₹40 Cr in seed funding", "Zylo", "seed"},
		{"Exclusive: Kiwi Labs closes $5M pre-seed round", "Kiwi Labs", "pre-seed"},
		{"QuickCart raises $100 Mn at unicorn valuation", "QuickCart", ""},
		// not companies raising money
		{"Helsing backer Plural raises $1.1B to fund Europe’s next moonshots", "", ""},
		{"Proximal Ventures raises debut fund to back startups", "", ""},
		{"Norwest pushes deeper into AI with new $4B fund", "", ""},
		{"Norwest closes $4 Bn global fund, takes AUM to $18 Bn", "", ""},
		{"Greenjoules raises Rs 150 Cr in Series B led by Amazon funds", "Greenjoules", "series b"},
		{"Oil Cools, But D2C Brands See No Way Out Of Price Hikes", "", ""},
		{"How this founder raised $2M without a deck", "", ""},
		{"India raises $3 billion in sovereign bonds", "", ""},
	}
	for _, c := range cases {
		name, stage, ok := NameFromHeadline(c.title)
		if name != c.name || stage != c.stage || ok != (c.name != "") {
			t.Errorf("%q -> %q, %q, %v; want %q, %q", c.title, name, stage, ok, c.name, c.stage)
		}
	}
}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Fixtures are real responses saved on 17 Nov 2026 and trimmed.
func TestParseFundingFeed(t *testing.T) {
	cands, err := ParseFunding(read(t, "inc42.xml"))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range cands {
		if c.Source != "rss" || c.Detail == "" {
			t.Errorf("candidate missing source or headline: %+v", c)
		}
		found = found || c.Name == "Dreamteam"
	}
	if !found {
		t.Errorf("expected Dreamteam from the saved feed, got %+v", cands)
	}
}

func TestParseYC(t *testing.T) {
	cands, err := ParseYC(read(t, "yc.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 3 {
		t.Fatalf("only the three India companies should pass, got %d: %+v", len(cands), cands)
	}
	if cands[0].Name != "Razorpay" || cands[0].Website == "" || cands[0].Source != "yc" {
		t.Errorf("unexpected first candidate: %+v", cands[0])
	}
}

func TestParseHN(t *testing.T) {
	cands, more, err := ParseHN(read(t, "hn.json"), "50338728")
	if err != nil {
		t.Fatal(err)
	}
	if !more {
		t.Error("the saved page is not the last one")
	}
	for _, c := range cands {
		if c.Name == "" || len(c.Name) > 50 || c.Source != "hn" || strings.Contains(c.Name, ":") {
			t.Errorf("bad candidate: %+v", c)
		}
	}
	t.Logf("%d India/worldwide postings among 40 comments, e.g. %+v", len(cands), first(cands))
}

func first(c []Candidate) any {
	if len(c) == 0 {
		return nil
	}
	return c[0]
}

func TestParseHNShape(t *testing.T) {
	body := []byte(`{"hits":[
	 {"comment_text":"Acme | Backend Engineer | Bangalore, India | ONSITE<p>We build payments. Apply: <a href=\"https:&#x2F;&#x2F;jobs.lever.co&#x2F;acme&#x2F;123\">https://jobs.lever.co/acme/123</a>","parent_id":1},
	 {"comment_text":"Globex (YC W21) | SWE | Remote (Worldwide)<p><a href=\"https:&#x2F;&#x2F;globex.dev&#x2F;careers\">site</a>","parent_id":1},
	 {"comment_text":"Initech | SWE | New York only","parent_id":1},
	 {"comment_text":"Is this remote in India?","parent_id":999}],"page":0,"nbPages":1}`)
	cands, more, err := ParseHN(body, "1")
	if err != nil || more || len(cands) != 2 {
		t.Fatalf("got %d candidates, more=%v, err=%v: %+v", len(cands), more, err, cands)
	}
	if cands[0].Name != "Acme" || cands[0].BoardType != "lever" || cands[0].BoardToken != "acme" {
		t.Errorf("board link not used: %+v", cands[0])
	}
	if cands[1].Name != "Globex" || cands[1].Website != "https://globex.dev" {
		t.Errorf("website or name wrong: %+v", cands[1])
	}
}

func TestFromSearchResults(t *testing.T) {
	got := FromSearchResults([]string{
		"https://boards.greenhouse.io/acme/jobs/1",
		"https://boards.greenhouse.io/acme/jobs/2",
		"https://www.linkedin.com/jobs/view/1",
		"https://careers.globex.com/openings",
		"https://example.com/blog/post",
	}, "software engineer intern hyderabad careers")
	if len(got) != 2 || got[0].BoardToken != "acme" || got[1].Website != "https://careers.globex.com" || got[1].Name != "globex" {
		t.Errorf("got %+v", got)
	}
}
