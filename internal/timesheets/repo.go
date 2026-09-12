package timesheets

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrConflict means the entry was already reviewed (approved/rejected)
	// by the time an Approve/Reject call reached it — a compare-and-swap
	// style guard against double-review races, not a separate row lock.
	ErrConflict = errors.New("entry already reviewed")
)

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

const entrySelect = `
	SELECT e.id, e.user_id, u.name, e.project_id, p.name, p.owner_id, COALESCE(e.task_id, 0), COALESCE(t.name, ''),
	       e.work_date, e.hours, e.description, e.status, COALESCE(e.approved_by, 0), COALESCE(a.name, ''),
	       e.approved_at, e.rejection_reason, e.billable, COALESCE(e.invoice_id, 0), e.invoiced_at,
	       e.created_at, e.updated_at
	FROM timesheet_entries e
	JOIN users u ON u.id = e.user_id
	JOIN projects p ON p.id = e.project_id
	LEFT JOIN tasks t ON t.id = e.task_id
	LEFT JOIN users a ON a.id = e.approved_by`

func scanEntryRow(row pgx.Row) (*Entry, error) {
	var e Entry
	err := row.Scan(&e.ID, &e.UserID, &e.UserName, &e.ProjectID, &e.ProjectName, &e.ProjectOwnerID, &e.TaskID, &e.TaskName,
		&e.WorkDate, &e.Hours, &e.Description, &e.Status, &e.ApprovedBy, &e.ApproverName,
		&e.ApprovedAt, &e.RejectionReason, &e.Billable, &e.InvoiceID, &e.InvoicedAt, &e.CreatedAt, &e.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &e, nil
}

func scanEntryRows(rows pgx.Rows) ([]Entry, error) {
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.UserID, &e.UserName, &e.ProjectID, &e.ProjectName, &e.ProjectOwnerID, &e.TaskID, &e.TaskName,
			&e.WorkDate, &e.Hours, &e.Description, &e.Status, &e.ApprovedBy, &e.ApproverName,
			&e.ApprovedAt, &e.RejectionReason, &e.Billable, &e.InvoiceID, &e.InvoicedAt, &e.CreatedAt, &e.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *Repo) Create(ctx context.Context, userID int64, in EntryInput) (*Entry, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO timesheet_entries (user_id, project_id, task_id, work_date, hours, description, billable)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		userID, in.ProjectID, nullInt64(in.TaskID), in.WorkDate, in.Hours, in.Description, in.Billable,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

// SetBillable is only reachable by the entry's own logger, and only before
// it's been invoiced — changing billability after an invoice has already
// snapshotted the entry wouldn't affect that invoice and would just be
// confusing to see flip on a settled bill.
func (r *Repo) SetBillable(ctx context.Context, id, userID int64, billable bool) error {
	tag, err := r.pool.Exec(ctx,
		`UPDATE timesheet_entries SET billable = $1 WHERE id = $2 AND user_id = $3 AND invoiced_at IS NULL`,
		billable, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *Repo) Get(ctx context.Context, id int64) (*Entry, error) {
	return scanEntryRow(r.pool.QueryRow(ctx, entrySelect+` WHERE e.id = $1`, id))
}

// ListForUserWeek returns entries for userID with work_date in
// [weekStart, weekEnd) (weekEnd exclusive), ordered chronologically.
func (r *Repo) ListForUserWeek(ctx context.Context, userID int64, weekStart, weekEnd time.Time) ([]Entry, error) {
	rows, err := r.pool.Query(ctx, entrySelect+`
		WHERE e.user_id = $1 AND e.work_date >= $2 AND e.work_date < $3
		ORDER BY e.work_date, e.id`, userID, weekStart, weekEnd)
	if err != nil {
		return nil, err
	}
	return scanEntryRows(rows)
}

// ListPendingForOwner returns submitted entries across every project
// ownerID owns, for their approvals queue.
func (r *Repo) ListPendingForOwner(ctx context.Context, ownerID int64) ([]Entry, error) {
	rows, err := r.pool.Query(ctx, entrySelect+`
		WHERE e.status = 'submitted' AND p.owner_id = $1
		ORDER BY e.work_date, e.id`, ownerID)
	if err != nil {
		return nil, err
	}
	return scanEntryRows(rows)
}

func (r *Repo) Approve(ctx context.Context, id, approverID int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE timesheet_entries
		SET status = 'approved', approved_by = $1, approved_at = now(), rejection_reason = '', updated_at = now()
		WHERE id = $2 AND status = 'submitted'`, approverID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

func (r *Repo) Reject(ctx context.Context, id, approverID int64, reason string) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE timesheet_entries
		SET status = 'rejected', approved_by = $1, approved_at = now(), rejection_reason = $2, updated_at = now()
		WHERE id = $3 AND status = 'submitted'`, approverID, reason, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrConflict
	}
	return nil
}

// Delete removes the entry only if it belongs to userID and hasn't already
// been approved — an approved entry is a settled record, not something the
// logging user can retract unilaterally.
func (r *Repo) Delete(ctx context.Context, id, userID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM timesheet_entries WHERE id = $1 AND user_id = $2 AND status <> 'approved'`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
