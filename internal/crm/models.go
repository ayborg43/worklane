package crm

import "time"

type Contact struct {
	ID        int64
	Name      string
	Company   string
	Email     string
	Phone     string
	CreatedBy int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

type Stage struct {
	ID        int64
	Name      string
	SortOrder int
	IsWon     bool
	CreatedAt time.Time
}

// Opportunity uses 0 as the "unset" sentinel for ContactID (consistent
// with projects.Task's AssigneeID/ParentTaskID convention). IsLost is
// independent of StageID on purpose — a lost deal keeps its real stage
// history rather than being force-moved into a terminal column.
type Opportunity struct {
	ID                int64
	Name              string
	ContactID         int64
	ContactName       string
	ContactCompany    string
	StageID           int64
	StageName         string
	OwnerID           int64
	OwnerName         string
	ValueAmount       float64
	ExpectedCloseDate *time.Time
	IsLost            bool
	LostReason        string
	CreatedBy         int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

type OpportunityInput struct {
	Name              string
	ContactID         int64 // 0 = no contact linked
	StageID           int64
	OwnerID           int64
	ValueAmount       float64
	ExpectedCloseDate *time.Time
}
