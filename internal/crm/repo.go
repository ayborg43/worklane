package crm

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

// ---------- contacts ----------

func (r *Repo) ListContacts(ctx context.Context) ([]Contact, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, name, company, email, phone, created_by, created_at, updated_at
		FROM contacts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.ID, &c.Name, &c.Company, &c.Email, &c.Phone, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) GetContact(ctx context.Context, id int64) (*Contact, error) {
	var c Contact
	err := r.pool.QueryRow(ctx, `
		SELECT id, name, company, email, phone, created_by, created_at, updated_at
		FROM contacts WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.Company, &c.Email, &c.Phone, &c.CreatedBy, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repo) CreateContact(ctx context.Context, name, company, email, phone string, createdBy int64) (*Contact, error) {
	var id int64
	err := r.pool.QueryRow(ctx,
		`INSERT INTO contacts (name, company, email, phone, created_by) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		name, company, email, phone, createdBy,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetContact(ctx, id)
}

func (r *Repo) UpdateContact(ctx context.Context, id int64, name, company, email, phone string) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE contacts SET name = $1, company = $2, email = $3, phone = $4, updated_at = now() WHERE id = $5`,
		name, company, email, phone, id)
	return err
}

func (r *Repo) DeleteContact(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM contacts WHERE id = $1`, id)
	return err
}

// ---------- stages ----------

