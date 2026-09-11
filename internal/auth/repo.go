package auth

import (
	"context"
	"errors"
	"time"

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

const userColumns = "id, email, name, password_hash, is_admin, created_at, updated_at"

func scanUser(row pgx.Row) (*User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &u, nil
}

func (r *Repo) CreateUser(ctx context.Context, email, name, passwordHash string) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, name, password_hash) VALUES ($1, $2, $3) RETURNING `+userColumns,
		email, name, passwordHash,
	)
	return scanUser(row)
}

func (r *Repo) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE email = $1`, email)
	return scanUser(row)
}

func (r *Repo) GetUserByID(ctx context.Context, id int64) (*User, error) {
	row := r.pool.QueryRow(ctx, `SELECT `+userColumns+` FROM users WHERE id = $1`, id)
	return scanUser(row)
}

// ListUsers returns all users ordered by name — used by Discuss to populate
// the "start a DM" picker.
func (r *Repo) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+userColumns+` FROM users ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.IsAdmin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, rows.Err()
}

func (r *Repo) CreateSession(ctx context.Context, userID int64, ttl time.Duration, userAgent, ip string) (token string, expiresAt time.Time, err error) {
	token, hash, err := newSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(ttl)
	_, err = r.pool.Exec(ctx,
		`INSERT INTO sessions (id, user_id, expires_at, user_agent, ip_address) VALUES ($1, $2, $3, $4, $5)`,
		hash, userID, expiresAt, userAgent, ip,
	)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (r *Repo) GetUserBySessionToken(ctx context.Context, token string) (*User, error) {
	hash := hashToken(token)
	row := r.pool.QueryRow(ctx,
		`SELECT u.id, u.email, u.name, u.password_hash, u.is_admin, u.created_at, u.updated_at
		 FROM sessions s JOIN users u ON u.id = s.user_id
		 WHERE s.id = $1 AND s.expires_at > now()`,
		hash,
	)
	return scanUser(row)
}

func (r *Repo) DeleteSessionByToken(ctx context.Context, token string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE id = $1`, hashToken(token))
	return err
}

func (r *Repo) DeleteExpiredSessions(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM sessions WHERE expires_at < now()`)
	return err
}
