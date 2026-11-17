package pipeline

import (
	"context"
	"fmt"
	"sort"

	"jobradar/internal/boards"
	"jobradar/internal/model"
	"jobradar/internal/store"
)

// workdayPage handles one page of a paged board (Workday, Darwinbox,
// SmartRecruiters; see boards.Paged). These have no single feed: list pages (20 postings each) name the postings, and each posting's
// description is a separate request. The crawl uses the site-crawl frontier:
//
//   - each list page claims the list page after it
//   - each list page claims the postings it has not stored yet; postings
//     already stored are only marked as seen, so a repeat crawl of an
//     unchanged site costs the list pages and nothing else
//   - a posting page yields one job
//
// The crawl is complete when no claimed page is left unprocessed.
func (e *Extractor) workdayPage(ctx context.Context, f model.Fetched) (Extracted, error) {
	t := f.Task
	var out Extracted
	var links []store.SiteLink
	isList := boards.IsListPage(t.BoardType, t.URL)
	jobs, failure := 0, ""

	switch {
	case f.Blocked != "":
		failure = f.Blocked
		if isList && t.Depth == 0 {
			if err := e.Store.MarkManual(ctx, t.CompanyID, f.Blocked); err != nil {
				return out, err
			}
		}
	case f.Failed != "":
		failure = f.Failed
	case f.Status != 200:
		failure = fmt.Sprintf("http %d", f.Status)
	default:
		body, _, err := e.Store.PageBody(ctx, f.PageID)
		if err != nil {
			return out, err
		}
		if !isList {
			job, err := boards.ParseJobPage(t.BoardType, body, t.URL)
			if err != nil {
				failure = "posting did not parse: " + err.Error()
				break
			}
			out.Jobs, jobs = []model.RawJob{job}, 1
			break
		}
		list, err := boards.ParseListPage(t.BoardType, body, t.URL, e.WorkdayCountry)
		if err != nil {
			failure = "list did not parse: " + err.Error()
			break
		}
		if list.Refine != "" {
			links = []store.SiteLink{{URL: list.Refine, Depth: 0}}
			if out.Note = list.Note; out.Note == "" {
				out.Note = "listing narrowed to " + e.WorkdayCountry
			}
			break
		}
		known := map[string]bool{}
		if !t.Force {
			urls := make([]string, len(list.Postings))
			for i, p := range list.Postings {
				urls[i] = p.URL
			}
			if known, err = e.Store.TouchJobs(ctx, t.CompanyID, t.CrawlID, urls); err != nil {
				return out, err
			}
		}
		for _, p := range list.Postings {
			if known[p.URL] {
				jobs++
				continue
			}
			links = append(links, store.SiteLink{URL: p.DetailURL, Depth: 2})
		}
		if list.Next != "" {
			links = append(links, store.SiteLink{URL: list.Next, Depth: 1})
		}
	}

	res, err := e.Store.FinishSitePage(ctx, t.CrawlID, t.URL, jobs, links, boards.MaxPagedPages)
	if err != nil {
		return out, err
	}
	// Postings first, the next list page last: the queue is first in, first
	// out, so the next page (and the work it brings) waits behind this page's.
	sort.SliceStable(res.Follow, func(i, j int) bool {
		return !boards.IsListPage(t.BoardType, res.Follow[i].URL) && boards.IsListPage(t.BoardType, res.Follow[j].URL)
	})
	for _, l := range res.Follow {
		next := t
		next.URL, next.Depth, next.Render = l.URL, l.Depth, false
		out.Tasks = append(out.Tasks, next)
	}
	if failure != "" {
		out.Note = failure
	}
	if res.Remaining == 0 {
		done := &model.CrawlDone{CrawlID: t.CrawlID, CompanyID: t.CompanyID, Company: t.Company, SearchID: t.SearchID, JobCount: res.TotalJobs}
		if failure != "" && res.TotalJobs == 0 {
			done.Failed = t.BoardType + ": " + failure // nothing was read: close nothing
		}
		out.Done = done
	}
	return out, nil
}
