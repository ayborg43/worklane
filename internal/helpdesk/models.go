package helpdesk

import "time"

// Ticket uses 0 as the "unset" sentinel for ContactID/AssigneeID, matching
// the same convention projects.Task uses for AssigneeID/ParentTaskID.
// ContactName/ContactCompany come from the linked CRM contact when present;
// CustomerName/CustomerEmail are a free-text fallback for tickets raised by
// someone who isn't (yet) a formal CRM contact.
type Ticket struct {
	ID             int64
	Subject        string
	Description    string
	ContactID      int64
	ContactName    string
	ContactCompany string
	CustomerName   string
	CustomerEmail  string
	AssigneeID     int64
	AssigneeName   string
	Priority       string // low, medium, high
	Status         string // open, in_progress, resolved
	CreatedBy      int64
	CreatedByName  string
	ResolvedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type TicketInput struct {
	Subject       string
	Description   string
	ContactID     int64
	CustomerName  string
	CustomerEmail string
	AssigneeID    int64
	Priority      string
	Status        string
}

type Comment struct {
	ID        int64
	TicketID  int64
	UserID    int64
	UserName  string
	Body      string
	CreatedAt time.Time
}

// RequesterLabel is the one line a Kanban card or detail page shows for
// "who this ticket is from" — the linked contact if there is one,
// otherwise the free-text customer name/email, otherwise a placeholder.
func (t Ticket) RequesterLabel() string {
	if t.ContactName != "" {
		if t.ContactCompany != "" {
			return t.ContactName + " (" + t.ContactCompany + ")"
		}
		return t.ContactName
	}
	if t.CustomerName != "" {
		return t.CustomerName
	}
	if t.CustomerEmail != "" {
		return t.CustomerEmail
	}
	return "Unknown requester"
}
