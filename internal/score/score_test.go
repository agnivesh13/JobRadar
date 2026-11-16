package score

import (
	"slices"
	"testing"
)

func TestClassify(t *testing.T) {
	cases := []struct{ title, emp, desc, want string }{
		{"Software Engineer Intern", "", "", Intern},
		{"Applied Scientist", "Intern", "", Intern},
		{"Senior Backend Engineer", "", "", Senior},
		{"Engineering Manager", "", "", Senior},
		{"Software Engineer II", "", "", Mid},
		{"Software Engineer, New Grad 2027", "", "", NewGrad},
		{"Graduate Engineer Trainee", "", "", NewGrad},
		{"Backend Engineer", "", "Requirements:\n- 0-2 years of experience in Python", NewGrad},
		{"Backend Engineer", "", "You have 1+ years of professional experience", Junior},
		{"Backend Engineer", "", "- 3-5 years of experience building APIs", Mid},
		{"Backend Engineer", "", "8+ years of industry experience", Senior},
		{"Backend Engineer", "", "2+ years of experience with Go\n5+ years of experience overall", Mid},
		{"Backend Engineer", "", "We welcome freshers and recent graduates.", NewGrad},
		{"Backend Engineer", "", "Founded 10 years ago, we get to work on hard problems.", ""},
	}
	for _, c := range cases {
		if got := Classify(c.title, c.emp, c.desc); got != c.want {
			t.Errorf("Classify(%q, %q, %q) = %q, want %q", c.title, c.emp, c.desc, got, c.want)
		}
	}
}

func i64(v int64) *int64 { return &v }

func TestSalaryState(t *testing.T) {
	s := Defaults()
	cases := []struct {
		exp  string
		max  *int64
		want string
	}{
		{NewGrad, i64(1_800_000), Above},
		{NewGrad, i64(1_000_000), Above}, // exactly the floor
		{NewGrad, i64(600_000), Below},
		{NewGrad, nil, Unknown},
		{Intern, i64(480_000), Above}, // 40,000 a month
		{Intern, i64(300_000), Below}, // 25,000 a month
		{Intern, i64(600_000), Above}, // 50k/month is below 10 LPA but above the stipend floor
	}
	for _, c := range cases {
		if got := SalaryState(c.exp, c.max, s); got != c.want {
			t.Errorf("SalaryState(%s, %v) = %s, want %s", c.exp, c.max, got, c.want)
		}
	}
}

func TestEvaluateFilters(t *testing.T) {
	s := Defaults()
	cases := []struct {
		name    string
		in      Input
		matched bool
	}{
		{"new grad backend in Hyderabad", Input{Title: "Software Engineer", Location: "Hyderabad, India", Experience: NewGrad}, true},
		{"Bengaluru alias", Input{Title: "Backend Engineer", Location: "Bengaluru, Karnataka", Experience: Junior}, true},
		{"remote India", Input{Title: "ML Engineer", Location: "Remote - India", Remote: true}, true},
		{"remote US only", Input{Title: "ML Engineer", Location: "Remote (US)", Remote: true}, false},
		{"bare remote", Input{Title: "ML Engineer", Location: "Remote", Remote: true}, false},
		{"anywhere in France", Input{Title: "Software Engineer", Location: "Anywhere in France", Remote: true}, false},
		{"remote APAC", Input{Title: "Software Engineer", Location: "Remote - NA, APAC, EMEA", Remote: true}, true},
		{"design intern", Input{Title: "UX Design - Intern", Location: "Bengaluru", Experience: Intern}, false},
		{"sde intern", Input{Title: "SDE Intern", Location: "Bengaluru", Experience: Intern}, true},
		{"wrong city", Input{Title: "Software Engineer", Location: "New York, NY", Experience: NewGrad}, false},
		{"senior title", Input{Title: "Senior Software Engineer", Location: "Hyderabad", Experience: Senior}, false},
		{"excluded word", Input{Title: "Sales Engineer", Location: "Hyderabad"}, false},
		{"unrelated role", Input{Title: "Chef", Location: "Hyderabad"}, false},
		{"mid level", Input{Title: "Software Engineer", Location: "Hyderabad", Experience: Mid}, false},
		{"location not stated passes", Input{Title: "Quant Researcher", Location: ""}, true},
		{"quant intern", Input{Title: "Quantitative Trading Intern", Location: "Bangalore", Experience: Intern}, true},
		{"sr is a whole word only", Input{Title: "SRE", Location: "Hyderabad", Experience: Junior}, true},
	}
	for _, c := range cases {
		got := Evaluate(c.in, s, Signal{})
		want := c.matched
		if got.Matched != want {
			t.Errorf("%s: matched=%v, want %v (%s)", c.name, got.Matched, want, got.FitReason)
		}
	}
}

