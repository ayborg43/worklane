package settings

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotConfigured = errors.New("mail is not configured")

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

func (r *Repo) Get(ctx context.Context) (*MailSettings, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT ms.smtp_host, ms.smtp_port, ms.smtp_username, ms.smtp_password,
		       ms.from_address, ms.from_name, ms.use_tls, ms.updated_at, COALESCE(u.name, '')
		FROM mail_settings ms
		LEFT JOIN users u ON u.id = ms.updated_by
		WHERE ms.id = 1`)

	var m MailSettings
	err := row.Scan(&m.SMTPHost, &m.SMTPPort, &m.Username, &m.Password,
		&m.FromAddress, &m.FromName, &m.UseTLS, &m.UpdatedAt, &m.UpdatedByName)
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *Repo) Update(ctx context.Context, m MailSettings, updatedBy int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE mail_settings
		SET smtp_host = $1, smtp_port = $2, smtp_username = $3, smtp_password = $4,
		    from_address = $5, from_name = $6, use_tls = $7, updated_at = now(), updated_by = $8
		WHERE id = 1`,
		m.SMTPHost, m.SMTPPort, m.Username, m.Password, m.FromAddress, m.FromName, m.UseTLS, updatedBy)
	return err
}

// Send delivers an email using the currently-saved settings, re-read on
// every call so a config change takes effect immediately with no restart.
// It satisfies notifications.Mailer structurally — notifications never
// imports this package, keeping that dependency one-directional the same
// way attachments does with its MembershipChecker interface.
func (r *Repo) Send(ctx context.Context, to, subject, body string) error {
	cfg, err := r.Get(ctx)
	if err != nil {
		return err
	}
	if !cfg.Configured() {
		return ErrNotConfigured
	}
	return sendSMTP(*cfg, to, subject, body)
}
