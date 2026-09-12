package dashboard

import "time"

type TaskItem struct {
	ID          int64
	Name        string
	ProjectID   int64
	ProjectName string
	EndDate     time.Time
}

type TicketItem struct {
	ID       int64
	Subject  string
	Priority string
}

type OpportunityItem struct {
	ID          int64
	Name        string
	ValueAmount float64
	ContactName string
}

type ProjectHoursItem struct {
	ProjectID   int64
	ProjectName string
	Hours       float64
}

type ProjectItem struct {
	ID        int64
	Name      string
	TaskCount int
}

type InvoiceItem struct {
	ID          int64
	ProjectID   int64
	ProjectName string
	TotalAmount float64
	Status      string
}

// Data is the full set of KPI widgets shown on the dashboard. Each count/
// total is computed independently of its preview list (a COUNT(*) alongside
// a small LIMIT'd SELECT) so the big number stays accurate even once a
// widget has more matches than the handful it previews.
type Data struct {
	OverdueTaskCount int
	OverdueTasks     []TaskItem

	OpenTicketCount int
	OpenTickets     []TicketItem

	PipelineValue    float64
	TopOpportunities []OpportunityItem

	UnbilledHours     float64
	UnbilledByProject []ProjectHoursItem

	ProjectCount int
	Projects     []ProjectItem

	TotalInvoiced float64
	TopInvoices   []InvoiceItem
}
