// Package salary finds pay figures in job text and normalises them to rupees
// per year.
package salary

import (
	"math"
	"regexp"
	"strconv"
	"strings"

	"jobradar/internal/model"
)

// Rates converts foreign currency to rupees. Configurable in .env.
type Rates struct {
	USD, EUR, GBP float64
}

// Range is a salary in rupees per year. Text is the sentence it came from.
type Range struct {
	Min, Max int64
	Text     string
}

const (
	lakh  = 100_000
	crore = 10_000_000
	// Sanity bounds for a yearly figure: below 5,000 a month or above 20 crore
	// a year is a parsing mistake (a headcount, a funding round), not a salary.
	minYearly = 60_000
	maxYearly = 20 * crore
	// A bare rupee amount under this with no period stated is a monthly figure:
	// nobody advertises an annual salary of 1.2 lakh.
	monthlyCeiling = 150_000
)

// Normalize converts a structured pay field (from a board feed or JobPosting
// markup) to rupees per year.
func Normalize(p model.Pay, r Rates) (Range, bool) {
	rate, ok := r.rate(p.Currency)
	if !ok {
		return Range{}, false
	}
	mult := periodMultiplier(p.Period)
	lo, hi := p.Min*rate*mult, p.Max*rate*mult
	if lo == 0 {
		lo = hi
	}
	if hi == 0 {
		hi = lo
	}
	return finish(lo, hi, p.Text)
}

func (r Rates) rate(currency string) (float64, bool) {
	switch strings.ToUpper(strings.TrimSpace(currency)) {
	case "INR", "RS", "₹":
		return 1, true
	case "USD", "$":
		return r.USD, r.USD > 0
	case "EUR", "€":
		return r.EUR, r.EUR > 0
	case "GBP", "£":
		return r.GBP, r.GBP > 0
	}
	return 0, false
}

func periodMultiplier(period string) float64 {
	switch strings.ToLower(period) {
	case "month":
		return 12
	case "week":
		return 52
	case "hour":
		return 2080 // 40 hours x 52 weeks
	}
	return 1
}

func finish(lo, hi float64, text string) (Range, bool) {
	if lo > hi {
		lo, hi = hi, lo
	}
	if hi < minYearly || hi > maxYearly {
		return Range{}, false
	}
	return Range{Min: int64(math.Round(lo)), Max: int64(math.Round(hi)), Text: strings.TrimSpace(text)}, true
}

const (
	num   = `(\d[\d,]*(?:\.\d+)?)`
	dash  = `\s*(?:-|–|—|to|and)\s*`
	inrP  = `(?:₹|rs\.?|inr)\s*`
	indU  = `(lpa|lakhs?|lacs?|l|crores?|cr|k)`
	perRe = `(?:per|a|an|/|p\.?)\s*`
)

