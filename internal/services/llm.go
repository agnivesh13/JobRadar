package services

import (
	"context"
	"strings"

	"jobradar/internal/app"
	"jobradar/internal/extract"
	"jobradar/internal/llm"
	"jobradar/internal/model"
	"jobradar/internal/score"
)

// llmExtractor returns the last-resort page reader, or nil when no LLM is
// configured. It also honours the "LLM" switch in Settings at call time.
func llmExtractor(d *app.Deps) func(ctx context.Context, p extract.Page, pageURL string) (model.RawJob, bool) {
	client := llm.New(d.Cfg.LLMBaseURL, d.Cfg.LLMAPIKey, d.Cfg.LLMModel)
	if client == nil {
		return nil
	}
	return func(ctx context.Context, p extract.Page, pageURL string) (model.RawJob, bool) {
		// Only pages that could plausibly be one posting are worth a call.
		if len(p.Text) < 400 || p.H1 == "" {
			return model.RawJob{}, false
		}
		if s, err := score.LoadSettings(ctx, d.Store); err != nil || !s.LLMEnabled {
			return model.RawJob{}, false
		}
		post, err := client.ExtractPosting(ctx, p.H1+"\n"+p.Text)
		if err != nil {
			d.Log.Warn("llm extraction failed", "url", pageURL, "err", err)
			return model.RawJob{}, false
		}
		if !post.IsJob || strings.TrimSpace(post.Title) == "" {
			return model.RawJob{}, false
		}
		desc := p.Text
		// Keep the quoted pay sentence only if it really is on the page.
		if post.Salary != "" && strings.Contains(p.Text, post.Salary) {
			desc = post.Salary + "\n" + desc
		}
		return model.RawJob{URL: pageURL, Title: strings.TrimSpace(post.Title), Location: strings.TrimSpace(post.Location),
			Remote: strings.Contains(strings.ToLower(post.Location), "remote"), Description: desc}, true
	}
}
