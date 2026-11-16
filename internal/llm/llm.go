// Package llm is an optional helper for the two places the plan allows a
// language model: reading a posting off a page nothing else understood, and
// a second opinion on fit. It talks to any OpenAI-compatible endpoint (Groq,
// Ollama, ...). JobRadar works fully with it switched off.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL string // e.g. https://api.groq.com/openai/v1 or http://ollama:11434/v1
	APIKey  string
	Model   string
	HTTP    *http.Client
}

// New returns nil when no endpoint is configured, and every caller treats a
// nil client as "LLM off".
func New(baseURL, apiKey, model string) *Client {
	if strings.TrimSpace(baseURL) == "" || strings.TrimSpace(model) == "" {
		return nil
	}
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), APIKey: apiKey, Model: model, HTTP: &http.Client{Timeout: 60 * time.Second}}
}

// JSON sends one system+user exchange and decodes the model's JSON reply into out.
func (c *Client) JSON(ctx context.Context, system, user string, out any) error {
	if c == nil {
		return errors.New("llm not configured")
	}
	body, _ := json.Marshal(map[string]any{
		"model":           c.Model,
		"temperature":     0,
		"response_format": map[string]string{"type": "json_object"},
		"messages": []map[string]string{
			{"role": "system", "content": system},
			{"role": "user", "content": user},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("llm http %d: %.200s", res.StatusCode, raw)
	}
	var reply struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(raw, &reply); err != nil || len(reply.Choices) == 0 {
		return fmt.Errorf("llm: unexpected reply: %.200s", raw)
	}
	content := strings.TrimSpace(reply.Choices[0].Message.Content)
	// Some models wrap JSON in a code fence despite being told not to.
	content = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(content, "```json"), "```"), "```")
	return json.Unmarshal([]byte(strings.TrimSpace(content)), out)
}

// Posting is what ExtractPosting reads off a page.
type Posting struct {
	IsJob    bool   `json:"is_job"`
	Title    string `json:"title"`
	Location string `json:"location"`
	Salary   string `json:"salary_sentence"` // quoted from the page, "" if none
}

// ExtractPosting asks whether a page is a single job posting and, if so, for
// its title, location and the exact sentence stating pay.
func (c *Client) ExtractPosting(ctx context.Context, pageText string) (Posting, error) {
	var p Posting
	if len(pageText) > 12_000 {
		pageText = strings.ToValidUTF8(pageText[:12_000], "")
	}
	err := c.JSON(ctx,
		`You read the text of one web page from a company's careers site. Reply with JSON only: {"is_job": boolean, "title": string, "location": string, "salary_sentence": string}. `+
			`is_job is true only if the page describes exactly one open position. salary_sentence must be copied word for word from the page, or "" if the page states no pay. Never guess.`,
		pageText, &p)
	return p, err
}

// Fit is the LLM's opinion of a posting against a resume.
type Fit struct {
	Score  int    `json:"score"`  // 0-100
	Reason string `json:"reason"` // one line
}

func (c *Client) ScoreFit(ctx context.Context, resume, title, location, description string) (Fit, error) {
	var f Fit
	if len(description) > 8_000 {
		description = strings.ToValidUTF8(description[:8_000], "")
	}
	if len(resume) > 6_000 {
		resume = strings.ToValidUTF8(resume[:6_000], "")
	}
	err := c.JSON(ctx,
		`You judge how well a job posting fits a candidate. Reply with JSON only: {"score": integer 0-100, "reason": string}. `+
			`Score high only when the role's level matches the candidate's experience and the required skills overlap. reason is one short sentence.`,
		"RESUME:\n"+resume+"\n\nPOSTING: "+title+" ("+location+")\n"+description, &f)
	f.Score = max(0, min(100, f.Score))
	return f, err
}
