package invoicing

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrNoBillableHours means the requested period has nothing left to
	// bill — every approved+billable entry in it is either absent or
	// already claimed by an earlier invoice.
	ErrNoBillableHours = errors.New("no billable, approved, uninvoiced hours in that period")
	// ErrConflict means the invoice isn't a draft — only drafts can be
	// deleted, the same "settled record" posture timesheet approval and
	// CRM stage deletion already take.
	ErrConflict = errors.New("only draft invoices can be deleted")
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// ---------- project-level billing settings ----------
//
// invoicing reads/writes the projects and timesheet_entries tables
// directly via its own pool rather than importing projects.Repo/
// timesheets.Repo — the same "query another package's table directly for a
// narrow, specific need" precedent projects.Repo.ListMembers already sets
// by querying the users table directly instead of going through auth.Repo.
// This keeps invoicing fully decoupled at the Go level (no import cycle
// risk at all) while still sharing the underlying schema.

func (r *Repo) ProjectInfo(ctx context.Context, projectID int64) (name string, ownerID int64, rate float64, err error) {
	err = r.pool.QueryRow(ctx, `SELECT name, owner_id, billable_rate FROM projects WHERE id = $1`, projectID).
		Scan(&name, &ownerID, &rate)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", 0, 0, ErrNotFound
		}
		return "", 0, 0, err
	}
	return name, ownerID, rate, nil
}

func (r *Repo) SetBillableRate(ctx context.Context, projectID int64, rate float64) error {
	_, err := r.pool.Exec(ctx, `UPDATE projects SET billable_rate = $1, updated_at = now() WHERE id = $2`, rate, projectID)
	return err
}

// ---------- invoices ----------

const invoiceSelect = `
	SELECT i.id, i.project_id, p.name, i.period_start, i.period_end, i.status, i.total_amount,
	       i.created_by, u.name, i.created_at, i.updated_at
	FROM invoices i
	JOIN projects p ON p.id = i.project_id
	JOIN users u ON u.id = i.created_by`

