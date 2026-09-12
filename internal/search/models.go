package search

// Result is a single global-search hit, normalized across every module it
// draws from (tasks, tickets, contacts, wiki pages, invoices) so the search
// box and its results template only ever deal with one shape.
type Result struct {
	Kind     string // "task", "ticket", "contact", "wiki", "invoice"
	Title    string
	Subtitle string
	URL      string
}

func (r Result) Label() string {
	switch r.Kind {
	case "task":
		return "Task"
	case "ticket":
		return "Ticket"
	case "contact":
		return "Contact"
	case "wiki":
		return "Wiki"
	case "invoice":
		return "Invoice"
	default:
		return r.Kind
	}
}
