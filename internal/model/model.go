// Package model holds the plain data types passed between stages.
package model

import "time"

// Board types.
const (
	BoardGreenhouse      = "greenhouse"
	BoardLever           = "lever"
	BoardAshby           = "ashby"
	BoardWorkday         = "workday"
	BoardKeka            = "keka"
	BoardDarwinbox       = "darwinbox"
	BoardSmartRecruiters = "smartrecruiters"
	BoardCustom          = "custom"
	BoardUnknown         = "unknown"
)

// Page kinds.
const (
	KindBoardFeed = "board_feed" // one JSON document with every job of a company
	KindSitePage  = "site_page"  // an HTML page on a company's own site
	KindWorkday   = "workday"    // a list page or posting of a paged board (Workday, Darwinbox, SmartRecruiters), crawled through the site frontier
)

type Company struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Domain        string     `json:"domain"`
	CareersURL    string     `json:"careers_url"`
	BoardType     string     `json:"board_type"`
	BoardToken    string     `json:"board_token"`
	PayTier       string     `json:"pay_tier"`
	Source        string     `json:"source"`
	FundingStage  string     `json:"funding_stage"`
	Status        string     `json:"status"`
	LastCrawledAt *time.Time `json:"last_crawled_at"`
	LastJobCount  *int       `json:"last_job_count"`
}

// Pay is a salary figure exactly as a source stated it, before normalisation.
type Pay struct {
	Min      float64 `json:"min"`
	Max      float64 `json:"max"`
	Currency string  `json:"currency"` // ISO code: INR, USD, ...
	Period   string  `json:"period"`   // year | month | hour
	Text     string  `json:"text"`     // the quote it came from
}

// RawJob is what an extractor produces: a posting as found, not yet scored.
type RawJob struct {
	URL            string     `json:"url"`
	Title          string     `json:"title"`
	Location       string     `json:"location"`
	Remote         bool       `json:"remote"`
	Department     string     `json:"department"`
	EmploymentType string     `json:"employment_type"` // free text from the source: "Intern", "full time", ...
	Description    string     `json:"description"`     // plain text
	PostedAt       *time.Time `json:"posted_at"`
	Pay            *Pay       `json:"pay"` // only when the source has a structured salary field
}

// FetchTask asks a fetcher to download one URL.
type FetchTask struct {
	CrawlID   string `json:"crawl_id"` // one crawl of one company
	CompanyID int64  `json:"company_id"`
	Company   string `json:"company"` // name, for the live view
	URL       string `json:"url"`
	Kind      string `json:"kind"`
	BoardType string `json:"board_type"`
	Priority  string `json:"priority"`  // high | normal
	Depth     int    `json:"depth"`     // link distance from the careers page (site crawls)
	SearchID  string `json:"search_id"` // set for deep-search crawls
	Render    bool   `json:"render"`    // fetch through headless Chrome
	Force     bool   `json:"force"`     // extract again even if the content hash is unchanged
}

// Fetched says a page was downloaded and stored.
type Fetched struct {
	Task      FetchTask `json:"task"`
	PageID    int64     `json:"page_id"`
	Status    int       `json:"status"`
	FinalURL  string    `json:"final_url"` // after redirects
	Hash      string    `json:"hash"`      // sha256 of the body
	Unchanged bool      `json:"unchanged"` // same content as the last fully processed fetch
	Failed    string    `json:"failed"`    // permanent failure that is not a block (e.g. body too large)
	Blocked   string    `json:"blocked"`   // non-empty when robots.txt or the site refused us
}

// JobFound carries one extracted posting to the scorer.
type JobFound struct {
	CrawlID   string `json:"crawl_id"`
	CompanyID int64  `json:"company_id"`
	Company   string `json:"company"`
	SearchID  string `json:"search_id"`
	Job       RawJob `json:"job"`
}

// CrawlDone closes one crawl of one company, so the scorer can detect
// postings that disappeared.
type CrawlDone struct {
	CrawlID   string `json:"crawl_id"`
	CompanyID int64  `json:"company_id"`
	Company   string `json:"company"`
	SearchID  string `json:"search_id"`
	PageID    int64  `json:"page_id"` // the feed page this crawl read (board crawls)
	Hash      string `json:"hash"`    // its content hash, stamped as "extracted" on success
	JobCount  int    `json:"job_count"`
	Unchanged bool   `json:"unchanged"` // feed identical to last time: every known job is still there
	Failed    string `json:"failed"`    // non-empty when the crawl did not complete; nothing is closed
}

// ScoreMsg is the one message type on the scorer's subject: a chunk of jobs
// or the end of a crawl. Sharing a subject keeps them in publish order.
//
// Jobs travel in chunks rather than one per message because the queue
// flushes to disk on every write: a crawl of 40,000 postings is a few
// thousand flushes this way instead of 80,000.
type ScoreMsg struct {
	Jobs []JobFound `json:"jobs,omitempty"`
	Job  *JobFound  `json:"job,omitempty"` // single-job form, still accepted
	Done *CrawlDone `json:"done,omitempty"`
}
