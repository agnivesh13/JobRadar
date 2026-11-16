package salary

import (
	"testing"

	"jobradar/internal/model"
)

var rates = Rates{USD: 88, EUR: 96, GBP: 112}

func TestParse(t *testing.T) {
	const L = 100_000
	cases := []struct {
		text     string
		min, max int64 // rupees per year; 0,0 = no salary expected
	}{
		// the plan's examples
		{"12-18 LPA", 12 * L, 18 * L},
		{"Salary: ₹12,00,000", 12 * L, 12 * L},
		{"18L CTC", 18 * L, 18 * L},
		{"Compensation: $90k", 90_000 * 88, 90_000 * 88},
		{"Stipend: ₹40,000 per month", 480_000, 480_000},

		// Indian formats
		{"CTC: 12 to 18 lakhs per annum", 12 * L, 18 * L},
		{"Package of 25 LPA", 25 * L, 25 * L},
		{"Salary range: ₹15 - 22 lakh", 15 * L, 22 * L},
		{"Compensation: 80 lakh - 1.2 cr", 80 * L, 120 * L},
		{"Salary: Rs. 8,00,000 - 12,00,000 per annum", 8 * L, 12 * L},
		{"Pay: INR 60,000 - 80,000 per month", 720_000, 960_000},
		{"Stipend: 40,000 per month", 480_000, 480_000},
		{"Stipend of 25k - 40k per month", 300_000, 480_000},
		{"Stipend: ₹50,000", 600_000, 600_000}, // small bare amount = monthly
		{"We offer 6.5 LPA for freshers", 650_000, 650_000},
		{"CTC 14.5LPA", 1_450_000, 1_450_000},

		// foreign currency
		{"The base salary range is $120,000 - $150,000 per year", 120_000 * 88, 150_000 * 88},
		{"Pay: $50 - $65 per hour", 50 * 2080 * 88, 65 * 2080 * 88},
		{"Salary: €60k–€75k", 60_000 * 96, 75_000 * 96},
		{"Annual salary £45,000", 45_000 * 112, 45_000 * 112},
		{"Compensation: USD 4,000 per month", 4_000 * 12 * 88, 4_000 * 12 * 88},

		// not salaries
		{"We raised $50M in Series B funding", 0, 0},
		{"Trusted by 5 lakh customers", 0, 0},
		{"Processing ₹500 crore in transactions every month", 0, 0},
		{"$500 annual learning budget", 0, 0},
		{"Health insurance cover of ₹5 lakh", 0, 0},
		{"3-5 years of experience in Go", 0, 0},
		{"Join 12 engineers in our Hyderabad office", 0, 0},
		{"Valued at $1.2 billion", 0, 0},
		{"Land 5 or more new logos per year at a target ACV of $250K or more.", 0, 0},
		{"Compensation package: $150k-$175k CAD base, plus benefits", 0, 0},
		{"", 0, 0},
	}
	for _, c := range cases {
		got, ok := Parse(c.text, rates)
		if c.max == 0 {
			if ok {
				t.Errorf("%q: expected no salary, got %d-%d", c.text, got.Min, got.Max)
			}
			continue
		}
		if !ok || got.Min != c.min || got.Max != c.max {
			t.Errorf("%q: got %d-%d (ok=%v), want %d-%d", c.text, got.Min, got.Max, ok, c.min, c.max)
		}
	}
}

func TestParseMultiline(t *testing.T) {
	text := "About us\nWe raised $30M last year.\nRequirements:\n- 0-2 years experience\nCompensation: 14-20 LPA plus ESOPs\nApply now"
	got, ok := Parse(text, rates)
	if !ok || got.Min != 1_400_000 || got.Max != 2_000_000 {
		t.Fatalf("got %+v ok=%v", got, ok)
	}
	if got.Text != "Compensation: 14-20 LPA plus ESOPs" {
		t.Errorf("quote should be the salary line, got %q", got.Text)
	}
}

func TestNormalize(t *testing.T) {
	cases := []struct {
		pay      model.Pay
		min, max int64
		ok       bool
	}{
		{model.Pay{Min: 211400, Max: 290600, Currency: "USD", Period: "year"}, 211400 * 88, 290600 * 88, true},
		{model.Pay{Min: 40000, Max: 50000, Currency: "INR", Period: "month"}, 480_000, 600_000, true},
		{model.Pay{Max: 1_800_000, Currency: "INR", Period: "year"}, 1_800_000, 1_800_000, true},
		{model.Pay{Min: 30, Max: 40, Currency: "USD", Period: "hour"}, 30 * 2080 * 88, 40 * 2080 * 88, true},
		{model.Pay{Min: 100, Max: 200, Currency: "XYZ", Period: "year"}, 0, 0, false},
		{model.Pay{Min: 1, Max: 2, Currency: "INR", Period: "year"}, 0, 0, false}, // implausibly small
	}
	for _, c := range cases {
		got, ok := Normalize(c.pay, rates)
		if ok != c.ok || got.Min != c.min || got.Max != c.max {
			t.Errorf("%+v: got %d-%d ok=%v, want %d-%d ok=%v", c.pay, got.Min, got.Max, ok, c.min, c.max, c.ok)
		}
	}
}
