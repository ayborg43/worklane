package helpdesk

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func nullInt64(v int64) sql.NullInt64 {
	if v == 0 {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: v, Valid: true}
}

const ticketSelect = `
	SELECT t.id, t.subject, t.description, COALESCE(t.contact_id, 0), COALESCE(c.name, ''), COALESCE(c.company, ''),
	       t.customer_name, t.customer_email, COALESCE(t.assignee_id, 0), COALESCE(u.name, ''),
	       t.priority, t.status, t.created_by, COALESCE(cb.name, ''), t.resolved_at, t.created_at, t.updated_at
	FROM tickets t
	LEFT JOIN contacts c ON c.id = t.contact_id
	LEFT JOIN users u ON u.id = t.assignee_id
	JOIN users cb ON cb.id = t.created_by`

func scanTicket(row pgx.Row) (*Ticket, error) {
	var t Ticket
	err := row.Scan(&t.ID, &t.Subject, &t.Description, &t.ContactID, &t.ContactName, &t.ContactCompany,
		&t.CustomerName, &t.CustomerEmail, &t.AssigneeID, &t.AssigneeName,
		&t.Priority, &t.Status, &t.CreatedBy, &t.CreatedByName, &t.ResolvedAt, &t.CreatedAt, &t.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func scanTickets(rows pgx.Rows) ([]Ticket, error) {
	defer rows.Close()
	var out []Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.Subject, &t.Description, &t.ContactID, &t.ContactName, &t.ContactCompany,
			&t.CustomerName, &t.CustomerEmail, &t.AssigneeID, &t.AssigneeName,
			&t.Priority, &t.Status, &t.CreatedBy, &t.CreatedByName, &t.ResolvedAt, &t.CreatedAt, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListAll returns every ticket — helpdesk is a shared, company-wide queue
// like CRM (not project-scoped), so there's no membership filter here.
func (r *Repo) ListAll(ctx context.Context) ([]Ticket, error) {
	rows, err := r.pool.Query(ctx, ticketSelect+` ORDER BY t.created_at DESC`)
	if err != nil {
		return nil, err
	}
	return scanTickets(rows)
}

func (r *Repo) Get(ctx context.Context, id int64) (*Ticket, error) {
	return scanTicket(r.pool.QueryRow(ctx, ticketSelect+` WHERE t.id = $1`, id))
}

func (r *Repo) Create(ctx context.Context, in TicketInput, createdBy int64) (*Ticket, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO tickets (subject, description, contact_id, customer_name, customer_email,
		                      assignee_id, priority, status, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		in.Subject, in.Description, nullInt64(in.ContactID), in.CustomerName, in.CustomerEmail,
		nullInt64(in.AssigneeID), in.Priority, in.Status, createdBy,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// Update writes every field including resolved_at, which the caller (see
// helpdesk.Handlers.Update) computes based on the status transition —
// set on first entering "resolved", cleared on leaving it.
func (r *Repo) Update(ctx context.Context, id int64, in TicketInput, resolvedAt *time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE tickets SET subject = $1, description = $2, contact_id = $3, customer_name = $4,
		       customer_email = $5, assignee_id = $6, priority = $7, status = $8, resolved_at = $9,
		       updated_at = now()
		WHERE id = $10`,
		in.Subject, in.Description, nullInt64(in.ContactID), in.CustomerName, in.CustomerEmail,
		nullInt64(in.AssigneeID), in.Priority, in.Status, resolvedAt, id)
	return err
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM tickets WHERE id = $1`, id)
	return err
}

// ---------- comments ----------

func (r *Repo) ListComments(ctx context.Context, ticketID int64) ([]Comment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.ticket_id, c.user_id, u.name, c.body, c.created_at
		FROM ticket_comments c JOIN users u ON u.id = c.user_id
		WHERE c.ticket_id = $1 ORDER BY c.created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.TicketID, &c.UserID, &c.UserName, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) CreateComment(ctx context.Context, ticketID, userID int64, body string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO ticket_comments (ticket_id, user_id, body) VALUES ($1, $2, $3)`, ticketID, userID, body)
	return err
}

// ---------- CRM contacts (read-only, for the requester dropdown) ----------

// ContactOption reads the contacts table directly rather than importing
// crm.Repo — the same one-directional "query another package's table for
// a narrow need" precedent invoicing/wiki already set for projects.
type ContactOption struct {
	ID      int64
	Name    string
	Company string
}

func (r *Repo) ListContacts(ctx context.Context) ([]ContactOption, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, company FROM contacts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ContactOption
	for rows.Next() {
		var c ContactOption
		if err := rows.Scan(&c.ID, &c.Name, &c.Company); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
