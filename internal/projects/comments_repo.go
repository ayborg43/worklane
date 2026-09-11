package projects

import "context"

func (r *Repo) ListComments(ctx context.Context, taskID int64) ([]Comment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, c.task_id, c.user_id, u.name, c.body, c.created_at
		FROM task_comments c JOIN users u ON u.id = c.user_id
		WHERE c.task_id = $1 ORDER BY c.created_at, c.id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.UserID, &c.UserName, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) CreateComment(ctx context.Context, taskID, userID int64, body string) (*Comment, error) {
	var c Comment
	err := r.pool.QueryRow(ctx, `
		INSERT INTO task_comments (task_id, user_id, body) VALUES ($1, $2, $3)
		RETURNING id, task_id, user_id, body, created_at`,
		taskID, userID, body,
	).Scan(&c.ID, &c.TaskID, &c.UserID, &c.Body, &c.CreatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}
