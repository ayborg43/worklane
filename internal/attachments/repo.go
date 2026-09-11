package attachments

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

const attachmentSelect = `
	SELECT a.id, a.project_id, COALESCE(a.task_id, 0), a.uploaded_by, u.name,
	       a.filename, a.content_type, a.size_bytes, a.storage_path, a.created_at
	FROM attachments a
	JOIN users u ON u.id = a.uploaded_by`

func scanAttachment(row pgx.Row) (*Attachment, error) {
	var a Attachment
	err := row.Scan(&a.ID, &a.ProjectID, &a.TaskID, &a.UploadedBy, &a.UploaderName,
		&a.Filename, &a.ContentType, &a.SizeBytes, &a.StoragePath, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *Repo) Create(ctx context.Context, a Attachment) (*Attachment, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO attachments (project_id, task_id, uploaded_by, filename, content_type, size_bytes, storage_path)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		a.ProjectID, nullInt64(a.TaskID), a.UploadedBy, a.Filename, a.ContentType, a.SizeBytes, a.StoragePath,
	).Scan(&id)
	if err != nil {
		return nil, err
	}
	return r.Get(ctx, id)
}

func (r *Repo) Get(ctx context.Context, id int64) (*Attachment, error) {
	return scanAttachment(r.pool.QueryRow(ctx, attachmentSelect+` WHERE a.id = $1`, id))
}

// ListForProject returns only project-level files (task_id IS NULL) — files
// attached to a specific task are listed separately via ListForTask.
func (r *Repo) ListForProject(ctx context.Context, projectID int64) ([]Attachment, error) {
	rows, err := r.pool.Query(ctx, attachmentSelect+`
		WHERE a.project_id = $1 AND a.task_id IS NULL ORDER BY a.created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	return scanAttachments(rows)
}

func (r *Repo) ListForTask(ctx context.Context, taskID int64) ([]Attachment, error) {
	rows, err := r.pool.Query(ctx, attachmentSelect+`
		WHERE a.task_id = $1 ORDER BY a.created_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	return scanAttachments(rows)
}

func scanAttachments(rows pgx.Rows) ([]Attachment, error) {
	defer rows.Close()
	var out []Attachment
	for rows.Next() {
		var a Attachment
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.TaskID, &a.UploadedBy, &a.UploaderName,
			&a.Filename, &a.ContentType, &a.SizeBytes, &a.StoragePath, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
