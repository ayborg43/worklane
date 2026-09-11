package activities

import "time"

// TaskID uses the same 0-sentinel convention as attachments.Attachment — 0
// means a project-level activity, not tied to any specific task. DoneAt is
// a genuine pointer (nil = pending), same rationale as
// notifications.Notification.ReadAt: no sensible zero value for a
// timestamp the way 0 works for an int64 id.
type Activity struct {
	ID             int64
	ProjectID      int64
	ProjectName    string
	TaskID         int64
	TaskName       string
	Kind           string
	Note           string
	DueDate        time.Time
	AssignedTo     int64
	AssignedToName string
	CreatedBy      int64
	CreatedByName  string
	DoneAt         *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// KindLabel and IsOverdue/IsDueToday are zero-arg methods so templates can
// call them directly via reflection — no template.Funcs registration
// needed, same convention as attachments.Attachment.SizeDisplay.
func (a Activity) KindLabel() string {
	switch a.Kind {
	case "call":
		return "Call"
	case "meeting":
		return "Meeting"
	case "email":
		return "Email"
	default:
		return "To-do"
	}
}

func today() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

func (a Activity) IsOverdue() bool {
	return a.DoneAt == nil && a.DueDate.Before(today())
}

func (a Activity) IsDueToday() bool {
	return a.DoneAt == nil && a.DueDate.Equal(today())
}

type Input struct {
	ProjectID  int64
	TaskID     int64
	Kind       string
	Note       string
	DueDate    time.Time
	AssignedTo int64
	CreatedBy  int64
}
