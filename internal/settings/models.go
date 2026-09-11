package settings

import (
	"strings"
	"time"
)

// MailSettings is the singleton admin-configured outbound mail
// configuration. A row always exists (seeded by its migration), so reads
// never need to handle "not configured yet" as a missing-row case — an
// empty SMTPHost is what "not configured" looks like instead.
type MailSettings struct {
	SMTPHost      string
	SMTPPort      int
	Username      string
	Password      string
	FromAddress   string
	FromName      string
	UseTLS        bool
	UpdatedAt     time.Time
	UpdatedByName string
}

// Configured reports whether enough is filled in to attempt a send.
func (m MailSettings) Configured() bool {
	return strings.TrimSpace(m.SMTPHost) != "" && m.SMTPPort != 0 && strings.TrimSpace(m.FromAddress) != ""
}
