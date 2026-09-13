package chat

import (
	"html/template"
	"strings"
	"time"

	"github.com/sociolytik/odoo-clone/internal/uploads"
)

// OtherUserName is only meaningful for Kind == "dm" — the display name of
// the other participant, resolved relative to whichever user is viewing.
type Channel struct {
	ID            int64
	Name          string
	Kind          string
	DMKey         string
	OtherUserName string
	UnreadCount   int
	CreatedBy     int64
	CreatedAt     time.Time
}

// AttachmentFilename == "" means the message carries no file — a message can
// have body text, an attachment, or both (an attachment with no caption).
type Message struct {
	ID                    int64
	ChannelID             int64
	UserID                int64
	UserName              string
	Body                  string
	AttachmentFilename    string
	AttachmentContentType string
	AttachmentSizeBytes   int64
	AttachmentPath        string
	CreatedAt             time.Time
	// BodyHTML is computed per-request (not a DB column) by RenderMentions —
	// Body with any recognized "@Full Name" wrapped in a highlight span.
	// Handlers must set it before rendering chat/message_row.html, which
	// renders it in place of Body.
	BodyHTML template.HTML
}

func (m Message) HasAttachment() bool { return m.AttachmentFilename != "" }

func (m Message) IsImage() bool { return strings.HasPrefix(m.AttachmentContentType, "image/") }

// AttachmentSizeDisplay is a zero-arg method so templates can call it
// directly via reflection, matching attachments.Attachment.SizeDisplay.
func (m Message) AttachmentSizeDisplay() string { return uploads.FormatSize(m.AttachmentSizeBytes) }
