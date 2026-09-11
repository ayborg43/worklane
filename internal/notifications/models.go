package notifications

import "time"

// ReadAt is a genuine pointer (nil = unread) — same rationale as
// timesheets.Entry.ApprovedAt: there's no sensible zero value for "unset"
// on a timestamp.
type Notification struct {
	ID        int64
	UserID    int64
	Kind      string
	Body      string
	Link      string
	ReadAt    *time.Time
	CreatedAt time.Time
}
