package boards

import (
	"os"
	"strings"
	"testing"

	"jobradar/internal/model"
)

// The fixtures are real responses saved on 16 Nov 2026 and trimmed to a few
// jobs each, so these tests never touch the network.
func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGreenhouse(t *testing.T) {
	jobs, err := Parse(model.BoardGreenhouse, load(t, "greenhouse.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Title != "Assistant Manager - Internal Audit" || j.Location != "Bengaluru-VTP, India" {
		t.Errorf("unexpected first job: %q @ %q", j.Title, j.Location)
	}
	if !strings.HasPrefix(j.URL, "https://") || j.PostedAt == nil {
		t.Errorf("url or posted date missing: %+v", j)
	}
	if len(j.Description) < 200 || strings.ContainsAny(j.Description, "<>") || strings.Contains(j.Description, "&lt;") {
		t.Errorf("description not cleaned to plain text: %.200q", j.Description)
	}
}

func TestLever(t *testing.T) {
	jobs, err := Parse(model.BoardLever, load(t, "lever.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("want 3 jobs, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Title != "accounts payable manager" || j.Location != "hyderabad" || j.EmploymentType != "full time" {
		t.Errorf("unexpected first job: %+v", j)
	}
	if j.PostedAt == nil || j.PostedAt.Year() < 2020 {
		t.Errorf("createdAt (epoch ms) parsed wrong: %v", j.PostedAt)
	}
	if !strings.Contains(j.Description, "Prefr") {
		t.Errorf("description should include the lists sections: %.200q", j.Description)
	}
}

func TestAshby(t *testing.T) {
	jobs, err := Parse(model.BoardAshby, load(t, "ashby.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 3 {
		t.Fatalf("want 3 jobs, got %d", len(jobs))
	}
	j := jobs[0]
	if j.Title != "Software Engineer, Security, Cloud" || !j.Remote {
		t.Errorf("unexpected first job: %q remote=%v", j.Title, j.Remote)
	}
	if !strings.Contains(j.Location, "New York") || !strings.Contains(j.Location, "Remote (US)") {
		t.Errorf("secondary locations should be joined in: %q", j.Location)
	}
	if j.Pay == nil || j.Pay.Min != 211400 || j.Pay.Max != 290600 || j.Pay.Currency != "USD" || j.Pay.Period != "year" {
		t.Errorf("structured pay parsed wrong: %+v", j.Pay)
	}
	if jobs[1].Pay != nil {
		t.Errorf("job without a salary component must have nil pay: %+v", jobs[1].Pay)
	}
	if jobs[2].EmploymentType != "Intern" {
		t.Errorf("employment type: %q", jobs[2].EmploymentType)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, board := range []string{model.BoardGreenhouse, model.BoardLever, model.BoardAshby} {
		if _, err := Parse(board, []byte("<html>blocked</html>")); err == nil {
			t.Errorf("%s: expected an error for a non-JSON body", board)
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct{ url, board, token string }{
		{"https://boards.greenhouse.io/stripe", "greenhouse", "stripe"},
		{"https://job-boards.greenhouse.io/groww/jobs/123", "greenhouse", "groww"},
		{"https://boards.greenhouse.io/embed/job_board/js?for=rubrik", "greenhouse", "rubrik"},
		{"https://jobs.lever.co/cred/aa98a938", "lever", "cred"},
		{"https://jobs.ashbyhq.com/ramp/34413f8d", "ashby", "ramp"},
		{"https://example.com/careers", "", ""},
	}
	for _, c := range cases {
		board, token, ok := Detect(c.url)
		if board != c.board || token != c.token || ok != (c.board != "") {
			t.Errorf("Detect(%q) = %q, %q, %v", c.url, board, token, ok)
		}
	}
}

func TestHTMLToText(t *testing.T) {
	got := HTMLToText(`<p>Pay: <b>12&ndash;18 LPA</b></p><ul><li>Go</li><li>SQL</li></ul><script>x()</script>`)
	want := "Pay: 12–18 LPA\n- Go\n- SQL"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
