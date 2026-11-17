package score

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Experience levels.
const (
	Intern  = "intern"
	NewGrad = "new_grad"
	Junior  = "junior" // up to 2 years
	Mid     = "mid"    // 3 to 5 years
	Senior  = "senior"
)

// Salary states.
const (
	Above   = "above"
	Below   = "below"
	Unknown = "unknown"
)

var (
	internRe      = regexp.MustCompile(`(?i)\b(intern|interns|internship|apprentice|co-?op|summer analyst)\b`)
	seniorRe      = regexp.MustCompile(`(?i)\b(senior|sr|staff|principal|lead|manager|director|head|vp|vice president|architect|chief|distinguished|iii|iv|l[5-9])\b`)
	midRe         = regexp.MustCompile(`(?i)\b(ii|sde[- ]?2|sde[- ]?ii|swe[- ]?2|l4|mid[- ]level)\b`)
	newGradRe     = regexp.MustCompile(`(?i)\b(new grad(uate)?s?|graduate|fresher|freshers|entry[- ]level|campus|trainee|university|early[- ]career|early talent|associate software|sde[- ]?1|sde[- ]?i|swe[- ]?1|20(2[6-9]))\b`)
	descNewGradRe = regexp.MustCompile(`(?i)\b(new grad(uate)?s?|recent graduates?|freshers?|entry[- ]level|campus hir\w+|early[- ]career|no prior experience)\b`)
	// "3-5 years", "2+ years", "at least 4 yrs"
	yearsRe = regexp.MustCompile(`(?i)(\d{1,2})\s*\+?\s*(?:(?:-|–|—|to)\s*(\d{1,2})\s*\+?\s*)?(?:years?|yrs?)`)
	expWord = regexp.MustCompile(`(?i)\b(experience|exp|experienced|professional|industry|hands-on|working)\b`)
)

// Classify works out the experience level a posting asks for, from the
// title first and the description's "N years" second. "" means unknown.
func Classify(title, employmentType, description string) string {
	switch {
	case internRe.MatchString(title), internRe.MatchString(employmentType):
		return Intern
	case seniorRe.MatchString(title):
		return Senior
	case midRe.MatchString(title):
		return Mid
	case newGradRe.MatchString(title):
		return NewGrad
	}
	// "Site Reliability Engineer (4 to 8 Years)": years in the title need no
	// "experience" word next to them; the lower bound decides.
	if m := yearsRe.FindStringSubmatch(title); m != nil {
		if n, _ := strconv.Atoi(m[1]); n <= 20 {
			return levelForYears(n)
		}
	}
	if years, ok := minYears(description); ok {
		return levelForYears(years)
	}
	if descNewGradRe.MatchString(description) {
		return NewGrad
	}
	return ""
}

func levelForYears(years int) string {
	switch {
	case years == 0:
		return NewGrad
	case years <= 2:
		return Junior
	case years <= 5:
		return Mid
	default:
		return Senior
	}
}

// minYears returns the lowest "years of experience" figure the text asks for.
func minYears(text string) (int, bool) {
	best, found := 0, false
	for _, line := range strings.Split(text, "\n") {
		if !expWord.MatchString(line) {
			continue
		}
		for _, m := range yearsRe.FindAllStringSubmatch(line, -1) {
			n, _ := strconv.Atoi(m[1])
			if n > 20 {
				continue
			}
			if !found || n > best {
				// A posting that lists several requirements ("2+ years of Go",
				// "5+ years overall") is as senior as its largest minimum.
				best, found = n, true
			}
		}
	}
	return best, found
}

// Input is everything about a posting that scoring looks at.
type Input struct {
	Title       string
	Location    string
	Remote      bool
	Description string
	Experience  string
	SalaryMin   *int64 // rupees per year
	SalaryMax   *int64
}

// Signal is what is known about the company, used when a posting states no pay.
type Signal struct {
	PayTier        string // high | medium | low | unknown (set by hand)
	FundingStage   string
	PaysAboveFloor bool // another posting of this company stated pay at or above the floor
}

