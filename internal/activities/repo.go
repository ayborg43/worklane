package activities

import (
	"context"
	"database/sql"
	"errors"

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

const activitySelect = `
	SELECT a.id, a.project_id, p.name, COALESCE(a.task_id, 0), COALESCE(t.name, ''),
	       a.kind, a.note, a.due_date, a.assigned_to, au.name, a.created_by, cu.name,
	       a.done_at, a.created_at, a.updated_at
	FROM activities a
	JOIN projects p ON p.id = a.project_id
	LEFT JOIN tasks t ON t.id = a.task_id
	JOIN users au ON au.id = a.assigned_to
	JOIN users cu ON cu.id = a.created_by`

func scanActivity(row pgx.Row) (*Activity, error) {
	var a Activity
	err := row.Scan(&a.ID, &a.ProjectID, &a.ProjectName, &a.TaskID, &a.TaskName,
		&a.Kind, &a.Note, &a.DueDate, &a.AssignedTo, &a.AssignedToName, &a.CreatedBy, &a.CreatedByName,
		&a.DoneAt, &a.CreatedAt, &a.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func scanActivities(rows pgx.Rows) ([]Activity, error) {
	defer rows.Close()
	var out []Activity
	for rows.Next() {
		var a Activity
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.ProjectName, &a.TaskID, &a.TaskName,
			&a.Kind, &a.Note, &a.DueDate, &a.AssignedTo, &a.AssignedToName, &a.CreatedBy, &a.CreatedByName,
			&a.DoneAt, &a.CreatedAt, &a.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repo) Create(ctx context.Context, in Input) (*Activity, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO activities (project_id, task_id, kind, note, due_date, assigned_to, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		in.ProjectID, nullInt64(in.TaskID), in.Kind, in.Note, in.DueDate, in.AssignedTo, in.CreatedBy,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

func (r *Repo) Get(ctx context.Context, id int64) (*Activity, error) {
	return scanActivity(r.pool.QueryRow(ctx, activitySelect+` WHERE a.id = $1`, id))
}

// ListForProject returns only project-level activities (task_id IS NULL) —
// activities on a specific task are listed separately via ListForTask.
func (r *Repo) ListForProject(ctx context.Context, projectID int64) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, activitySelect+`
		WHERE a.project_id = $1 AND a.task_id IS NULL
		ORDER BY (a.done_at IS NOT NULL), a.due_date, a.id`, projectID)
	if err != nil {
		return nil, err
	}
	return scanActivities(rows)
}

func (r *Repo) ListForTask(ctx context.Context, taskID int64) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, activitySelect+`
		WHERE a.task_id = $1
		ORDER BY (a.done_at IS NOT NULL), a.due_date, a.id`, taskID)
	if err != nil {
		return nil, err
	}
	return scanActivities(rows)
}

// ListPendingForUser powers the "My Activities" page — every not-done
// activity assigned to userID. Scoping to member projects isn't needed
// here the way reporting/calendar need it: you can only ever be assigned
// an activity on a project you were already a member of at creation time.
func (r *Repo) ListPendingForUser(ctx context.Context, userID int64) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, activitySelect+`
		WHERE a.assigned_to = $1 AND a.done_at IS NULL
		ORDER BY a.due_date, a.id`, userID)
	if err != nil {
		return nil, err
	}
	return scanActivities(rows)
}

func (r *Repo) MarkDone(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE activities SET done_at = now(), updated_at = now() WHERE id = $1`, id)
	return err
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM activities WHERE id = $1`, id)
	return err
}

// ListDueTodayUnnotified mirrors projects.Repo.ListTasksDueTodayUnnotified —
// same "notify exactly once via a sent-at flag" shape.
func (r *Repo) ListDueTodayUnnotified(ctx context.Context) ([]Activity, error) {
	rows, err := r.pool.Query(ctx, activitySelect+`
		WHERE a.due_date = CURRENT_DATE AND a.done_at IS NULL AND a.reminder_sent_at IS NULL
		ORDER BY a.id`)
	if err != nil {
		return nil, err
	}
	return scanActivities(rows)
}

func (r *Repo) MarkReminderSent(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE activities SET reminder_sent_at = now() WHERE id = $1`, id)
	return err
}
