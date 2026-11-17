package pipeline

import (
	"context"
	"fmt"
	"strings"

	"jobradar/internal/boards"
	"jobradar/internal/extract"
	"jobradar/internal/model"
	"jobradar/internal/store"
)

// Start registers whatever a task needs before it is published. A site
// crawl's first page must be in the frontier so its completion is counted.
func Start(ctx context.Context, st *store.Store, t model.FetchTask) error {
	if t.Kind == model.KindSitePage || t.Kind == model.KindWorkday {
		return st.SeedCrawlPage(ctx, t.CrawlID, t.URL)
	}
	return nil
}

// sitePage handles one page of a whole-site crawl:
//
//  1. the page loads a known job board: switch the company to that board's
//     feed and stop crawling HTML
//  2. the page is an empty JavaScript shell: send it to the renderer
//  3. otherwise extract postings (structured data, then HTML heuristics,
//     then the optional LLM) and follow job-looking links on the same site
//
// The crawl is complete when no claimed page is left unprocessed.
func (e *Extractor) sitePage(ctx context.Context, f model.Fetched) (Extracted, error) {
	t := f.Task
	var out Extracted
	var links []store.SiteLink
	failure := ""

	switch {
	case f.Blocked != "":
		failure = f.Blocked
		if t.Depth == 0 {
			// The careers page itself is off limits: mark the company "manual".
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
		page := extract.Parse(body, t.URL)
		if page.Board == "" {
			if b, tok, ok := boards.Detect(f.FinalURL); ok { // the careers URL redirected to a board
				page.Board, page.BoardTok = b, tok
			}
		}
		if page.Board != "" {
			feed, err := boards.FeedURL(page.Board, page.BoardTok)
			if err == nil {
				if err := e.Store.UpdateCompany(ctx, t.CompanyID, store.CompanyPatch{BoardType: &page.Board, BoardToken: &page.BoardTok}); err != nil {
					// Another company already owns this board: carry on as a plain site crawl.
					e.Log.Info("board already belongs to another company", "board", page.Board, "token", page.BoardTok)
				} else {
					if page.Board == model.BoardWorkday {
						// Workday is crawled page by page through this same frontier;
						// its first page must be claimed before this one is finished.
						if err := e.Store.SeedCrawlPage(ctx, t.CrawlID, feed); err != nil {
							return out, err
						}
					}
					if _, err := e.Store.FinishSitePage(ctx, t.CrawlID, t.URL, 0, nil, extract.MaxPages); err != nil {
						return out, err
					}
					next := t
					next.URL, next.Kind, next.BoardType, next.Depth, next.Render, next.Force = feed, model.KindBoardFeed, page.Board, 0, false, true
					if page.Board == model.BoardWorkday {
						next.Kind, next.Force = model.KindWorkday, t.Force
					}
					out.Tasks = []model.FetchTask{next}
					out.Note = fmt.Sprintf("board detected: %s (%s)", page.Board, page.BoardTok)
					return out, nil // the feed task closes this crawl
				}
			}
		}
		if !t.Render && extract.LooksEmpty(page, body) && e.CanRender != nil && e.CanRender() {
			next := t
			next.Render = true
			out.Render = &next
			out.Note = "no content over plain HTTP, sending to the renderer"
			return out, nil // the page stays unprocessed until the rendered copy comes back
		}
		if !t.Render && extract.LooksEmpty(page, body) {
			out.Note = "page needs JavaScript and the renderer is not running (docker compose --profile render up -d)"
		}
		out.Jobs = page.Jobs
		if len(out.Jobs) == 0 {
			if j, ok := extract.HeuristicJob(page, t.URL); ok {
				out.Jobs = []model.RawJob{j}
			} else if e.LLM != nil {
				if j, ok := e.LLM(ctx, page, t.URL); ok {
					out.Jobs = []model.RawJob{j}
				}
			}
		}
		// By-product of crawling: a link to another company's job board is a
		// company we may not know yet. Queue it for discovery.
		for _, l := range page.Links {
			if b, tok, ok := boards.Detect(l.URL); ok && !extract.SameSite(l.URL, t.URL) {
				e.Store.AddCandidate(ctx, store.Candidate{Name: boards.DisplayName(b, tok), BoardType: b, BoardToken: tok, Source: "outbound", Detail: "linked from " + t.URL})
			}
		}
		follow := extract.Follow(page, t.URL, t.Depth)
		// The careers page hands off to a hosted hiring site (Keka, Workday, ...):
		// continue the crawl there, and start there next time.
		if t.Depth == 0 && len(out.Jobs) == 0 {
			if hosted := extract.HostedCareers(body); hosted != "" && !extract.SameSite(hosted, t.URL) {
				follow = append(follow, hosted)
				e.Store.UpdateCompany(ctx, t.CompanyID, store.CompanyPatch{CareersURL: &hosted})
				out.Note = "careers are hosted at " + hosted
			}
		}
		// A careers page that shows neither a posting nor a link to one over
		// plain HTTP usually fills its job list with JavaScript: render it once.
		if !t.Render && t.Depth <= 1 && len(out.Jobs) == 0 && len(follow) == 0 && e.CanRender != nil && e.CanRender() {
			next := t
			next.Render = true
			return Extracted{Render: &next, Note: "careers page lists nothing over plain HTTP, sending to the renderer"}, nil
		}
		for _, u := range follow {
			links = append(links, store.SiteLink{URL: u, Depth: t.Depth + 1})
		}
	}

	res, err := e.Store.FinishSitePage(ctx, t.CrawlID, t.URL, len(out.Jobs), links, extract.MaxPages)
	if err != nil {
		return out, err
	}
	for _, l := range res.Follow {
		next := t
		next.URL, next.Depth, next.Render = l.URL, l.Depth, false
		out.Tasks = append(out.Tasks, next)
	}
	if failure != "" {
		out.Note = failure
	} else if strings.HasPrefix(out.Note, "page needs JavaScript") && (len(out.Jobs) > 0 || len(out.Tasks) > 0) {
		out.Note = "" // the page yielded something after all
	}
	if res.Remaining == 0 {
		done := &model.CrawlDone{CrawlID: t.CrawlID, CompanyID: t.CompanyID, Company: t.Company, SearchID: t.SearchID, JobCount: res.TotalJobs}
		if res.Pages <= 1 && failure != "" {
			done.Failed = "careers page: " + failure
		}
		out.Done = done
	}
	return out, nil
}
