package projects

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const milestoneSelect = `
	SELECT m.id, m.project_id, p.name, m.name, m.date, m.created_at
	FROM project_milestones m
	JOIN projects p ON p.id = m.project_id`

func scanMilestones(rows pgx.Rows) ([]Milestone, error) {
	var out []Milestone
	for rows.Next() {
		var m Milestone
		if err := rows.Scan(&m.ID, &m.ProjectID, &m.ProjectName, &m.Name, &m.Date, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *Repo) ListMilestones(ctx context.Context, projectID int64) ([]Milestone, error) {
	rows, err := r.pool.Query(ctx, milestoneSelect+` WHERE m.project_id = $1 ORDER BY m.date, m.id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMilestones(rows)
}

// ListMilestonesBetween scopes across every project userID is a member of —
// same membership-join shape timesheets/reports_repo.go already uses for
// cross-project reporting — for [from, to) (to exclusive).
func (r *Repo) ListMilestonesBetween(ctx context.Context, userID int64, from, to time.Time) ([]Milestone, error) {
	rows, err := r.pool.Query(ctx, milestoneSelect+`
		JOIN project_members pm ON pm.project_id = m.project_id AND pm.user_id = $1
		WHERE m.date >= $2 AND m.date < $3
		ORDER BY m.date, m.id`, userID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMilestones(rows)
}

func (r *Repo) GetMilestone(ctx context.Context, id int64) (*Milestone, error) {
	rows, err := r.pool.Query(ctx, milestoneSelect+` WHERE m.id = $1`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list, err := scanMilestones(rows)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

func (r *Repo) CreateMilestone(ctx context.Context, projectID int64, name string, date time.Time) (*Milestone, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO project_milestones (project_id, name, date) VALUES ($1, $2, $3) RETURNING id`,
		projectID, name, date,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetMilestone(ctx, id)
}

func (r *Repo) DeleteMilestone(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM project_milestones WHERE id = $1`, id)
	return err
}
