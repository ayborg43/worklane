package timeoff

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrConflict means the request was already reviewed (approved/
	// rejected) by the time an Approve/Reject call reached it — a
	// compare-and-swap guard against double-review races, same as
	// timesheets.ErrConflict.
	ErrConflict = errors.New("request already reviewed")
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

const requestSelect = `
	SELECT r.id, r.user_id, u.name, r.leave_type, r.start_date, r.end_date, r.reason,
	       r.status, COALESCE(r.approved_by, 0), COALESCE(a.name, ''), r.approved_at, r.rejection_reason,
	       r.created_at, r.updated_at
	FROM leave_requests r
	JOIN users u ON u.id = r.user_id
	LEFT JOIN users a ON a.id = r.approved_by`

func scanRequest(row pgx.Row) (*Request, error) {
	var req Request
	err := row.Scan(&req.ID, &req.UserID, &req.UserName, &req.LeaveType, &req.StartDate, &req.EndDate, &req.Reason,
		&req.Status, &req.ApprovedBy, &req.ApproverName, &req.ApprovedAt, &req.RejectionReason,
		&req.CreatedAt, &req.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &req, nil
}

func scanRequests(rows pgx.Rows) ([]Request, error) {
	defer rows.Close()
	var out []Request
	for rows.Next() {
		var req Request
		if err := rows.Scan(&req.ID, &req.UserID, &req.UserName, &req.LeaveType, &req.StartDate, &req.EndDate, &req.Reason,
			&req.Status, &req.ApprovedBy, &req.ApproverName, &req.ApprovedAt, &req.RejectionReason,
			&req.CreatedAt, &req.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, req)
	}
	return out, rows.Err()
}

func (r *Repo) Create(ctx context.Context, userID int64, in RequestInput) (*Request, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO leave_requests (user_id, leave_type, start_date, end_date, reason)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		userID, in.LeaveType, in.StartDate, in.EndDate, in.Reason,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

func (r *Repo) Get(ctx context.Context, id int64) (*Request, error) {
	return scanRequest(r.pool.QueryRow(ctx, requestSelect+` WHERE r.id = $1`, id))
}

func (r *Repo) ListForUser(ctx context.Context, userID int64) ([]Request, error) {
	rows, err := r.pool.Query(ctx, requestSelect+` WHERE r.user_id = $1 ORDER BY r.start_date DESC`, userID)
	if err != nil {
		return nil, err
	}
	return scanRequests(rows)
}

// ListPending is org-wide (not scoped by ownership) — any admin reviews any
// employee's request, since time off isn't tied to a project.
func (r *Repo) ListPending(ctx context.Context) ([]Request, error) {
	rows, err := r.pool.Query(ctx, requestSelect+` WHERE r.status = 'submitted' ORDER BY r.start_date`)
	if err != nil {
		return nil, err
	}
	return scanRequests(rows)
}

func (r *Repo) Approve(ctx context.Context, id, approverID int64) error {
	tag, err := r.pool.Exec(ctx, `
		UPDATE leave_requests
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
		UPDATE leave_requests
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

// Delete removes the request only if it belongs to userID and hasn't
// already been reviewed — same "settled record" posture timesheets.Delete
// uses for approved entries.
func (r *Repo) Delete(ctx context.Context, id, userID int64) error {
	tag, err := r.pool.Exec(ctx,
		`DELETE FROM leave_requests WHERE id = $1 AND user_id = $2 AND status = 'submitted'`, id, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
