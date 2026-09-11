package projects

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sociolytik/odoo-clone/internal/auth"
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

// CreateProject inserts the project and adds ownerID as a member in one
// round trip via a CTE, so the two writes are atomic without a separate
// transaction API.
func (r *Repo) CreateProject(ctx context.Context, name, description string, ownerID int64) (*Project, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		WITH new_project AS (
			INSERT INTO projects (name, description, owner_id) VALUES ($1, $2, $3) RETURNING id
		), member AS (
			INSERT INTO project_members (project_id, user_id) SELECT id, $3 FROM new_project
		)
		SELECT id FROM new_project`,
		name, description, ownerID,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetProject(ctx, id)
}

func (r *Repo) ListProjects(ctx context.Context, userID int64) ([]Project, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.name, p.description, p.owner_id, u.name, p.status, p.created_at, p.updated_at,
		       COUNT(t.id)
		FROM projects p
		JOIN users u ON u.id = p.owner_id
		JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $1
		LEFT JOIN tasks t ON t.project_id = p.id
		GROUP BY p.id, u.name
		ORDER BY p.created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Project
	for rows.Next() {
		var p Project
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID, &p.OwnerName, &p.Status,
			&p.CreatedAt, &p.UpdatedAt, &p.TaskCount); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) IsMember(ctx context.Context, projectID, userID int64) (bool, error) {
	var exists bool
	err := r.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM project_members WHERE project_id = $1 AND user_id = $2)`,
		projectID, userID,
	).Scan(&exists)
	return exists, err
}

func (r *Repo) AddMember(ctx context.Context, projectID, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO project_members (project_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		projectID, userID)
	return err
}

func (r *Repo) RemoveMember(ctx context.Context, projectID, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`DELETE FROM project_members WHERE project_id = $1 AND user_id = $2`, projectID, userID)
	return err
}

const memberUserColumns = "u.id, u.email, u.name, u.password_hash, u.created_at, u.updated_at"