var (
	// "12-18 LPA", "12 to 18 lakhs", "18L CTC", "1.2 cr"
	lakhRange  = regexp.MustCompile(`(?i)(?:` + inrP + `)?` + num + `\s*(?:lpa|lakhs?|lacs?|l)?` + dash + `(?:` + inrP + `)?` + num + `\s*(lpa|lakhs?|lacs?|l|crores?|cr)\b`)
	lakhSingle = regexp.MustCompile(`(?i)(?:` + inrP + `)?` + num + `\s*(lpa|lakhs?|lacs?|l|crores?|cr)\b`)
	// "₹12,00,000", "Rs. 40,000 - 60,000 per month", "INR 25k"
	inrRange  = regexp.MustCompile(`(?i)` + inrP + num + `\s*` + indU + `?` + dash + `(?:` + inrP + `)?` + num + `\s*` + indU + `?\b`)
	inrSingle = regexp.MustCompile(`(?i)` + inrP + num + `\s*` + indU + `?\b`)
	// "$90k", "$120,000 - $150,000", "USD 90,000", "€60k", "£45,000"
	fxRange  = regexp.MustCompile(`(?i)(\$|us\$|usd|€|eur|£|gbp)\s*` + num + `\s*(k)?` + dash + `(?:\$|us\$|usd|€|eur|£|gbp)?\s*` + num + `\s*(k)?\b`)
	fxSingle = regexp.MustCompile(`(?i)(\$|us\$|usd|€|eur|£|gbp)\s*` + num + `\s*(k)?\b`)
	// "40,000 per month" with no currency sign, only trusted next to "stipend"
	bareMonthly = regexp.MustCompile(`(?i)` + num + `\s*(k)?(?:` + dash + num + `\s*(k)?)?\s*(?:/-)?\s*` + perRe + `(?:month|mo)\b`)

	monthRe = regexp.MustCompile(`(?i)(?:` + perRe + `(?:month|mo)\b|\bmonthly\b|\bpm\b|p\.m\.)`)
	hourRe  = regexp.MustCompile(`(?i)(?:` + perRe + `(?:hour|hr)\b|\bhourly\b)`)
	yearRe  = regexp.MustCompile(`(?i)(?:` + perRe + `(?:year|yr|annum)\b|\bannual(?:ly)?\b|\byearly\b|\bp\.?a\.?\b|\blpa\b|\bctc\b)`)
	// Words that make a money figure a salary rather than revenue or funding.
	payContext = regexp.MustCompile(`(?i)\b(salary|salaries|ctc|compensation|pay|paid|stipend|package|remuneration|base|ote|earn|earnings|wage|wages|lpa|per annum|per month|per year|per hour|hourly|annually|range)\b`)
	// Words that mark a figure as not a salary.
	notPay = regexp.MustCompile(`(?i)\b(funding|raised|raise|valuation|valued|revenue|arr|gmv|aum|customers|users|downloads|transactions|series [a-h]|investors?|disbursed|processed|insurance|cover(?:age)?|bonus|budget|allowance|reimbursement|relocation|referral|esops?|worth|acv|quota|quotas|deal size|deals|logos|pipeline|bookings|portfolio|spend|savings|grant|prize|scholarship)\b`)
	// Currencies with no configured rate: a "$" on such a line is not US dollars.
	otherFx   = regexp.MustCompile(`\b(CAD|AUD|SGD|NZD|HKD|AED|MXN|BRL|C\$|A\$|S\$)`)
	bigSuffix = regexp.MustCompile(`(?i)^\s*(?:\+\s*)?(m|mn|mm|million|b|bn|billion|trillion)\b`)
	stipendRe = regexp.MustCompile(`(?i)\bstipend\b`)
)

func toNum(s string) float64 {
	f, _ := strconv.ParseFloat(strings.ReplaceAll(s, ",", ""), 64)
	return f
}

func indianUnit(unit string) float64 {
	switch strings.ToLower(unit) {
	case "lpa", "lakh", "lakhs", "lac", "lacs", "l":
		return lakh
	case "cr", "crore", "crores":
		return crore
	case "k":
		return 1000
	}
	return 1
}

// Parse finds the first believable salary in free text. It reads line by
// line so the context words must sit next to the figure.
func Parse(text string, r Rates) (Range, bool) {
	for _, line := range strings.Split(text, "\n") {
		if len(line) > 600 {
			line = clipLine(line)
		}
		if rng, ok := parseLine(line, r); ok {
			return rng, true
		}
	}
	return Range{}, false
}

// clipLine cuts a very long line down to the part around the first pay word.
func clipLine(line string) string {
	loc := payContext.FindStringIndex(line)
	if loc == nil {
		return strings.ToValidUTF8(line[:600], "")
	}
	start, end := max(0, loc[0]-200), min(len(line), loc[1]+400)
	return strings.ToValidUTF8(line[start:end], "")
}