type Result struct {
	SalaryState string
	Matched     bool
	Flags       []string
	FitScore    int
	FitReason   string
}

// Evaluate applies the filters and computes the fit score. It is a pure
// function of its inputs, so re-scoring after a settings change is just
// calling it again.
func Evaluate(in Input, s Settings, sig Signal) Result {
	s = s.normalised()
	res := Result{Flags: []string{}}
	title := " " + normalise(in.Title) + " "

	// --- salary state ---
	res.SalaryState = SalaryState(in.Experience, in.SalaryMax, s)

	// --- filters ---
	roleOK := len(s.IncludeKeywords) == 0 || containsAny(title, s.IncludeKeywords)
	excluded := containsAny(title, s.ExcludeKeywords)
	expOK := in.Experience == "" || slices.Contains(s.Experience, in.Experience)
	locScore, locOK := matchLocation(in.Location, in.Remote, s.Locations)
	batchOK := true
	if years := batchYears(in.Title + "\n" + in.Description); len(years) > 0 && !slices.Contains(years, s.GradYear) {
		res.Flags = append(res.Flags, "batch_mismatch")
		batchOK = !s.HideOtherBatch
	}
	res.Matched = roleOK && !excluded && expOK && locOK && batchOK

	// --- fit score: skills 40, experience 25, location 15, salary 20 ---
	skillPts, skillNote := skillScore(in, s, roleOK)
	expPts := map[string]int{Intern: 25, NewGrad: 25, Junior: 20, "": 12, Mid: 5, Senior: 0}[in.Experience]
	if !expOK {
		expPts = min(expPts, 5)
	}
	salPts, salNote := 0, "salary below floor"
	switch res.SalaryState {
	case Above:
		salPts, salNote = 20, "salary at or above floor"
	case Unknown:
		switch {
		case sig.PayTier == "high":
			salPts, salNote = 15, "salary unknown, company pay tier high"
		case sig.PaysAboveFloor:
			salPts, salNote = 14, "salary unknown, company pays above floor elsewhere"
		case sig.PayTier == "medium":
			salPts, salNote = 10, "salary unknown, company pay tier medium"
		case sig.PayTier == "low":
			salPts, salNote = 3, "salary unknown, company pay tier low"
		case lateStage(sig.FundingStage):
			salPts, salNote = 10, "salary unknown, well-funded company"
		default:
			salPts, salNote = 8, "salary unknown"
		}
	}
	total := skillPts + expPts + locScore + salPts
	if slices.Contains(res.Flags, "batch_mismatch") {
		total -= 30
	}
	if excluded || !roleOK {
		total = min(total, 30)
	}
	res.FitScore = max(0, min(100, total))

	level := in.Experience
	if level == "" {
		level = "level not stated"
	}
	res.FitReason = fmt.Sprintf("%s; %s; %s", skillNote, strings.ReplaceAll(level, "_", " "), salNote)
	return res
}

// SalaryState compares a normalised yearly salary against the right floor:
// the monthly stipend floor for internships, the LPA floor otherwise. The
// top of a range decides, as the plan specifies.
func SalaryState(experience string, salaryMax *int64, s Settings) string {
	if salaryMax == nil || *salaryMax <= 0 {
		return Unknown
	}
	floor := int64(s.SalaryFloorLPA * 100_000)
	if experience == Intern {
		floor = s.StipendFloor * 12
	}
	if *salaryMax >= floor {
		return Above
	}
	return Below
}

var nonWord = regexp.MustCompile(`[^a-z0-9+#]+`)

func normalise(s string) string {
	return strings.TrimSpace(nonWord.ReplaceAllString(strings.ToLower(s), " "))
}

// containsAny reports whether the space-padded normalised text holds any
// keyword as a whole word or phrase.
func containsAny(padded string, keywords []string) bool {
	for _, k := range keywords {
		if k = normalisedKeyword(k); k != "" && strings.Contains(padded, " "+k+" ") {
			return true
		}
	}
	return false
}

