package projects

import "time"

type Project struct {
	ID          int64
	Name        string
	Description string
	OwnerID     int64
	OwnerName   string
	Status      string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	TaskCount   int
}

// Task uses 0 as the "unset" sentinel for AssigneeID/ParentTaskID (rather
// than *int64) so templates can compare/render them without a helper func.
// RecurrenceUnit == "" means not recurring; RecurrenceUntil is a genuine
// pointer (no sensible zero value for "no end date"), same rationale as
// timesheets.Entry.ApprovedAt.
type Task struct {
	ID                 int64
	ProjectID          int64
	ParentTaskID       int64
	Name               string
	Description        string
	AssigneeID         int64
	AssigneeName       string
	StartDate          time.Time
	EndDate            time.Time
	Progress           int
	Status             string
	SortOrder          int
	CreatedAt          time.Time
	UpdatedAt          time.Time
	DependsOn          []int64
	RecurrenceUnit     string
	RecurrenceInterval int
	RecurrenceUntil    *time.Time
}

type TaskInput struct {
	Name         string
	Description  string
	AssigneeID   int64 // 0 = unassigned
	StartDate    time.Time
	EndDate      time.Time
	Progress     int
	Status       string
	ParentTaskID int64 // 0 = top-level task, not a subtask
}

// TaskTemplate is a reusable blueprint for quickly creating similar tasks —
// "Use" fills in name/description/assignee and computes end_date from a
// chosen start_date + DefaultDurationDays.
type TaskTemplate struct {
	ID                  int64
	ProjectID           int64
	Name                string
	Description         string
	DefaultDurationDays int
	DefaultAssigneeID   int64
	DefaultAssigneeName string
	CreatedAt           time.Time
}

type Comment struct {
	ID        int64
	TaskID    int64
	UserID    int64
	UserName  string
	Body      string
	CreatedAt time.Time
}

type Milestone struct {
	ID          int64
	ProjectID   int64
	ProjectName string
	Name        string
	Date        time.Time
	CreatedAt   time.Time
}