func parseLine(line string, r Rates) (Range, bool) {
	hasContext := payContext.MatchString(line)
	blocked := notPay.MatchString(line) && !regexp.MustCompile(`(?i)\b(salary|ctc|stipend|compensation|lpa)\b`).MatchString(line)
	if blocked {
		return Range{}, false
	}

	// Rupees in lakhs or crores. "LPA" is a salary by definition; a bare
	// "lakh" needs a pay word on the line ("5 lakh customers" is not pay).
	if m := lakhRange.FindStringSubmatchIndex(line); m != nil {
		unit := line[m[6]:m[7]]
		if isLPA(unit) || hasContext {
			u := indianUnit(unit)
			lo, hi := toNum(line[m[2]:m[3]])*u, toNum(line[m[4]:m[5]])*u
			if lo > hi && u == crore { // "80 lakh - 1.2 cr"
				lo = toNum(line[m[2]:m[3]]) * lakh
			}
			return finish(lo, hi, line)
		}
	}
	if m := lakhSingle.FindStringSubmatchIndex(line); m != nil {
		unit := line[m[4]:m[5]]
		if (isLPA(unit) || hasContext) && !bigSuffix.MatchString(line[m[1]:]) {
			v := toNum(line[m[2]:m[3]]) * indianUnit(unit)
			return finish(v, v, line)
		}
	}

	// Rupees with a currency sign.
	if m := inrRange.FindStringSubmatch(line); m != nil && hasContext {
		lo, hi := toNum(m[1])*indianUnit(m[2]), toNum(m[3])*indianUnit(m[4])
		if m[2] == "" && m[4] != "" && lo < hi/100 { // "₹12 - 18 lakh": unit applies to both
			lo = toNum(m[1]) * indianUnit(m[4])
		}
		return finish(lo*inrPeriod(line, hi), hi*inrPeriod(line, hi), line)
	}
	if m := inrSingle.FindStringSubmatchIndex(line); m != nil && hasContext && !bigSuffix.MatchString(line[m[1]:]) {
		unit := ""
		if m[4] >= 0 {
			unit = line[m[4]:m[5]]
		}
		v := toNum(line[m[2]:m[3]]) * indianUnit(unit)
		return finish(v*inrPeriod(line, v), v*inrPeriod(line, v), line)
	}

	// Foreign currency: needs a pay word, because "$" figures are usually not pay.
	if hasContext && !otherFx.MatchString(line) {
		if m := fxRange.FindStringSubmatchIndex(line); m != nil && !bigSuffix.MatchString(line[m[1]:]) {
			g := groups(line, m)
			rate, ok := r.rate(fxCode(g[1]))
			lo, hi := fxAmount(g[2], g[3] != "" || g[5] != ""), fxAmount(g[4], g[5] != "")
			if ok && fxPlausible(line, hi) {
				mult := fxPeriod(line)
				return finish(lo*rate*mult, hi*rate*mult, line)
			}
		}
		if m := fxSingle.FindStringSubmatchIndex(line); m != nil && !bigSuffix.MatchString(line[m[1]:]) {
			g := groups(line, m)
			rate, ok := r.rate(fxCode(g[1]))
			v := fxAmount(g[2], g[3] != "")
			if ok && fxPlausible(line, v) {
				mult := fxPeriod(line)
				return finish(v*rate*mult, v*rate*mult, line)
			}
		}
	}

	// "Stipend: 40,000 per month" with no currency sign.
	if stipendRe.MatchString(line) {
		if m := bareMonthly.FindStringSubmatch(line); m != nil {
			lo := toNum(m[1]) * kMult(m[2])
			hi := lo
			if m[3] != "" {
				hi = toNum(m[3]) * kMult(m[4])
				if m[2] == "" && m[4] != "" && lo < 1000 {
					lo *= 1000
				}
			}
			return finish(lo*12, hi*12, line)
		}
	}
	return Range{}, false
}

func isLPA(unit string) bool { return strings.EqualFold(unit, "lpa") }

func groups(line string, idx []int) []string {
	out := make([]string, len(idx)/2)
	for i := range out {
		if idx[2*i] >= 0 {
			out[i] = line[idx[2*i]:idx[2*i+1]]
		}
	}
	return out
}

func kMult(k string) float64 {
	if k != "" {
		return 1000
	}
	return 1
}

func fxAmount(n string, k bool) float64 {
	if k {
		return toNum(n) * 1000
	}
	return toNum(n)
}

func fxCode(sym string) string {
	switch strings.ToLower(sym) {
	case "€", "eur":
		return "EUR"
	case "£", "gbp":
		return "GBP"
	}
	return "USD"
}

// inrPeriod decides whether a rupee figure is monthly (x12) or yearly (x1).
func inrPeriod(line string, amount float64) float64 {
	switch {
	case monthRe.MatchString(line):
		return 12
	case yearRe.MatchString(line):
		return 1
	case amount < monthlyCeiling:
		return 12
	}
	return 1
}

func fxPeriod(line string) float64 {
	switch {
	case hourRe.MatchString(line):
		return 2080
	case monthRe.MatchString(line):
		return 12
	}
	return 1
}

// fxPlausible rejects perks like "$500 learning budget": a yearly foreign
// figure must be at least 10,000 unless the line says hourly or monthly.
func fxPlausible(line string, amount float64) bool {
	switch {
	case hourRe.MatchString(line):
		return amount >= 5 && amount <= 500
	case monthRe.MatchString(line):
		return amount >= 300
	}
	return amount >= 10_000
}
