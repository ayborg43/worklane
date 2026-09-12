package portal

import (
	"fmt"
	"time"
)

// Contact is the portal's own minimal view of a CRM contact — the customer
// identity behind a portal session. portal queries the contacts table
// directly rather than importing crm, same narrow-need precedent as
// helpdesk.Repo.ListContacts.
type Contact struct {
	ID      int64
	Name    string
	Email   string
	Company string
}

// Ticket is a read-only, customer-facing view of a helpdesk ticket — only
// the fields a customer should see, not assignee/internal notes.
type Ticket struct {
	ID         int64
	Subject    string
	Priority   string
	Status     string
	CreatedAt  time.Time
	ResolvedAt *time.Time
}

type Comment struct {
	ID        int64
	UserName  string
	Body      string
	CreatedAt time.Time
}

// Invoice mirrors invoicing.Invoice's shape (including the Number() display
// method) but is its own type, queried directly from invoices/invoice_lines
// — portal never imports invoicing, same narrow-need precedent as everywhere
// else in this app.
type Invoice struct {
	ID            int64
	ProjectName   string
	PeriodStart   time.Time
	PeriodEnd     time.Time
	Status        string
	TotalAmount   float64
	CreatedAt     time.Time
	CreatedByName string
	// Lines is populated only by GetInvoiceForContact, not
	// ListInvoicesForContact — the list view never needs line items.
	Lines []InvoiceLine
}

func (i Invoice) Number() string {
	return fmt.Sprintf("INV-%04d", i.ID)
}

type InvoiceLine struct {
	WorkDate    time.Time
	UserName    string
	Description string
	Hours       float64
	Rate        float64
	Amount      float64
}
