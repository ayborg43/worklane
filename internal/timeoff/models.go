package timeoff

import "time"

// Request mirrors timesheets.Entry's approval-state shape (status/
// approved_by/approved_at/rejection_reason) — the same submitted ->
// approved/rejected state machine, just reviewed by an admin instead of a
// project owner, since a leave request isn't scoped to any one project the
// way a timesheet entry is.
type Request struct {
	ID              int64
	UserID          int64
	UserName        string
	LeaveType       string // vacation, sick, unpaid, other
	StartDate       time.Time
	EndDate         time.Time
	Reason          string
	Status          string // submitted, approved, rejected
	ApprovedBy      int64
	ApproverName    string
	ApprovedAt      *time.Time
	RejectionReason string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Days is an inclusive calendar-day count — the simplest possible measure,
// deliberately not excluding weekends or holidays since no company-calendar
// concept exists anywhere in this app.
func (r Request) Days() int {
	return int(r.EndDate.Sub(r.StartDate).Hours()/24) + 1
}

type RequestInput struct {
	LeaveType string
	StartDate time.Time
	EndDate   time.Time
	Reason    string
}
