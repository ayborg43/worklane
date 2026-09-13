package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const perKindLimit = 5

// Repo queries tasks/tickets/contacts/wiki_pages/invoices directly rather
// than importing projects/helpdesk/crm/wiki/invoicing — same narrow-need
// precedent as wiki.Repo.ProjectName and invoicing.Repo.ProjectInfo, just
// spread across five tables instead of one. Each kind enforces exactly the
// visibility rule its owning module's own handlers already enforce:
// project-membership for tasks/wiki, project-ownership for invoices, and
// unrestricted for tickets/contacts (both already shared, ACL-free modules).
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// Search runs one narrow query per kind rather than a single UNION, since a
// UNION would force every kind through the same column shape and the same
// visibility rule — the exact thing that varies here.
func (r *Repo) Search(ctx context.Context, userID int64, q string) ([]Result, error) {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil, nil
	}
	like := "%" + escapeLike(q) + "%"

	var out []Result
	for _, fn := range []func(context.Context, int64, string) ([]Result, error){
		r.searchTasks, r.searchTickets, r.searchContacts, r.searchWiki, r.searchInvoices,
	} {
		results, err := fn(ctx, userID, like)
		if err != nil {
			return nil, err
		}
		out = append(out, results...)
	}
	return out, nil
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (r *Repo) searchTasks(ctx context.Context, userID int64, like string) ([]Result, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.name, p.id, p.name
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $1
		WHERE t.name ILIKE $2 ESCAPE '\'
		ORDER BY t.updated_at DESC
		LIMIT $3`, userID, like, perKindLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var taskID, projectID int64
		var name, projectName string
		if err := rows.Scan(&taskID, &name, &projectID, &projectName); err != nil {
			return nil, err
		}
		out = append(out, Result{
			Kind:     "task",
			Title:    name,
			Subtitle: projectName,
			URL:      fmt.Sprintf("/projects/%d/tasks/%d", projectID, taskID),
		})
	}
	return out, rows.Err()
}

func (r *Repo) searchTickets(ctx context.Context, _ int64, like string) ([]Result, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, subject, status
		FROM tickets
		WHERE subject ILIKE $1 ESCAPE '\' OR customer_name ILIKE $1 ESCAPE '\'
		ORDER BY updated_at DESC
		LIMIT $2`, like, perKindLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var id int64
		var subject, status string
		if err := rows.Scan(&id, &subject, &status); err != nil {
			return nil, err
		}
		out = append(out, Result{
			Kind:     "ticket",
			Title:    subject,
			Subtitle: "Ticket · " + ticketStatusLabel(status),
			URL:      fmt.Sprintf("/helpdesk/tickets/%d", id),
		})
	}
	return out, rows.Err()
}

func (r *Repo) searchContacts(ctx context.Context, _ int64, like string) ([]Result, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, company
		FROM contacts
		WHERE name ILIKE $1 ESCAPE '\' OR company ILIKE $1 ESCAPE '\' OR email ILIKE $1 ESCAPE '\'
		ORDER BY updated_at DESC
		LIMIT $2`, like, perKindLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var id int64
		var name, company string
		if err := rows.Scan(&id, &name, &company); err != nil {
			return nil, err
		}
		subtitle := "Contact"
		if company != "" {
			subtitle = company
		}
		out = append(out, Result{
			Kind:     "contact",
			Title:    name,
			Subtitle: subtitle,
			URL:      fmt.Sprintf("/crm/contacts#contact-row-%d", id),
		})
	}
	return out, rows.Err()
}

func (r *Repo) searchWiki(ctx context.Context, userID int64, like string) ([]Result, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT w.id, w.title, p.id, p.name
		FROM wiki_pages w
		JOIN projects p ON p.id = w.project_id
		JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $1
		WHERE w.title ILIKE $2 ESCAPE '\' OR w.body ILIKE $2 ESCAPE '\'
		ORDER BY w.updated_at DESC
		LIMIT $3`, userID, like, perKindLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var pageID, projectID int64
		var title, projectName string
		if err := rows.Scan(&pageID, &title, &projectID, &projectName); err != nil {
			return nil, err
		}
		out = append(out, Result{
			Kind:     "wiki",
			Title:    title,
			Subtitle: projectName,
			URL:      fmt.Sprintf("/projects/%d/wiki/%d", projectID, pageID),
		})
	}
	return out, rows.Err()
}

// searchInvoices is scoped to projects the user owns — invoicing's own
// handlers only let a project's owner view/generate its invoices, so search
// must not surface an invoice a mere member (let alone a stranger) couldn't
// otherwise open.
func (r *Repo) searchInvoices(ctx context.Context, userID int64, like string) ([]Result, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT i.id, i.status, i.total_amount, p.name
		FROM invoices i
		JOIN projects p ON p.id = i.project_id
		WHERE p.owner_id = $1 AND (p.name ILIKE $2 ESCAPE '\' OR i.status ILIKE $2 ESCAPE '\')
		ORDER BY i.created_at DESC
		LIMIT $3`, userID, like, perKindLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Result
	for rows.Next() {
		var id int64
		var status string
		var total float64
		var projectName string
		if err := rows.Scan(&id, &status, &total, &projectName); err != nil {
			return nil, err
		}
		out = append(out, Result{
			Kind:     "invoice",
			Title:    fmt.Sprintf("Invoice #%d — %s", id, projectName),
			Subtitle: fmt.Sprintf("%s · ₦%.2f", statusTitle(status), total),
			URL:      fmt.Sprintf("/invoices/%d", id),
		})
	}
	return out, rows.Err()
}

func ticketStatusLabel(s string) string {
	switch s {
	case "open":
		return "Open"
	case "in_progress":
		return "In Progress"
	case "resolved":
		return "Resolved"
	default:
		return s
	}
}

func statusTitle(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
