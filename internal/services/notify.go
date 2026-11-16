package services

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"jobradar/internal/app"
	"jobradar/internal/llm"
	"jobradar/internal/model"
	"jobradar/internal/pipeline"
	"jobradar/internal/score"
)

// afterMatch runs the two optional extras for a new posting that passes the
// filters: an LLM second opinion on fit, and a Telegram alert. Both are off
// unless configured in .env and switched on in Settings, and neither can
// fail the pipeline.
type afterMatch struct {
	d   *app.Deps
	llm *llm.Client
}

func newAfterMatch(d *app.Deps) *afterMatch {
	return &afterMatch{d: d, llm: llm.New(d.Cfg.LLMBaseURL, d.Cfg.LLMAPIKey, d.Cfg.LLMModel)}
}

func (a *afterMatch) Run(ctx context.Context, jobID int64, m model.JobFound, v pipeline.Verdict) (fit int) {
	fit = v.FitScore
	if a.llm == nil && a.d.Cfg.TelegramToken == "" {
		return fit
	}
	s, err := score.LoadSettings(ctx, a.d.Store)
	if err != nil {
		return fit
	}
	// Only postings the rules already like are worth an LLM call.
	if a.llm != nil && s.LLMEnabled && strings.TrimSpace(s.ResumeText) != "" && v.FitScore >= 50 {
		lctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		opinion, err := a.llm.ScoreFit(lctx, s.ResumeText, m.Job.Title, m.Job.Location, m.Job.Description)
		cancel()
		if err != nil {
			a.d.Log.Warn("llm fit failed", "job", jobID, "err", err)
		} else if blended, err := a.d.Store.SetLLMScore(ctx, jobID, opinion.Score, opinion.Reason); err == nil {
			fit = blended
		}
	}
	if a.d.Cfg.TelegramToken != "" && a.d.Cfg.TelegramChatID != "" && s.TelegramEnabled && fit >= s.TelegramMin {
		if first, err := a.d.Store.ClaimAlert(ctx, jobID); err == nil && first {
			text := fmt.Sprintf("New match (fit %d)\n%s at %s\n%s\n%s", fit, m.Job.Title, m.Company, m.Job.Location, m.Job.URL)
			if err := sendTelegram(ctx, a.d.Cfg.TelegramToken, a.d.Cfg.TelegramChatID, text); err != nil {
				a.d.Log.Warn("telegram alert failed", "err", err)
			}
		}
	}
	return fit
}

// sendTelegram posts one message through the Bot API.
func sendTelegram(ctx context.Context, token, chatID, text string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	form := url.Values{"chat_id": {chatID}, "text": {text}, "disable_web_page_preview": {"true"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.telegram.org/bot"+token+"/sendMessage", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("telegram request failed") // the URL holds the token: never log the raw error
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("telegram http %d", res.StatusCode)
	}
	return nil
}
