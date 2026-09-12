package invoicing

import (
	"fmt"
	"time"
)

type Invoice struct {
	ID            int64
	ProjectID     int64
	ProjectName   string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Status        string // draft, sent, paid
	TotalAmount   float64
	CreatedBy     int64
	CreatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time
	// Lines is populated only by Get (the single-invoice detail view), not
	// by ListForProject — the list view never needs line items, so it never
	// pays for the extra query.
	Lines []InvoiceLine
}

// Number is a display-only derived value (no stored invoice_number column
// — the row's own id is already unique, so formatting it is simpler than
// maintaining a separate denormalized field).
func (i Invoice) Number() string {
	return fmt.Sprintf("INV-%04d", i.ID)
}

type InvoiceLine struct {
	ID          int64
	InvoiceID   int64
	WorkDate    time.Time
	UserName    string
	Description string
	Hours       float64
	Rate        float64
	Amount      float64
}
