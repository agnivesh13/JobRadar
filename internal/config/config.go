// Package config loads all runtime configuration from environment variables.
// Everything has a default so `jobradar <cmd>` works with an empty environment.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	DatabaseURL string
	NatsURL     string
	HTTPAddr    string // api listen address
	HealthAddr  string // worker health listen address
	WebDir      string // built React app

	CrawlerContact string        // contact address put in the User-Agent
	DomainInterval time.Duration // minimum gap between requests to one domain (all workers together)
	FetchTimeout   time.Duration
	MaxBodyBytes   int64
	BodyRetention  time.Duration // raw page bodies are deleted after this

	FetchConcurrency   int // parallel fetches per fetcher container
	ExtractConcurrency int
	ScoreConcurrency   int

	USDToINR float64
	EURToINR float64
	GBPToINR float64

	RendererURL  string // base URL of the renderer service ("" = rendering disabled)
	RendererTabs int
	SearxngURL   string // optional self-hosted search

	WorkdayCountry string // Workday crawls are narrowed to this country when the site offers the filter ("" = all)

	LLMBaseURL string // OpenAI-compatible endpoint (Groq, Ollama); "" = LLM off
	LLMAPIKey  string
	LLMModel   string

	TelegramToken  string
	TelegramChatID string
}

func Load() Config {
	return Config{
		DatabaseURL:        env("DATABASE_URL", "postgres://jobradar:jobradar@localhost:55432/jobradar?sslmode=disable"),
		NatsURL:            env("NATS_URL", "nats://localhost:4222"),
		HTTPAddr:           env("HTTP_ADDR", ":8080"),
		HealthAddr:         env("HEALTH_ADDR", ":8081"),
		WebDir:             env("WEB_DIR", "web/dist"),
		CrawlerContact:     env("CRAWLER_CONTACT", ""),
		DomainInterval:     time.Duration(envInt("DOMAIN_INTERVAL_MS", 1000)) * time.Millisecond,
		FetchTimeout:       time.Duration(envInt("FETCH_TIMEOUT_S", 120)) * time.Second,
		MaxBodyBytes:       int64(envInt("MAX_BODY_MB", 64)) << 20,
		BodyRetention:      time.Duration(envInt("BODY_RETENTION_DAYS", 7)) * 24 * time.Hour,
		FetchConcurrency:   envInt("FETCH_CONCURRENCY", 4),
		ExtractConcurrency: envInt("EXTRACT_CONCURRENCY", 2),
		ScoreConcurrency:   envInt("SCORE_CONCURRENCY", 4),
		USDToINR:           envFloat("USD_TO_INR", 88),
		EURToINR:           envFloat("EUR_TO_INR", 96),
		GBPToINR:           envFloat("GBP_TO_INR", 112),
		RendererURL:        env("RENDERER_URL", ""),
		RendererTabs:       envInt("RENDERER_TABS", 2),
		SearxngURL:         env("SEARXNG_URL", ""),
		WorkdayCountry:     env("WORKDAY_COUNTRY", "India"),
		LLMBaseURL:         env("LLM_BASE_URL", ""),
		LLMAPIKey:          env("LLM_API_KEY", ""),
		LLMModel:           env("LLM_MODEL", ""),
		TelegramToken:      env("TELEGRAM_TOKEN", ""),
		TelegramChatID:     env("TELEGRAM_CHAT_ID", ""),
	}
}

// UserAgent identifies the crawler honestly, with a contact address when one is configured.
func (c Config) UserAgent() string {
	if c.CrawlerContact == "" {
		return "JobRadar/0.1 (personal job-search crawler)"
	}
	return fmt.Sprintf("JobRadar/0.1 (personal job-search crawler; contact: %s)", c.CrawlerContact)
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if n, err := strconv.Atoi(env(key, "")); err == nil {
		return n
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if f, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return f
	}
	return def
}
