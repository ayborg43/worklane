package wiki

import (
	"context"
	"errors"
	"strings"

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

const pageSelect = `
	SELECT p.id, p.project_id, p.title, p.body, p.created_by, c.name, p.updated_by, u.name,
	       p.created_at, p.updated_at
	FROM wiki_pages p
	JOIN users c ON c.id = p.created_by
	JOIN users u ON u.id = p.updated_by`

func scanPage(row pgx.Row) (*Page, error) {
	var p Page
	err := row.Scan(&p.ID, &p.ProjectID, &p.Title, &p.Body, &p.CreatedBy, &p.CreatedByName,
		&p.UpdatedBy, &p.UpdatedByName, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// ListForProject orders alphabetically by title (a wiki's page list reads
// better as an index than a chronological feed).
func (r *Repo) ListForProject(ctx context.Context, projectID int64) ([]Page, error) {
	rows, err := r.pool.Query(ctx, pageSelect+` WHERE p.project_id = $1 ORDER BY p.title`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Page
	for rows.Next() {
		var p Page
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.Title, &p.Body, &p.CreatedBy, &p.CreatedByName,
			&p.UpdatedBy, &p.UpdatedByName, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *Repo) Get(ctx context.Context, id int64) (*Page, error) {
	return scanPage(r.pool.QueryRow(ctx, pageSelect+` WHERE p.id = $1`, id))
}

func (r *Repo) Create(ctx context.Context, projectID int64, in PageInput, userID int64) (*Page, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO wiki_pages (project_id, title, body, created_by, updated_by)
		VALUES ($1, $2, $3, $4, $4) RETURNING id`,
		projectID, in.Title, in.Body, userID,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

func (r *Repo) Update(ctx context.Context, id int64, in PageInput, userID int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE wiki_pages SET title = $1, body = $2, updated_by = $3, updated_at = now() WHERE id = $4`,
		in.Title, in.Body, userID, id)
	return err
}

func (r *Repo) Delete(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM wiki_pages WHERE id = $1`, id)
	return err
}

// TitleIndex maps every page title in a project (lowercased) to its id —
// how RenderMarkdown resolves [[Page Title]] cross-links.
func (r *Repo) TitleIndex(ctx context.Context, projectID int64) (map[string]int64, error) {
	rows, err := r.pool.Query(ctx, `SELECT id, title FROM wiki_pages WHERE project_id = $1`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]int64)
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out[normalizeTitle(title)] = id
	}
	return out, rows.Err()
}

// ProjectName reads the projects table directly rather than importing
// projects.Repo — same narrow-need precedent invoicing.Repo.ProjectInfo
// and projects.Repo.ListMembers (querying users directly) already set.
func (r *Repo) ProjectName(ctx context.Context, projectID int64) (string, error) {
	var name string
	err := r.pool.QueryRow(ctx, `SELECT name FROM projects WHERE id = $1`, projectID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return name, nil
}

func normalizeTitle(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
