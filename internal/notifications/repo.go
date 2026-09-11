package notifications

import (
	"context"
	"log"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sociolytik/odoo-clone/internal/auth"
)

// Mailer is satisfied structurally by *settings.Repo. notifications never
// imports settings — same one-directional shape as attachments'
// MembershipChecker — so this stays declared locally.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

// Repo records and lists notifications, and — when a Mailer is wired in —
// fans each one out to email too. It still has no knowledge of
// projects/timesheets/tasks specifically; every call site already funnels
// through Create, so that's the single integration point for delivery.
// projects/timesheets import notifications (never the reverse).
type Repo struct {
	pool    *pgxpool.Pool
	users   *auth.Repo
	mailer  Mailer
	baseURL string
}

func NewRepo(pool *pgxpool.Pool, users *auth.Repo, mailer Mailer, baseURL string) *Repo {
	return &Repo{pool: pool, users: users, mailer: mailer, baseURL: baseURL}
}

func (r *Repo) Create(ctx context.Context, userID int64, kind, body, link string) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO notifications (user_id, kind, body, link) VALUES ($1, $2, $3, $4)`,
		userID, kind, body, link)
	if err != nil {
		return err
	}
	if r.mailer != nil {
		// Fire-and-forget: a slow or failing SMTP server must never hold up
		// the request that triggered the notification (same "wrap it,
		// don't fail the caller" convention used at every Create call
		// site). Uses a fresh context since the request's own ends when
		// the handler returns, well before a network round trip to an
		// SMTP server would complete.
		go r.sendMail(userID, kind, body, link)
	}
	return nil
}

func (r *Repo) sendMail(userID int64, kind, body, link string) {
	ctx := context.Background()
	user, err := r.users.GetUserByID(ctx, userID)
	if err != nil {
		log.Printf("notifications: mail: look up user %d: %v", userID, err)
		return
	}
	text := body
	if link != "" && r.baseURL != "" {
		text += "\n\n" + r.baseURL + link
	}
	if err := r.mailer.Send(ctx, user.Email, "Worklane: "+kindLabel(kind), text); err != nil {
		log.Printf("notifications: mail: send to %s: %v", user.Email, err)
	}
}

func kindLabel(kind string) string {
	switch kind {
	case "task_assigned":
		return "Task assigned to you"
	case "task_due":
		return "Task due today"
	case "activity_assigned":
		return "Activity scheduled for you"
	case "activity_due":
		return "Activity due today"
	case "timesheet_approved":
		return "Timesheet entry approved"
	case "timesheet_rejected":
		return "Timesheet entry rejected"
	default:
		return "New notification"
	}
}

func (r *Repo) ListRecentForUser(ctx context.Context, userID int64, limit int) ([]Notification, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT id, user_id, kind, body, link, read_at, created_at
		FROM notifications
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.UserID, &n.Kind, &n.Body, &n.Link, &n.ReadAt, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (r *Repo) UnreadCount(ctx context.Context, userID int64) (int, error) {
	var n int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read_at IS NULL`, userID,
	).Scan(&n)
	return n, err
}

func (r *Repo) MarkAllRead(ctx context.Context, userID int64) error {
	_, err := r.pool.Exec(ctx,
		`UPDATE notifications SET read_at = now() WHERE user_id = $1 AND read_at IS NULL`, userID)
	return err
}
