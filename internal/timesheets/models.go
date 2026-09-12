package timesheets

import "time"

// TaskID/TaskName use the same 0-sentinel convention as projects.Task —
// task_id is an optional FK (a timesheet entry can be logged against just a
// project, with no specific task). ApprovedBy/ApproverName follow the same
// 0-sentinel convention; ApprovedAt is a genuine pointer (a deliberate
// deviation — there's no sensible zero value for "unset" on a timestamp the
// way 0 works for an int64 id).
// Billable defaults to true (most logged time is billable) — InvoiceID
// (0-sentinel) and InvoicedAt (genuine pointer, same ApprovedAt rationale)
// are set together once an invoice snapshots this entry, after which the
// billable flag is locked (see Repo.SetBillable).
type Entry struct {
	ID              int64
	UserID          int64
	UserName        string
	ProjectID       int64
	ProjectName     string
	ProjectOwnerID  int64
	TaskID          int64
	TaskName        string
	WorkDate        time.Time
	Hours           float64
	Description     string
	Status          string
	ApprovedBy      int64
	ApproverName    string
	ApprovedAt      *time.Time
	RejectionReason string
	Billable        bool
	InvoiceID       int64
	InvoicedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

type EntryInput struct {
	ProjectID   int64
	TaskID      int64
	WorkDate    time.Time
	Hours       float64
	Description string
	Billable    bool
}