func TestBatchFlag(t *testing.T) {
	s := Defaults() // GradYear 2027
	cases := []struct {
		desc    string
		flagged bool
	}{
		{"Open to the 2027 batch only.", false},
		{"Eligibility: 2026 batch", true},
		{"Graduating in 2025 or 2026", true},
		{"Graduating in 2026/2027", false},
		{"Class of 2024-2027 may apply", false},
		{"Founded in 2021, we raised in 2025.", false},
		{"No year mentioned at all", false},
	}
	for _, c := range cases {
		got := Evaluate(Input{Title: "Software Engineer", Location: "Hyderabad", Description: c.desc}, s, Signal{})
		if slices.Contains(got.Flags, "batch_mismatch") != c.flagged {
			t.Errorf("%q: flags=%v, want flagged=%v", c.desc, got.Flags, c.flagged)
		}
	}
}

func TestFitScoreOrdering(t *testing.T) {
	s := Defaults()
	s.ResumeText = "Python, FastAPI, PostgreSQL, AWS, Docker, React, SQL, asyncio, scikit-learn"
	good := Evaluate(Input{
		Title: "Backend Engineer, New Grad", Location: "Hyderabad", Experience: NewGrad, SalaryMax: i64(1_800_000),
		Description: "Build APIs in Python and FastAPI on AWS with PostgreSQL and Docker.",
	}, s, Signal{})
	unknownPay := Evaluate(Input{
		Title: "Backend Engineer, New Grad", Location: "Hyderabad", Experience: NewGrad,
		Description: "Build APIs in Python and FastAPI on AWS with PostgreSQL and Docker.",
	}, s, Signal{})
	highTier := Evaluate(Input{
		Title: "Backend Engineer, New Grad", Location: "Hyderabad", Experience: NewGrad,
		Description: "Build APIs in Python and FastAPI on AWS with PostgreSQL and Docker.",
	}, s, Signal{PayTier: "high"})
	offStack := Evaluate(Input{
		Title: "Backend Engineer", Location: "Hyderabad", Experience: Mid,
		Description: "5 years of Ruby and PHP with Cassandra.",
	}, s, Signal{})

	if good.FitScore < 90 {
		t.Errorf("strong match should score 90+, got %d (%s)", good.FitScore, good.FitReason)
	}
	if !(good.FitScore > highTier.FitScore && highTier.FitScore > unknownPay.FitScore && unknownPay.FitScore > offStack.FitScore) {
		t.Errorf("ordering wrong: stated=%d highTier=%d unknown=%d offStack=%d", good.FitScore, highTier.FitScore, unknownPay.FitScore, offStack.FitScore)
	}
	if good.SalaryState != Above || unknownPay.SalaryState != Unknown {
		t.Errorf("salary states: %s, %s", good.SalaryState, unknownPay.SalaryState)
	}
}

func TestSkillsIn(t *testing.T) {
	got := skillsIn("We use C++, Node.js, PostgreSQL and React. Please express interest in the rest of the team.")
	for _, want := range []string{"c++", "node.js", "postgres", "react"} {
		if !got[want] {
			t.Errorf("missing skill %q in %v", want, got)
		}
	}
	for _, bad := range []string{"express", "rest"} {
		if got[bad] {
			t.Errorf("English word matched as skill %q", bad)
		}
	}
}