func (r *Repo) ListStages(ctx context.Context) ([]Stage, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, name, sort_order, is_won, created_at FROM crm_stages ORDER BY sort_order, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Stage
	for rows.Next() {
		var s Stage
		if err := rows.Scan(&s.ID, &s.Name, &s.SortOrder, &s.IsWon, &s.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (r *Repo) GetStage(ctx context.Context, id int64) (*Stage, error) {
	var s Stage
	err := r.pool.QueryRow(ctx, `SELECT id, name, sort_order, is_won, created_at FROM crm_stages WHERE id = $1`, id).Scan(&s.ID, &s.Name, &s.SortOrder, &s.IsWon, &s.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &s, nil
}

// CreateStage appends the new stage after every existing one — sort_order
// is just "max + 1", not a value anyone edits directly.
func (r *Repo) CreateStage(ctx context.Context, name string) (*Stage, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO crm_stages (name, sort_order)
		VALUES ($1, COALESCE((SELECT MAX(sort_order) + 1 FROM crm_stages), 0))
		RETURNING id`, name,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetStage(ctx, id)
}

func (r *Repo) DeleteStage(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM crm_stages WHERE id = $1`, id)
	return err
}

// CountOpportunitiesInStage guards stage deletion — deleting a stage with
// opportunities still in it would either cascade-orphan them or fail the
// FK constraint, neither of which is a good silent behavior.
func (r *Repo) CountOpportunitiesInStage(ctx context.Context, stageID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM opportunities WHERE stage_id = $1`, stageID).Scan(&n)
	return n, err
}

// SwapStageOrder exchanges two stages' sort_order values — the mechanics
// behind the "Manage stages" section's up/down reorder buttons.
func (r *Repo) SwapStageOrder(ctx context.Context, aID, bID int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var aOrder, bOrder int
	if err := tx.QueryRow(ctx, `SELECT sort_order FROM crm_stages WHERE id = $1`, aID).Scan(&aOrder); err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, `SELECT sort_order FROM crm_stages WHERE id = $1`, bID).Scan(&bOrder); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE crm_stages SET sort_order = $1 WHERE id = $2`, bOrder, aID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE crm_stages SET sort_order = $1 WHERE id = $2`, aOrder, bID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ---------- opportunities ----------

const opportunitySelect = `
	SELECT o.id, o.name, COALESCE(o.contact_id, 0), COALESCE(c.name, ''), COALESCE(c.company, ''),
	       o.stage_id, s.name, o.owner_id, COALESCE(u.name, ''), o.value_amount, o.expected_close_date,
	       o.is_lost, o.lost_reason, o.created_by, o.created_at, o.updated_at
	FROM opportunities o
	JOIN crm_stages s ON s.id = o.stage_id
	LEFT JOIN contacts c ON c.id = o.contact_id
	LEFT JOIN users u ON u.id = o.owner_id`

func scanOpportunity(row pgx.Row) (*Opportunity, error) {
	var o Opportunity
	err := row.Scan(&o.ID, &o.Name, &o.ContactID, &o.ContactName, &o.ContactCompany,
		&o.StageID, &o.StageName, &o.OwnerID, &o.OwnerName, &o.ValueAmount, &o.ExpectedCloseDate,
		&o.IsLost, &o.LostReason, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &o, nil
}

func scanOpportunities(rows pgx.Rows) ([]Opportunity, error) {
	defer rows.Close()
	var out []Opportunity
	for rows.Next() {
		var o Opportunity
		if err := rows.Scan(&o.ID, &o.Name, &o.ContactID, &o.ContactName, &o.ContactCompany,
			&o.StageID, &o.StageName, &o.OwnerID, &o.OwnerName, &o.ValueAmount, &o.ExpectedCloseDate,
			&o.IsLost, &o.LostReason, &o.CreatedBy, &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// ListPipeline returns every not-lost opportunity when includeLost is
// false (the normal Kanban view), or every opportunity when true (the
// "Show lost" toggle).
func (r *Repo) ListPipeline(ctx context.Context, includeLost bool) ([]Opportunity, error) {
	query := opportunitySelect
	if !includeLost {
		query += ` WHERE o.is_lost = false`
	}
	query += ` ORDER BY o.created_at DESC`
	rows, err := r.pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	return scanOpportunities(rows)
}

func (r *Repo) GetOpportunity(ctx context.Context, id int64) (*Opportunity, error) {
	return scanOpportunity(r.pool.QueryRow(ctx, opportunitySelect+` WHERE o.id = $1`, id))
}

func (r *Repo) CreateOpportunity(ctx context.Context, in OpportunityInput, createdBy int64) (*Opportunity, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO opportunities (name, contact_id, stage_id, owner_id, value_amount, expected_close_date, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		in.Name, nullInt64(in.ContactID), in.StageID, in.OwnerID, in.ValueAmount, in.ExpectedCloseDate, createdBy,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.GetOpportunity(ctx, id)
}

func (r *Repo) UpdateOpportunity(ctx context.Context, id int64, in OpportunityInput) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE opportunities SET name = $1, contact_id = $2, stage_id = $3, owner_id = $4,
		       value_amount = $5, expected_close_date = $6, updated_at = now()
		WHERE id = $7`,
		in.Name, nullInt64(in.ContactID), in.StageID, in.OwnerID, in.ValueAmount, in.ExpectedCloseDate, id)
	return err
}

func (r *Repo) MarkLost(ctx context.Context, id int64, reason string) error {
	_, err := r.pool.Exec(ctx, `UPDATE opportunities SET is_lost = true, lost_reason = $1, updated_at = now() WHERE id = $2`, reason, id)
	return err
}

func (r *Repo) MarkWon(ctx context.Context, id, wonStageID int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE opportunities SET stage_id = $1, is_lost = false, lost_reason = '', updated_at = now() WHERE id = $2`,
		wonStageID, id)
	return err
}

func (r *Repo) DeleteOpportunity(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM opportunities WHERE id = $1`, id)
	return err
}

// PipelineValueByStage sums value_amount per stage across active (not
// lost) opportunities — the small per-column total shown on the Kanban
// board, the way a real CRM pipeline view does.
func (r *Repo) PipelineValueByStage(ctx context.Context) (map[int64]float64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT stage_id, COALESCE(SUM(value_amount), 0)
		FROM opportunities WHERE is_lost = false GROUP BY stage_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[int64]float64)
	for rows.Next() {
		var stageID int64
		var total float64
		if err := rows.Scan(&stageID, &total); err != nil {
			return nil, err
		}
		out[stageID] = total
	}
	return out, rows.Err()
}
