package auth

import (
	"context"
	"errors"
	"strings"
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

// CreateUser makes the very first registered user an admin — the
// NOT EXISTS subquery is evaluated against the table's state before this
// row is inserted, so it's true only when the table was empty going in.
// This is what actually bootstraps an admin on a fresh database; migration
// 0011's backfill only helps installs that already had users when it ran.
func (r *Repo) CreateUser(ctx context.Context, email, name, passwordHash string) (*User, error) {
	row := r.pool.QueryRow(ctx,
		`INSERT INTO users (email, name, password_hash, is_admin)
		 VALUES ($1, $2, $3, NOT EXISTS (SELECT 1 FROM users))
		 RETURNING `+userColumns,
		email, name, passwordHash,
	)
	return scanUser(row)
}

// EnsureAdminExists promotes the earliest-registered user to admin if no
// admin currently exists. A self-healing safety net alongside CreateUser's
// first-user-becomes-admin logic — covers any database that predates that
// fix, or otherwise ends up with zero admins. Cheap enough to call once on
// every server startup.
func (r *Repo) EnsureAdminExists(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE users SET is_admin = true
		WHERE id = (SELECT id FROM users ORDER BY id ASC LIMIT 1)
		  AND NOT EXISTS (SELECT 1 FROM users WHERE is_admin = true)`)
	return err
}

// PromoteAdminByEmail sets is_admin = true for the user matching email, if
// one has already registered. Deliberately does nothing else: it never
// creates an account and never touches a password, so it's safe to leave
// ADMIN_EMAIL set in the environment indefinitely without it silently
// resetting anyone's credentials on every restart. A no-op (not an error)
// when email is empty or doesn't match any registered user yet.
func (r *Repo) PromoteAdminByEmail(ctx context.Context, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	_, err := r.pool.Exec(ctx, `UPDATE users SET is_admin = true WHERE email = $1`, email)
	return err
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
