package ai

import (
	"strings"
	"time"
)

// Settings is the singleton admin-configured OpenAI-compatible endpoint
// used for the "Rephrase" button on text fields. A row always exists
// (seeded by its migration), so reads never need to handle "not
// configured yet" as a missing-row case — an empty BaseURL/APIKey is
// what "not configured" looks like instead, same convention as
// settings.MailSettings.
type Settings struct {
	BaseURL       string
	APIKey        string
	Model         string
	UpdatedAt     time.Time
	UpdatedByName string
}

// Configured reports whether enough is filled in to attempt a call. Model
// is required too — unlike a mail server's port, there's no universal
// default that makes sense across different OpenAI-compatible providers
// (gpt-4o-mini, llama3, etc. all mean something different to their own
// endpoint).
func (s Settings) Configured() bool {
	return strings.TrimSpace(s.BaseURL) != "" && strings.TrimSpace(s.APIKey) != "" && strings.TrimSpace(s.Model) != ""
}