var cityAliases = map[string][]string{
	"bangalore": {"bangalore", "bengaluru"},
	"bengaluru": {"bangalore", "bengaluru"},
	"gurgaon":   {"gurgaon", "gurugram"},
	"gurugram":  {"gurgaon", "gurugram"},
	"delhi":     {"delhi", "ncr", "noida", "gurgaon", "gurugram"},
	"mumbai":    {"mumbai", "bombay"},
}

// A remote posting counts as "remote India" only when it names India or a
// region containing it. A bare "Remote" from a US or EU company almost
// never means India, so it does not pass.
var broadRemote = regexp.MustCompile(`(?i)\b(india|apac|asia|global|globally|worldwide)\b`)

// matchLocation returns location points (0-15) and whether the filter passes.
func matchLocation(location string, remote bool, wanted []string) (int, bool) {
	loc := strings.ToLower(location)
	if strings.TrimSpace(loc) == "" {
		return 8, true // not stated: keep it, score it lower
	}
	anywhere := false
	for _, w := range wanted {
		w = strings.ToLower(strings.TrimSpace(w))
		switch {
		case w == "anywhere":
			anywhere = true
		case strings.HasPrefix(w, "remote"):
			isRemote := remote || strings.Contains(loc, "remote")
			if isRemote && broadRemote.MatchString(loc) {
				return 12, true
			}
		default:
			names := cityAliases[w]
			if names == nil {
				names = []string{w}
			}
			for _, n := range names {
				if strings.Contains(loc, n) {
					return 15, true
				}
			}
		}
	}
	if anywhere {
		return 6, true
	}
	return 0, false
}

var (
	batchRe  = regexp.MustCompile(`(?i)(?:batch|class of|graduat\w*|passing out|pass[- ]?outs?|passed out)[^.\n]{0,30}?(20[2-3]\d(?:\s*(?:/|,|&|and|or|-|–|to)\s*(?:20[2-3]\d|[2-3]\d))*)`)
	batchRe2 = regexp.MustCompile(`(?i)\b(20[2-3]\d)\s*(?:batch|graduates?|grads?|pass[- ]?outs?)\b`)
	yearRe   = regexp.MustCompile(`20[2-3]\d|\b[2-3]\d\b`)
	rangeRe  = regexp.MustCompile(`^(20[2-3]\d)\s*(?:-|–|to)\s*(20[2-3]\d|[2-3]\d)$`)
)

// batchYears lists the graduation years a posting names ("2026 batch",
// "graduating in 2025/26"). Empty means the posting names none.
func batchYears(text string) []int {
	var years []int
	for _, m := range batchRe2.FindAllStringSubmatch(text, -1) {
		years = append(years, toYear(m[1]))
	}
	for _, m := range batchRe.FindAllStringSubmatch(text, -1) {
		span := strings.TrimSpace(m[1])
		if r := rangeRe.FindStringSubmatch(span); r != nil { // "2024-2027" covers every year between
			lo, hi := toYear(r[1]), toYear(r[2])
			for y := lo; y <= hi && y-lo < 10; y++ {
				years = append(years, y)
			}
			continue
		}
		for _, y := range yearRe.FindAllString(span, -1) {
			years = append(years, toYear(y))
		}
	}
	return years
}

func toYear(s string) int {
	n, _ := strconv.Atoi(s)
	if n < 100 {
		n += 2000
	}
	return n
}

func lateStage(stage string) bool {
	s := strings.ToLower(stage)
	for _, k := range []string{"series b", "series c", "series d", "series e", "series f", "ipo", "public", "unicorn"} {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}

// keywordCache remembers the normalised form of each keyword: the same few
// dozen keywords are checked against every posting.
var keywordCache sync.Map

func normalisedKeyword(k string) string {
	if v, ok := keywordCache.Load(k); ok {
		return v.(string)
	}
	n := normalise(k)
	keywordCache.Store(k, n)
	return n
}