func scanUsers(rows pgx.Rows) ([]auth.User, error) {
	defer rows.Close()
	var out []auth.User
	for rows.Next() {
		var u auth.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ListMembers/ListNonMembers query users directly (rather than going through
// auth.Repo) the same way chat.Repo already does for author/DM names — a
// filtered user list isn't something auth.Repo exposes, and adding
// per-caller methods there would be more indirection for no real benefit.
func (r *Repo) ListMembers(ctx context.Context, projectID int64) ([]auth.User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+memberUserColumns+`
		FROM project_members pm JOIN users u ON u.id = pm.user_id
		WHERE pm.project_id = $1 ORDER BY u.name`, projectID)
	if err != nil {
		return nil, err
	}
	return scanUsers(rows)
}

func (r *Repo) ListNonMembers(ctx context.Context, projectID int64) ([]auth.User, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT `+memberUserColumns+`
		FROM users u
		WHERE NOT EXISTS (SELECT 1 FROM project_members pm WHERE pm.project_id = $1 AND pm.user_id = u.id)
		ORDER BY u.name`, projectID)
	if err != nil {
		return nil, err
	}
	return scanUsers(rows)
}

func (r *Repo) GetProject(ctx context.Context, id int64) (*Project, error) {
	var p Project
	err := r.pool.QueryRow(ctx, `
		SELECT p.id, p.name, p.description, p.owner_id, u.name, p.status, p.created_at, p.updated_at
		FROM projects p JOIN users u ON u.id = p.owner_id
		WHERE p.id = $1`, id,
	).Scan(&p.ID, &p.Name, &p.Description, &p.OwnerID, &p.OwnerName, &p.Status, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

const taskSelect = `
	SELECT t.id, t.project_id, COALESCE(t.parent_task_id, 0), t.name, t.description,
	       COALESCE(t.assignee_id, 0), COALESCE(u.name, ''), t.start_date, t.end_date,
	       t.progress, t.status, t.sort_order, t.created_at, t.updated_at,
	       COALESCE(t.recurrence_unit, ''), COALESCE(t.recurrence_interval, 0), t.recurrence_until
	FROM tasks t
	LEFT JOIN users u ON u.id = t.assignee_id`

func scanTask(row pgx.Row) (*Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.ProjectID, &t.ParentTaskID, &t.Name, &t.Description,
		&t.AssigneeID, &t.AssigneeName, &t.StartDate, &t.EndDate,
		&t.Progress, &t.Status, &t.SortOrder, &t.CreatedAt, &t.UpdatedAt,
		&t.RecurrenceUnit, &t.RecurrenceInterval, &t.RecurrenceUntil)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func scanTasks(rows pgx.Rows) ([]Task, error) {
	defer rows.Close()
	var tasks []Task
	for rows.Next() {
		var t Task
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.ParentTaskID, &t.Name, &t.Description,
			&t.AssigneeID, &t.AssigneeName, &t.StartDate, &t.EndDate,
			&t.Progress, &t.Status, &t.SortOrder, &t.CreatedAt, &t.UpdatedAt,
			&t.RecurrenceUnit, &t.RecurrenceInterval, &t.RecurrenceUntil); err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}

func (r *Repo) ListTasks(ctx context.Context, projectID int64) ([]Task, error) {
	rows, err := r.pool.Query(ctx, taskSelect+` WHERE t.project_id = $1 ORDER BY t.sort_order, t.start_date, t.id`, projectID)
	if err != nil {
		return nil, err
	}
	tasks, err := scanTasks(rows)
	if err != nil {
		return nil, err
	}

	deps, err := r.dependenciesByProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	for i := range tasks {
		tasks[i].DependsOn = deps[tasks[i].ID]
	}
	return tasks, nil
}

// ListTasksDueTodayUnnotified returns assigned, not-done tasks whose due
// date is today and that haven't had a due-date reminder sent yet — the
// due_reminder_sent_at flag makes each task's reminder fire exactly once no
// matter how often the reminder scan runs.
func (r *Repo) ListTasksDueTodayUnnotified(ctx context.Context) ([]Task, error) {
	rows, err := r.pool.Query(ctx, taskSelect+`
		WHERE t.end_date = CURRENT_DATE AND t.status <> 'done'
		      AND t.assignee_id IS NOT NULL AND t.due_reminder_sent_at IS NULL
		ORDER BY t.id`)
	if err != nil {
		return nil, err
	}
	return scanTasks(rows)
}

func (r *Repo) MarkDueReminderSent(ctx context.Context, taskID int64) error {
	_, err := r.pool.Exec(ctx, `UPDATE tasks SET due_reminder_sent_at = now() WHERE id = $1`, taskID)
	return err
}

// ListTasksDueBetween scopes across every project userID is a member of —
// same membership-join shape ListMilestonesBetween and
// timesheets/reports_repo.go use — for [from, to) (to exclusive). Used by
// the calendar view; dependencies aren't loaded since the calendar only
// needs each task's name/date/link, not its dependency graph.
func (r *Repo) ListTasksDueBetween(ctx context.Context, userID int64, from, to time.Time) ([]Task, error) {
	rows, err := r.pool.Query(ctx, taskSelect+`
		JOIN project_members pm ON pm.project_id = t.project_id AND pm.user_id = $1
		WHERE t.end_date >= $2 AND t.end_date < $3
		ORDER BY t.end_date, t.id`, userID, from, to)
	if err != nil {
		return nil, err
	}
	return scanTasks(rows)
}

// ListSubtasks returns child tasks of parentTaskID, in the same sort_order
// convention as ListTasks. Dependencies aren't loaded — subtasks shown on
// the parent's detail page don't need their own dependency graph there.
func (r *Repo) ListSubtasks(ctx context.Context, parentTaskID int64) ([]Task, error) {
	rows, err := r.pool.Query(ctx, taskSelect+`
		WHERE t.parent_task_id = $1 ORDER BY t.sort_order, t.start_date, t.id`, parentTaskID)
	if err != nil {
		return nil, err
	}
	return scanTasks(rows)
}

// SetRecurrence replaces a task's recurrence settings as a group — unit ==
// "" clears all three columns back to NULL (turns recurrence off).
func (r *Repo) SetRecurrence(ctx context.Context, taskID int64, unit string, interval int, until *time.Time) error {
	var unitArg sql.NullString
	var intervalArg sql.NullInt32
	if unit != "" {
		unitArg = sql.NullString{String: unit, Valid: true}
		intervalArg = sql.NullInt32{Int32: int32(interval), Valid: true}
	}
	_, err := r.pool.Exec(ctx, `
		UPDATE tasks SET recurrence_unit = $1, recurrence_interval = $2, recurrence_until = $3, updated_at = now()
		WHERE id = $4`, unitArg, intervalArg, until, taskID)
	return err
}

func (r *Repo) GetTask(ctx context.Context, id int64) (*Task, error) {
	t, err := scanTask(r.pool.QueryRow(ctx, taskSelect+` WHERE t.id = $1`, id))
	if err != nil {
		return nil, err
	}
	deps, err := r.dependenciesForTask(ctx, id)
	if err != nil {
		return nil, err
	}
	t.DependsOn = deps
	return t, nil
}

func (r *Repo) dependenciesByProject(ctx context.Context, projectID int64) (map[int64][]int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT d.task_id, d.depends_on_task_id
		FROM task_dependencies d
		JOIN tasks t ON t.id = d.task_id
		WHERE t.project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64][]int64)
	for rows.Next() {
		var taskID, dependsOn int64
		if err := rows.Scan(&taskID, &dependsOn); err != nil {
			return nil, err
		}
		out[taskID] = append(out[taskID], dependsOn)
	}
	return out, rows.Err()
}

func (r *Repo) dependenciesForTask(ctx context.Context, taskID int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT depends_on_task_id FROM task_dependencies WHERE task_id = $1`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (r *Repo) CreateTask(ctx context.Context, projectID int64, in TaskInput) (*Task, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO tasks (project_id, parent_task_id, name, description, assignee_id, start_date, end_date, progress, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		projectID, nullInt64(in.ParentTaskID), in.Name, in.Description, nullInt64(in.AssigneeID), in.StartDate, in.EndDate, in.Progress, in.Status,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetTask(ctx, id)
}

func (r *Repo) UpdateTask(ctx context.Context, id int64, in TaskInput) (*Task, error) {
	// Resetting due_reminder_sent_at whenever end_date actually changes means
	// pushing a task's due date out (or back onto today) gets a fresh
	// reminder rather than staying silenced by a reminder sent for a
	// previous due date.
	_, err := r.pool.Exec(ctx, `
		UPDATE tasks SET name = $1, description = $2, assignee_id = $3, start_date = $4,
		       end_date = $5, progress = $6, status = $7, updated_at = now(),
		       due_reminder_sent_at = CASE WHEN end_date <> $5 THEN NULL ELSE due_reminder_sent_at END
		WHERE id = $8`,
		in.Name, in.Description, nullInt64(in.AssigneeID), in.StartDate, in.EndDate, in.Progress, in.Status, id,
	)
	if err != nil {
		return nil, err
	}
	return r.GetTask(ctx, id)
}

func (r *Repo) DeleteTask(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM tasks WHERE id = $1`, id)
	return err
}

func (r *Repo) AddDependency(ctx context.Context, taskID, dependsOnID int64) error {
	_, err := r.pool.Exec(ctx, `
		INSERT INTO task_dependencies (task_id, depends_on_task_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, taskID, dependsOnID)
	return err
}

func (r *Repo) RemoveDependency(ctx context.Context, taskID, dependsOnID int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM task_dependencies WHERE task_id = $1 AND depends_on_task_id = $2`, taskID, dependsOnID)
	return err
}