func scanInvoice(row pgx.Row) (*Invoice, error) {
	var inv Invoice
	err := row.Scan(&inv.ID, &inv.ProjectID, &inv.ProjectName, &inv.PeriodStart, &inv.PeriodEnd, &inv.Status,
		&inv.TotalAmount, &inv.CreatedBy, &inv.CreatedByName, &inv.CreatedAt, &inv.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &inv, nil
}

func (r *Repo) ListForProject(ctx context.Context, projectID int64) ([]Invoice, error) {
	rows, err := r.pool.Query(ctx, invoiceSelect+` WHERE i.project_id = $1 ORDER BY i.created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.ID, &inv.ProjectID, &inv.ProjectName, &inv.PeriodStart, &inv.PeriodEnd, &inv.Status,
			&inv.TotalAmount, &inv.CreatedBy, &inv.CreatedByName, &inv.CreatedAt, &inv.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// Get loads the invoice plus its line items (the detail/print view).
func (r *Repo) Get(ctx context.Context, id int64) (*Invoice, error) {
	inv, err := scanInvoice(r.pool.QueryRow(ctx, invoiceSelect+` WHERE i.id = $1`, id))
	if err != nil {
		return nil, err
	}
	lines, err := r.listLines(ctx, id)
	if err != nil {
		return nil, err
	}
	inv.Lines = lines
	return inv, nil
}

func (r *Repo) listLines(ctx context.Context, invoiceID int64) ([]InvoiceLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, invoice_id, work_date, user_name, description, hours, rate, amount
		FROM invoice_lines WHERE invoice_id = $1 ORDER BY work_date, id`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InvoiceLine
	for rows.Next() {
		var l InvoiceLine
		if err := rows.Scan(&l.ID, &l.InvoiceID, &l.WorkDate, &l.UserName, &l.Description, &l.Hours, &l.Rate, &l.Amount); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

type billableEntry struct {
	ID          int64
	WorkDate    time.Time
	UserName    string
	Description string
	Hours       float64
}

func (r *Repo) billableUninvoiced(ctx context.Context, projectID int64, from, to time.Time) ([]billableEntry, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT e.id, e.work_date, u.name, e.description, e.hours
		FROM timesheet_entries e
		JOIN users u ON u.id = e.user_id
		WHERE e.project_id = $1 AND e.status = 'approved' AND e.billable = true
		      AND e.invoiced_at IS NULL AND e.work_date >= $2 AND e.work_date < $3
		ORDER BY e.work_date, e.id`, projectID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []billableEntry
	for rows.Next() {
		var e billableEntry
		if err := rows.Scan(&e.ID, &e.WorkDate, &e.UserName, &e.Description, &e.Hours); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GenerateInvoice snapshots every approved, billable, not-yet-invoiced
// timesheet entry in [from, to) into a new invoice + line items at the
// project's current billable rate, then marks those entries invoiced so a
// later invoice can't double-bill them. All in one transaction.
//
// periodStart/periodEnd are both inclusive (the dates a person actually
// typed, and what gets stored/displayed on the invoice) — the conversion
// to the [from, to) exclusive-upper-bound shape the billable-hours query
// needs stays entirely inside this method, so callers never have to think
// about it and the stored period_end always matches what was requested.
func (r *Repo) GenerateInvoice(ctx context.Context, projectID, createdBy int64, periodStart, periodEnd time.Time) (*Invoice, error) {
	_, _, rate, err := r.ProjectInfo(ctx, projectID)
	if err != nil {
		return nil, err
	}
	queryEnd := periodEnd.AddDate(0, 0, 1)
	entries, err := r.billableUninvoiced(ctx, projectID, periodStart, queryEnd)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrNoBillableHours
	}

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var total float64
	for _, e := range entries {
		total += e.Hours * rate
	}

	var invoiceID int64
	err = tx.QueryRow(ctx, `
		INSERT INTO invoices (project_id, period_start, period_end, total_amount, created_by)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		projectID, periodStart, periodEnd, total, createdBy,
	).Scan(&invoiceID)
	if err != nil {
		return nil, err
	}

	entryIDs := make([]int64, 0, len(entries))
	for _, e := range entries {
		amount := e.Hours * rate
		if _, err := tx.Exec(ctx, `
			INSERT INTO invoice_lines (invoice_id, work_date, user_name, description, hours, rate, amount)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			invoiceID, e.WorkDate, e.UserName, e.Description, e.Hours, rate, amount); err != nil {
			return nil, err
		}
		entryIDs = append(entryIDs, e.ID)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE timesheet_entries SET invoiced_at = now(), invoice_id = $1 WHERE id = ANY($2)`,
		invoiceID, entryIDs); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return r.Get(ctx, invoiceID)
}

func (r *Repo) SetStatus(ctx context.Context, id int64, status string) error {
	_, err := r.pool.Exec(ctx, `UPDATE invoices SET status = $1, updated_at = now() WHERE id = $2`, status, id)
	return err
}

// Delete only removes draft invoices, and un-invoices their entries first
// (clearing invoiced_at/invoice_id) so the hours become billable again on
// a future invoice instead of being stuck permanently excluded.
func (r *Repo) Delete(ctx context.Context, id int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Clear the FK reference from timesheet_entries before deleting the
	// invoice row itself — invoice_id has no ON DELETE action, so deleting
	// the invoice first would violate the constraint.
	if _, err := tx.Exec(ctx,
		`UPDATE timesheet_entries SET invoiced_at = NULL, invoice_id = NULL
		 WHERE invoice_id = $1 AND EXISTS (SELECT 1 FROM invoices WHERE id = $1 AND status = 'draft')`, id); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM invoices WHERE id = $1 AND status = 'draft'`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return tx.Commit(ctx)
}
