package ai

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

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotConfigured = errors.New("AI is not configured")

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Get(ctx context.Context) (*Settings, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT a.base_url, a.api_key, a.model, a.updated_at, COALESCE(u.name, '')
		FROM ai_settings a
		LEFT JOIN users u ON u.id = a.updated_by
		WHERE a.id = 1`)
	var s Settings
	if err := row.Scan(&s.BaseURL, &s.APIKey, &s.Model, &s.UpdatedAt, &s.UpdatedByName); err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *Repo) Update(ctx context.Context, s Settings, updatedBy int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE ai_settings SET base_url = $1, api_key = $2, model = $3, updated_at = now(), updated_by = $4
		WHERE id = 1`,
		s.BaseURL, s.APIKey, s.Model, updatedBy)
	return err
}

const rephraseSystemPrompt = "You are a writing assistant embedded in a business app. Rephrase the user's text to be clearer and more polished, keeping the same meaning, language, and approximate length. Reply with ONLY the rephrased text and nothing else — no quotes, no preamble, no explanation."

// Rephrase sends text to the configured OpenAI-compatible chat completions
// endpoint (BaseURL is expected to already include any version prefix the
// provider needs, e.g. "https://api.openai.com/v1" — the same convention
// the official OpenAI SDKs use, so this works unmodified against OpenAI
// itself, Ollama's OpenAI-compatible mode, OpenRouter, Groq, etc.) and
// returns the model's rewritten version.
func (r *Repo) Rephrase(ctx context.Context, text string) (string, error) {
	return r.Complete(ctx, rephraseSystemPrompt, text)
}

// Complete is the shared low-level call every AI-assisted feature in the
// app builds on — Rephrase above, and other modules (e.g. social's
// AI-generate/AI-adapt) that need their own system prompt instead of
// rephrase's fixed one.
func (r *Repo) Complete(ctx context.Context, systemPrompt, text string) (string, error) {
	cfg, err := r.Get(ctx)
	if err != nil {
		return "", err
	}
	if !cfg.Configured() {
		return "", ErrNotConfigured
	}

	reqBody, err := json.Marshal(map[string]any{
		"model": cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": systemPrompt},
			{"role": "user", "content": text},
		},
		"temperature": 0.7,
	})
	if err != nil {
		return "", err
	}

	url := strings.TrimRight(cfg.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBody))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("couldn't reach AI provider: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("AI provider returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("couldn't parse AI provider response: %w", err)
	}
	if len(parsed.Choices) == 0 {
		return "", fmt.Errorf("AI provider returned no choices")
	}
	result := strings.TrimSpace(parsed.Choices[0].Message.Content)
	if result == "" {
		return "", fmt.Errorf("AI provider returned an empty response")
	}
	return result, nil
}
