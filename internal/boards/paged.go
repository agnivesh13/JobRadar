package boards

import (
	"fmt"
	"net/url"

	"jobradar/internal/model"
)

// Paged boards have no single feed: list pages name the postings and each
// posting's description is one more request. They are crawled through the
// site-crawl frontier (pipeline.pagedPage); this file is the one place that
// knows which board reads which page.

// ListPage is what one list page of a paged board yields.
type ListPage = WorkdayList

// Posting is one row of a list page.
type Posting = WorkdayPosting

// MaxPagedPages bounds one crawl of a paged board.
const MaxPagedPages = WorkdayMaxPages

// Paged reports whether a board is crawled as list pages plus posting pages.
func Paged(boardType string) bool {
	switch boardType {
	case model.BoardWorkday, model.BoardDarwinbox, model.BoardSmartRecruiters:
		return true
	}
	return false
}

// IsListPage reports whether a task URL is a list page (as opposed to one posting).
func IsListPage(boardType, taskURL string) bool {
	u, err := url.Parse(taskURL)
	if err != nil {
		return false
	}
	switch boardType {
	case model.BoardDarwinbox:
		return isDarwinboxList(u)
	case model.BoardSmartRecruiters:
		return isSmartRecruitersList(u)
	}
	return IsWorkdayList(taskURL)
}

// ParseListPage reads one list page. country ("" = no filter) narrows the
// crawl to one country on boards that can filter.
func ParseListPage(boardType string, body []byte, taskURL, country string) (ListPage, error) {
	u, err := url.Parse(taskURL)
	if err != nil {
		return ListPage{}, err
	}
	switch boardType {
	case model.BoardDarwinbox:
		return parseDarwinboxList(body, u)
	case model.BoardSmartRecruiters:
		return parseSmartRecruitersList(body, u, country)
	case model.BoardWorkday, "":
		return ParseWorkdayList(body, taskURL, country)
	}
	return ListPage{}, fmt.Errorf("board type %q has no list pages", boardType)
}

// ParseJobPage reads one posting page.
func ParseJobPage(boardType string, body []byte, taskURL string) (model.RawJob, error) {
	u, err := url.Parse(taskURL)
	if err != nil {
		return model.RawJob{}, err
	}
	switch boardType {
	case model.BoardDarwinbox:
		return parseDarwinboxJob(body, u)
	case model.BoardSmartRecruiters:
		return parseSmartRecruitersJob(body, u)
	case model.BoardWorkday, "":
		return ParseWorkdayJob(body, taskURL)
	}
	return model.RawJob{}, fmt.Errorf("board type %q has no posting pages", boardType)
}
