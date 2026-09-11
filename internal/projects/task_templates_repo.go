package projects

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const taskTemplateSelect = `
	SELECT tt.id, tt.project_id, tt.name, tt.description, tt.default_duration_days,
	       COALESCE(tt.default_assignee_id, 0), COALESCE(u.name, ''), tt.created_at
	FROM task_templates tt
	LEFT JOIN users u ON u.id = tt.default_assignee_id`

func scanTaskTemplates(rows pgx.Rows) ([]TaskTemplate, error) {
	var out []TaskTemplate
	for rows.Next() {
		var t TaskTemplate
		if err := rows.Scan(&t.ID, &t.ProjectID, &t.Name, &t.Description, &t.DefaultDurationDays,
			&t.DefaultAssigneeID, &t.DefaultAssigneeName, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (r *Repo) ListTaskTemplates(ctx context.Context, projectID int64) ([]TaskTemplate, error) {
	rows, err := r.pool.Query(ctx, taskTemplateSelect+` WHERE tt.project_id = $1 ORDER BY tt.name`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanTaskTemplates(rows)
}

func (r *Repo) GetTaskTemplate(ctx context.Context, id int64) (*TaskTemplate, error) {
	rows, err := r.pool.Query(ctx, taskTemplateSelect+` WHERE tt.id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := scanTaskTemplates(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (r *Repo) CreateTaskTemplate(ctx context.Context, projectID int64, name, description string, durationDays int, assigneeID int64) (*TaskTemplate, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO task_templates (project_id, name, description, default_duration_days, default_assignee_id)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		projectID, name, description, durationDays, nullInt64(assigneeID),
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetTaskTemplate(ctx, id)
}

func (r *Repo) DeleteTaskTemplate(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM task_templates WHERE id = $1`, id)
	return err
}
