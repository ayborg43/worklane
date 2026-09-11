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
type Task struct {
	ID           int64
	ProjectID    int64
	ParentTaskID int64
	Name         string
	Description  string
	AssigneeID   int64
	AssigneeName string
	StartDate    time.Time
	EndDate      time.Time
	Progress     int
	Status       string
	SortOrder    int
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DependsOn    []int64
}

type TaskInput struct {
	Name        string
	Description string
	AssigneeID  int64 // 0 = unassigned
	StartDate   time.Time
	EndDate     time.Time
	Progress    int
	Status      string
}

type Comment struct {
	ID        int64
	TaskID    int64
	UserID    int64
	UserName  string
	Body      string
	CreatedAt time.Time
}
