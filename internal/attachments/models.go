package attachments

import (
	"time"

	"github.com/sociolytik/odoo-clone/internal/uploads"
)

// TaskID uses the same 0-sentinel convention as projects.Task — 0 means a
// project-level file, not attached to any specific task.
type Attachment struct {
	ID           int64
	ProjectID    int64
	TaskID       int64
	UploadedBy   int64
	UploaderName string
	Filename     string
	ContentType  string
	SizeBytes    int64
	StoragePath  string
	CreatedAt    time.Time
}

// SizeDisplay is a zero-arg method so templates can call {{.SizeDisplay}}
// directly via reflection — no template.Funcs registration needed.
func (a Attachment) SizeDisplay() string {
	return uploads.FormatSize(a.SizeBytes)
}
