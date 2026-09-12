package portal

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrLinkInvalid covers "never existed", "expired", and "already used"
	// alike — a customer-facing message shouldn't distinguish between them
	// (nothing useful to act on either way, and no reason to help an
	// attacker tell a wrong guess apart from a burned link).
	ErrLinkInvalid = errors.New("magic link invalid or expired")
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// ---------- tokens (magic links + sessions) ----------
//
// Same "random token to the client, SHA-256 hash stored as the DB key"
// shape as auth/session.go, duplicated locally rather than imported —
// portal never imports auth (beyond auth.Middleware/auth.UserFromContext
// for the one internally-triggered SendLink route), keeping the two auth
// paths fully independent so a bug or change in one can't leak into the
// other.

func newToken() (token, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(raw)
	return token, hashToken(token), nil
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (r *Repo) CreateMagicLink(ctx context.Context, contactID int64, ttl time.Duration) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	_, err = r.pool.Exec(ctx,
		`INSERT INTO portal_magic_links (contact_id, token_hash, expires_at) VALUES ($1, $2, $3)`,
		contactID, hash, time.Now().Add(ttl))
	if err != nil {
		return "", err
	}
	return token, nil
}

// RedeemMagicLink marks the link used as part of the same lookup (a single
// atomic UPDATE ... RETURNING) so two near-simultaneous requests for the
// same emailed link can't both succeed.
func (r *Repo) RedeemMagicLink(ctx context.Context, token string) (contactID int64, err error) {
	err = r.pool.QueryRow(ctx, `
		UPDATE portal_magic_links SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		RETURNING contact_id`, hashToken(token),
	).Scan(&contactID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrLinkInvalid
		}
		return 0, err
	}
	return contactID, nil
}

func (r *Repo) CreateSession(ctx context.Context, contactID int64, ttl time.Duration) (token string, expiresAt time.Time, err error) {
	token, hash, err := newToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt = time.Now().Add(ttl)
	_, err = r.pool.Exec(ctx,
		`INSERT INTO portal_sessions (id, contact_id, expires_at) VALUES ($1, $2, $3)`,
		hash, contactID, expiresAt)
	if err != nil {
		return "", time.Time{}, err
	}
	return token, expiresAt, nil
}

func (r *Repo) GetContactBySessionToken(ctx context.Context, token string) (*Contact, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT c.id, c.name, c.email, c.company
		FROM portal_sessions s JOIN contacts c ON c.id = s.contact_id
		WHERE s.id = $1 AND s.expires_at > now()`, hashToken(token))
	return scanContact(row)
}

func (r *Repo) DeleteSessionByToken(ctx context.Context, token string) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM portal_sessions WHERE id = $1`, hashToken(token))
	return err
}

func (r *Repo) DeleteExpiredSessions(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM portal_sessions WHERE expires_at < now()`)
	return err
}

func (r *Repo) DeleteExpiredMagicLinks(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM portal_magic_links WHERE expires_at < now()`)
	return err
}

// ---------- contact lookup ----------

func scanContact(row pgx.Row) (*Contact, error) {
	var c Contact
	if err := row.Scan(&c.ID, &c.Name, &c.Email, &c.Company); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repo) GetContact(ctx context.Context, contactID int64) (*Contact, error) {
	return scanContact(r.pool.QueryRow(ctx, `SELECT id, name, email, company FROM contacts WHERE id = $1`, contactID))
}

// ---------- tickets ----------

const ticketSelect = `SELECT id, subject, priority, status, created_at, resolved_at FROM tickets`

func scanTicket(row pgx.Row) (*Ticket, error) {
	var t Ticket
	if err := row.Scan(&t.ID, &t.Subject, &t.Priority, &t.Status, &t.CreatedAt, &t.ResolvedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &t, nil
}

func (r *Repo) ListTicketsForContact(ctx context.Context, contactID int64) ([]Ticket, error) {
	rows, err := r.pool.Query(ctx, ticketSelect+` WHERE contact_id = $1 ORDER BY created_at DESC`, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Ticket
	for rows.Next() {
		var t Ticket
		if err := rows.Scan(&t.ID, &t.Subject, &t.Priority, &t.Status, &t.CreatedAt, &t.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetTicketForContact scopes the lookup to contactID in the WHERE clause
// (not just fetched-then-checked) so a contact can never view another
// contact's ticket by guessing an id — same 404-hides-existence posture
// requireMember uses elsewhere.
func (r *Repo) GetTicketForContact(ctx context.Context, contactID, ticketID int64) (*Ticket, error) {
	return scanTicket(r.pool.QueryRow(ctx, ticketSelect+` WHERE id = $1 AND contact_id = $2`, ticketID, contactID))
}

func (r *Repo) ListTicketComments(ctx context.Context, ticketID int64) ([]Comment, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.id, u.name, c.body, c.created_at
		FROM ticket_comments c JOIN users u ON u.id = c.user_id
		WHERE c.ticket_id = $1 ORDER BY c.created_at`, ticketID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Comment
	for rows.Next() {
		var c Comment
		if err := rows.Scan(&c.ID, &c.UserName, &c.Body, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ---------- invoices ----------
//
// Scoped via projects.contact_id — a project's designated "client" contact,
// set from the invoicing package's own project-settings page. A contact
// only ever sees invoices for projects explicitly linked to them.

const invoiceSelect = `
	SELECT i.id, p.name, i.period_start, i.period_end, i.status, i.total_amount, i.created_at, u.name
	FROM invoices i
	JOIN projects p ON p.id = i.project_id
	JOIN users u ON u.id = i.created_by`

func scanInvoice(row pgx.Row) (*Invoice, error) {
	var inv Invoice
	err := row.Scan(&inv.ID, &inv.ProjectName, &inv.PeriodStart, &inv.PeriodEnd, &inv.Status,
		&inv.TotalAmount, &inv.CreatedAt, &inv.CreatedByName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &inv, nil
}

func (r *Repo) ListInvoicesForContact(ctx context.Context, contactID int64) ([]Invoice, error) {
	rows, err := r.pool.Query(ctx, invoiceSelect+` WHERE p.contact_id = $1 ORDER BY i.created_at DESC`, contactID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Invoice
	for rows.Next() {
		var inv Invoice
		if err := rows.Scan(&inv.ID, &inv.ProjectName, &inv.PeriodStart, &inv.PeriodEnd, &inv.Status,
			&inv.TotalAmount, &inv.CreatedAt, &inv.CreatedByName); err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

func (r *Repo) GetInvoiceForContact(ctx context.Context, contactID, invoiceID int64) (*Invoice, error) {
	inv, err := scanInvoice(r.pool.QueryRow(ctx, invoiceSelect+` WHERE i.id = $1 AND p.contact_id = $2`, invoiceID, contactID))
	if err != nil {
		return nil, err
	}
	lines, err := r.listInvoiceLines(ctx, invoiceID)
	if err != nil {
		return nil, err
	}
	inv.Lines = lines
	return inv, nil
}

func (r *Repo) listInvoiceLines(ctx context.Context, invoiceID int64) ([]InvoiceLine, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT work_date, user_name, description, hours, rate, amount
		FROM invoice_lines WHERE invoice_id = $1 ORDER BY work_date, id`, invoiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []InvoiceLine
	for rows.Next() {
		var l InvoiceLine
		if err := rows.Scan(&l.WorkDate, &l.UserName, &l.Description, &l.Hours, &l.Rate, &l.Amount); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}
