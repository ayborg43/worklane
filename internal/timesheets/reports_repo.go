package timesheets

import (
	"context"
	"time"
)

type ProjectHours struct {
	ProjectID   int64
	ProjectName string
	Hours       float64
	Pct         int
}

type PersonHours struct {
	UserID   int64
	UserName string
	Hours    float64
	Pct      int
}

type WeekHours struct {
	WeekStart time.Time
	Hours     float64
	Pct       int
}

// HoursByProject/HoursByPerson/HoursByWeek all scope through project_members
// so a report only ever reflects projects userID actually belongs to.
// Totals include entries of every status (submitted/approved/rejected) —
// nothing asked for status filtering; excluding rejected entries later is a
// one-line WHERE addition if wanted.
func (r *Repo) HoursByProject(ctx context.Context, userID int64) ([]ProjectHours, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.name, COALESCE(SUM(e.hours), 0)
		FROM projects p
		JOIN project_members pm ON pm.project_id = p.id AND pm.user_id = $1
		LEFT JOIN timesheet_entries e ON e.project_id = p.id
		GROUP BY p.id, p.name
		HAVING COALESCE(SUM(e.hours), 0) > 0
		ORDER BY SUM(e.hours) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ProjectHours
	for rows.Next() {
		var p ProjectHours
		if err := rows.Scan(&p.ProjectID, &p.ProjectName, &p.Hours); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) HoursByPerson(ctx context.Context, userID int64) ([]PersonHours, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT u.id, u.name, SUM(e.hours)
		FROM timesheet_entries e
		JOIN users u ON u.id = e.user_id
		JOIN project_members pm ON pm.project_id = e.project_id AND pm.user_id = $1
		GROUP BY u.id, u.name
		ORDER BY SUM(e.hours) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PersonHours
	for rows.Next() {
		var p PersonHours
		if err := rows.Scan(&p.UserID, &p.UserName, &p.Hours); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// HoursByWeek uses Postgres's date_trunc('week', ...), which truncates to
// Monday the same way this package's own mondayOf does, so report week
// boundaries line up with the Timesheets week view.
func (r *Repo) HoursByWeek(ctx context.Context, userID int64, weeks int) ([]WeekHours, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT date_trunc('week', e.work_date)::date AS week_start, SUM(e.hours)
		FROM timesheet_entries e
		JOIN project_members pm ON pm.project_id = e.project_id AND pm.user_id = $1
		WHERE e.work_date >= (CURRENT_DATE - ($2 * INTERVAL '1 week'))
		GROUP BY week_start
		ORDER BY week_start`, userID, weeks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []WeekHours
	for rows.Next() {
		var w WeekHours
		if err := rows.Scan(&w.WeekStart, &w.Hours); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}
