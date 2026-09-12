package dashboard

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

const previewLimit = 5

// Repo queries tasks/tickets/opportunities/timesheet_entries directly
// rather than importing projects/helpdesk/crm/invoicing/timesheets — same
// narrow-need precedent as internal/search. Each widget mirrors the exact
// visibility rule its owning module already enforces: overdue tasks and
// unbilled hours are personal (assigned-to-me / owner-of-project, since
// only a project's owner can invoice it), while open tickets and pipeline
// value are shown org-wide, matching those modules' own shared, ACL-free
// posture.
type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Load(ctx context.Context, userID int64) (Data, error) {
	var d Data
	var err error

	if d.OverdueTaskCount, d.OverdueTasks, err = r.overdueTasks(ctx, userID); err != nil {
		return d, err
	}
	if d.OpenTicketCount, d.OpenTickets, err = r.openTickets(ctx); err != nil {
		return d, err
	}
	if d.PipelineValue, d.TopOpportunities, err = r.pipelineValue(ctx); err != nil {
		return d, err
	}
	if d.UnbilledHours, d.UnbilledByProject, err = r.unbilledHours(ctx, userID); err != nil {
		return d, err
	}
	return d, nil
}

func (r *Repo) overdueTasks(ctx context.Context, userID int64) (int, []TaskItem, error) {
	var count int
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM tasks
		WHERE assignee_id = $1 AND end_date < CURRENT_DATE AND status <> 'done'`, userID,
	).Scan(&count); err != nil {
		return 0, nil, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.name, p.id, p.name, t.end_date
		FROM tasks t
		JOIN projects p ON p.id = t.project_id
		WHERE t.assignee_id = $1 AND t.end_date < CURRENT_DATE AND t.status <> 'done'
		ORDER BY t.end_date ASC
		LIMIT $2`, userID, previewLimit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	var items []TaskItem
	for rows.Next() {
		var it TaskItem
		if err := rows.Scan(&it.ID, &it.Name, &it.ProjectID, &it.ProjectName, &it.EndDate); err != nil {
			return 0, nil, err
		}
		items = append(items, it)
	}
	return count, items, rows.Err()
}

func (r *Repo) openTickets(ctx context.Context) (int, []TicketItem, error) {
	var count int
	if err := r.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM tickets WHERE status IN ('open', 'in_progress')`,
	).Scan(&count); err != nil {
		return 0, nil, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT id, subject, priority FROM tickets
		WHERE status IN ('open', 'in_progress')
		ORDER BY created_at ASC
		LIMIT $1`, previewLimit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	var items []TicketItem
	for rows.Next() {
		var it TicketItem
		if err := rows.Scan(&it.ID, &it.Subject, &it.Priority); err != nil {
			return 0, nil, err
		}
		items = append(items, it)
	}
	return count, items, rows.Err()
}

func (r *Repo) pipelineValue(ctx context.Context) (float64, []OpportunityItem, error) {
	var total float64
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(o.value_amount), 0)
		FROM opportunities o
		JOIN crm_stages s ON s.id = o.stage_id
		WHERE o.is_lost = false AND s.is_won = false`,
	).Scan(&total); err != nil {
		return 0, nil, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT o.id, o.name, o.value_amount, COALESCE(c.name, '')
		FROM opportunities o
		JOIN crm_stages s ON s.id = o.stage_id
		LEFT JOIN contacts c ON c.id = o.contact_id
		WHERE o.is_lost = false AND s.is_won = false
		ORDER BY o.value_amount DESC
		LIMIT $1`, previewLimit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	var items []OpportunityItem
	for rows.Next() {
		var it OpportunityItem
		if err := rows.Scan(&it.ID, &it.Name, &it.ValueAmount, &it.ContactName); err != nil {
			return 0, nil, err
		}
		items = append(items, it)
	}
	return total, items, rows.Err()
}

func (r *Repo) unbilledHours(ctx context.Context, userID int64) (float64, []ProjectHoursItem, error) {
	var total float64
	if err := r.pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(e.hours), 0)
		FROM timesheet_entries e
		JOIN projects p ON p.id = e.project_id
		WHERE p.owner_id = $1 AND e.billable = true AND e.invoice_id IS NULL`, userID,
	).Scan(&total); err != nil {
		return 0, nil, err
	}

	rows, err := r.pool.Query(ctx, `
		SELECT p.id, p.name, SUM(e.hours) AS hrs
		FROM timesheet_entries e
		JOIN projects p ON p.id = e.project_id
		WHERE p.owner_id = $1 AND e.billable = true AND e.invoice_id IS NULL
		GROUP BY p.id, p.name
		ORDER BY hrs DESC
		LIMIT $2`, userID, previewLimit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()

	var items []ProjectHoursItem
	for rows.Next() {
		var it ProjectHoursItem
		if err := rows.Scan(&it.ProjectID, &it.ProjectName, &it.Hours); err != nil {
			return 0, nil, err
		}
		items = append(items, it)
	}
	return total, items, rows.Err()
}
