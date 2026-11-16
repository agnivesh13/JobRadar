// Package score decides, for each posting, its experience level, salary
// state, whether it passes the filters, and a 0-100 fit score.
package score

// Settings are the user-editable filters, stored as one JSON document in the
// settings table under the key "settings".
type Settings struct {
	SalaryFloorLPA  float64  `json:"salary_floor_lpa"` // yearly floor for full-time roles, in lakhs
	StipendFloor    int64    `json:"stipend_floor"`    // monthly floor for internships, in rupees
	IncludeKeywords []string `json:"include_keywords"` // a title must contain one of these
	ExcludeKeywords []string `json:"exclude_keywords"` // a title must contain none of these
	Experience      []string `json:"experience"`       // allowed levels: intern, new_grad, junior (0-2 yrs), mid, senior
	Locations       []string `json:"locations"`        // any match passes; "anywhere" disables the filter
	GradYear        int      `json:"grad_year"`        // postings naming another batch are flagged
	HideOtherBatch  bool     `json:"hide_other_batch"` // drop flagged postings instead of just flagging
	ShowUnknown     bool     `json:"show_unknown"`     // keep the "salary unknown" tab
	ResumeText      string   `json:"resume_text"`      // used for the skill-overlap part of the fit score
	LLMEnabled      bool     `json:"llm_enabled"`      // optional LLM pass (needs LLM_BASE_URL)
	TelegramEnabled bool     `json:"telegram_enabled"` // alert on fit score above TelegramMinScore
	TelegramMin     int      `json:"telegram_min_score"`
}

// Defaults answer the plan's open questions for a fresher graduating in 2027:
// software, backend, ML, data and quant roles; intern, new-grad and 0-2 year
// postings; Hyderabad, Bangalore or remote in India; 10 LPA and a 40,000
// monthly stipend floor; LLM off.
func Defaults() Settings {
	return Settings{
		SalaryFloorLPA: 10,
		StipendFloor:   40_000,
		IncludeKeywords: []string{
			"software engineer", "software developer", "software development", "sde", "swe", "developer",
			"backend", "back end", "back-end", "full stack", "fullstack", "full-stack", "frontend", "front end",
			"engineer", "engineering", "programmer", "member of technical staff", "mts",
			"machine learning", "ml", "ai", "data scientist", "data engineer", "data analyst", "data science",
			"quant", "quantitative", "trading", "researcher", "research engineer", "research scientist", "product analyst",
			"devops", "sre", "site reliability", "platform", "infrastructure", "cloud",
		},
		ExcludeKeywords: []string{
			"senior", "sr", "staff", "principal", "lead", "manager", "director", "head", "vp", "vice president",
			"architect", "chief", "president", "sales", "account executive", "recruiter", "talent", "marketing",
			"customer success", "customer support", "legal", "counsel", "accountant", "hr", "designer",
			"collections", "audit", "compliance", "copywriter", "design", "ux", "ui ux", "product management",
			"product manager", "operations", "ops", "consultant", "support", "insurance", "grc", "it systems", "helpdesk",
		},
		Experience: []string{"intern", "new_grad", "junior"},
		Locations:  []string{"hyderabad", "bangalore", "remote india"},
		GradYear:   2027,
		// A posting open only to another batch is one this profile cannot apply to.
		HideOtherBatch: true,
		ShowUnknown:    true,
		TelegramMin:    80,
	}
}

// normalised fills gaps so a partially saved document still behaves.
func (s Settings) normalised() Settings {
	d := Defaults()
	if s.SalaryFloorLPA <= 0 {
		s.SalaryFloorLPA = d.SalaryFloorLPA
	}
	if s.StipendFloor <= 0 {
		s.StipendFloor = d.StipendFloor
	}
	if len(s.Experience) == 0 {
		s.Experience = d.Experience
	}
	if len(s.Locations) == 0 {
		s.Locations = []string{"anywhere"}
	}
	if s.GradYear == 0 {
		s.GradYear = d.GradYear
	}
	if s.TelegramMin == 0 {
		s.TelegramMin = d.TelegramMin
	}
	return s
}
