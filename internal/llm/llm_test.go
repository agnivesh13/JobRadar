package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewIsNilWhenUnconfigured(t *testing.T) {
	if New("", "key", "model") != nil || New("http://x", "", "") != nil {
		t.Fatal("an unconfigured client must be nil, which means LLM off")
	}
	if _, err := (*Client)(nil).ExtractPosting(context.Background(), "x"); err == nil {
		t.Fatal("a nil client must return an error, not panic")
	}
}

func TestExtractPosting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), "Backend Engineer") {
			t.Errorf("page text not sent: %s", body)
		}
		// Reply wrapped in a code fence, as some models do.
		content := "```json\n{\"is_job\":true,\"title\":\"Backend Engineer\",\"location\":\"Hyderabad\",\"salary_sentence\":\"CTC 12-18 LPA\"}\n```"
		json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": content}}}})
	}))
	defer srv.Close()

	p, err := New(srv.URL, "k", "m").ExtractPosting(context.Background(), "Backend Engineer at Acme. CTC 12-18 LPA.")
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsJob || p.Title != "Backend Engineer" || p.Location != "Hyderabad" || p.Salary != "CTC 12-18 LPA" {
		t.Errorf("unexpected posting: %+v", p)
	}
}

func TestErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "rate limited", 429) }))
	defer srv.Close()
	if _, err := New(srv.URL, "", "m").ScoreFit(context.Background(), "r", "t", "l", "d"); err == nil {
		t.Fatal("a non-200 reply must be an error")
	}
}
