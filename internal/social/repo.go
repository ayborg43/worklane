package social

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrOAuthStateInvalid = errors.New("oauth state invalid or expired")
	// ErrNotImplemented covers a platform with no working Provider yet
	// (everything except "x" today) — see AppCredentials.Implemented.
	ErrNotImplemented = errors.New("this platform isn't connected yet")
)

type Repo struct {
	pool *pgxpool.Pool
}

func NewRepo(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// ---------- app credentials ----------

func (r *Repo) ListAppCredentials(ctx context.Context) ([]AppCredentials, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT c.platform, c.client_id, c.client_secret, c.updated_at, COALESCE(u.name, '')
		FROM social_app_credentials c
		LEFT JOIN users u ON u.id = c.updated_by
		ORDER BY array_position($1::text[], c.platform)`, Platforms)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []AppCredentials
	for rows.Next() {
		var c AppCredentials
		if err := rows.Scan(&c.Platform, &c.ClientID, &c.ClientSecret, &c.UpdatedAt, &c.UpdatedByName); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *Repo) GetAppCredentials(ctx context.Context, platform string) (*AppCredentials, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT c.platform, c.client_id, c.client_secret, c.updated_at, COALESCE(u.name, '')
		FROM social_app_credentials c
		LEFT JOIN users u ON u.id = c.updated_by
		WHERE c.platform = $1`, platform)
	var c AppCredentials
	err := row.Scan(&c.Platform, &c.ClientID, &c.ClientSecret, &c.UpdatedAt, &c.UpdatedByName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (r *Repo) UpdateAppCredentials(ctx context.Context, platform, clientID, clientSecret string, updatedBy int64) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE social_app_credentials
		SET client_id = $1, client_secret = $2, updated_at = now(), updated_by = $3
		WHERE platform = $4`, clientID, clientSecret, updatedBy, platform)
	return err
}

// ---------- connected accounts ----------

func (r *Repo) ListAccounts(ctx context.Context) ([]Account, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT a.id, a.platform, a.label, a.external_account_id, a.access_token, a.refresh_token,
		       a.token_expires_at, COALESCE(u.name, ''), a.created_at
		FROM social_accounts a
		JOIN users u ON u.id = a.connected_by
		ORDER BY a.created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(&a.ID, &a.Platform, &a.Label, &a.ExternalAccountID, &a.AccessToken, &a.RefreshToken,
			&a.TokenExpiresAt, &a.ConnectedByName, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Repo) GetAccount(ctx context.Context, id int64) (*Account, error) {
	row := r.pool.QueryRow(ctx, `
		SELECT a.id, a.platform, a.label, a.external_account_id, a.access_token, a.refresh_token,
		       a.token_expires_at, COALESCE(u.name, ''), a.created_at
		FROM social_accounts a
		JOIN users u ON u.id = a.connected_by
		WHERE a.id = $1`, id)
	var a Account
	err := row.Scan(&a.ID, &a.Platform, &a.Label, &a.ExternalAccountID, &a.AccessToken, &a.RefreshToken,
		&a.TokenExpiresAt, &a.ConnectedByName, &a.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

func (r *Repo) CreateAccount(ctx context.Context, platform, label, externalID, accessToken, refreshToken string, expiresAt time.Time, connectedBy int64) (int64, error) {
	var id int64
	err := r.pool.QueryRow(ctx, `
		INSERT INTO social_accounts (platform, label, external_account_id, access_token, refresh_token, token_expires_at, connected_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		platform, label, externalID, accessToken, refreshToken, expiresAt, connectedBy,
	).Scan(&id)
	return id, err
}

func (r *Repo) updateAccountTokens(ctx context.Context, id int64, accessToken, refreshToken string, expiresAt time.Time) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE social_accounts SET access_token = $1, refresh_token = $2, token_expires_at = $3, updated_at = now()
		WHERE id = $4`, accessToken, refreshToken, expiresAt, id)
	return err
}

func (r *Repo) DeleteAccount(ctx context.Context, id int64) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM social_accounts WHERE id = $1`, id)
	return err
}

// ---------- OAuth state (authorize -> callback round trip) ----------

func newOpaqueToken() (string, error) {
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// StartOAuth generates a state+PKCE pair, stores it briefly, and returns
// the URL to redirect the admin's browser to.
func (r *Repo) StartOAuth(ctx context.Context, platform, clientID, redirectURI string, initiatedBy int64) (redirectURL string, err error) {
	state, err := newOpaqueToken()
	if err != nil {
		return "", err
	}
	verifier, challenge, err := newPKCEPair()
	if err != nil {
		return "", err
	}
	_, err = r.pool.Exec(ctx, `
		INSERT INTO social_oauth_states (state, platform, code_verifier, initiated_by, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		state, platform, verifier, initiatedBy, time.Now().Add(10*time.Minute))
	if err != nil {
		return "", err
	}
	switch platform {
	case "x":
		return xAuthorizeRedirectURL(clientID, redirectURI, state, challenge), nil
	default:
		return "", ErrNotImplemented
	}
}

type oauthState struct {
	Platform     string
	CodeVerifier string
	InitiatedBy  int64
}

// consumeOAuthState deletes the state row as part of the same lookup
// (single-use, same idea as portal's magic links) so a replayed callback
// can't be processed twice.
func (r *Repo) consumeOAuthState(ctx context.Context, state string) (*oauthState, error) {
	row := r.pool.QueryRow(ctx, `
		DELETE FROM social_oauth_states
		WHERE state = $1 AND expires_at > now()
		RETURNING platform, code_verifier, initiated_by`, state)
	var s oauthState
	if err := row.Scan(&s.Platform, &s.CodeVerifier, &s.InitiatedBy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrOAuthStateInvalid
		}
		return nil, err
	}
	return &s, nil
}

func (r *Repo) DeleteExpiredOAuthStates(ctx context.Context) error {
	_, err := r.pool.Exec(ctx, `DELETE FROM social_oauth_states WHERE expires_at < now()`)
	return err
}

// FinishOAuth exchanges the authorization code for tokens and stores the
// newly connected account. redirectURI must exactly match what was sent to
// StartOAuth (and what's registered with the platform). connectedBy must
// match whichever admin actually started this specific flow — the state
// token is already an unguessable per-flow secret, but this is a cheap
// extra check against it somehow being replayed into a different session.
func (r *Repo) FinishOAuth(ctx context.Context, state, code, redirectURI string, connectedBy int64) (*Account, error) {
	s, err := r.consumeOAuthState(ctx, state)
	if err != nil {
		return nil, err
	}
	if s.InitiatedBy != connectedBy {
		return nil, ErrOAuthStateInvalid
	}
	if s.Platform != "x" {
		return nil, ErrNotImplemented
	}
	creds, err := r.GetAppCredentials(ctx, s.Platform)
	if err != nil {
		return nil, err
	}
	tok, err := xExchangeCode(ctx, creds.ClientID, creds.ClientSecret, redirectURI, code, s.CodeVerifier)
	if err != nil {
		return nil, err
	}
	_, username, err := xFetchMe(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	id, err := r.CreateAccount(ctx, s.Platform, "@"+username, username, tok.AccessToken, tok.RefreshToken, expiresAt, connectedBy)
	if err != nil {
		return nil, err
	}
	return r.GetAccount(ctx, id)
}

// ---------- posts ----------

func (r *Repo) CreatePost(ctx context.Context, body string, scheduledAt *time.Time, accountIDs []int64, createdBy int64) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var postID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO social_posts (body, created_by, scheduled_at) VALUES ($1, $2, $3) RETURNING id`,
		body, createdBy, scheduledAt,
	).Scan(&postID); err != nil {
		return 0, err
	}
	for _, accountID := range accountIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO social_post_targets (post_id, account_id) VALUES ($1, $2)`, postID, accountID); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return postID, nil
}

const postSelect = `
	SELECT p.id, p.body, COALESCE(u.name, ''), p.scheduled_at, p.created_at
	FROM social_posts p
	JOIN users u ON u.id = p.created_by`

func scanPost(row pgx.Row) (*Post, error) {
	var p Post
	err := row.Scan(&p.ID, &p.Body, &p.CreatedByName, &p.ScheduledAt, &p.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (r *Repo) ListPosts(ctx context.Context, limit int) ([]Post, error) {
	rows, err := r.pool.Query(ctx, postSelect+` ORDER BY p.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.Body, &p.CreatedByName, &p.ScheduledAt, &p.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		posts = append(posts, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range posts {
		targets, err := r.listTargetsForPost(ctx, posts[i].ID)
		if err != nil {
			return nil, err
		}
		posts[i].Targets = targets
	}
	return posts, nil
}

func (r *Repo) listTargetsForPost(ctx context.Context, postID int64) ([]PostTarget, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.post_id, t.account_id, a.label, a.platform, t.status, t.remote_post_id, t.error_message, t.posted_at
		FROM social_post_targets t
		JOIN social_accounts a ON a.id = t.account_id
		WHERE t.post_id = $1
		ORDER BY t.id`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PostTarget
	for rows.Next() {
		var t PostTarget
		if err := rows.Scan(&t.ID, &t.PostID, &t.AccountID, &t.AccountLabel, &t.Platform, &t.Status, &t.RemotePostID, &t.ErrorMessage, &t.PostedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

type dueTarget struct {
	TargetID  int64
	AccountID int64
	Body      string
}

// ListReadyTargets returns pending targets whose post should be attempted
// now: either scheduled_at is null (send immediately, right after
// CreatePost) or it has passed (picked up by the scheduler ticker).
func (r *Repo) ListReadyTargets(ctx context.Context, postID int64) ([]dueTarget, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.account_id, p.body
		FROM social_post_targets t
		JOIN social_posts p ON p.id = t.post_id
		WHERE t.post_id = $1 AND t.status = 'pending'
		  AND (p.scheduled_at IS NULL OR p.scheduled_at <= now())`, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dueTarget
	for rows.Next() {
		var d dueTarget
		if err := rows.Scan(&d.TargetID, &d.AccountID, &d.Body); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListAllDueTargets is the scheduler ticker's entry point: every pending
// target anywhere whose post's scheduled time has arrived.
func (r *Repo) ListAllDueTargets(ctx context.Context) ([]dueTarget, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT t.id, t.account_id, p.body
		FROM social_post_targets t
		JOIN social_posts p ON p.id = t.post_id
		WHERE t.status = 'pending' AND p.scheduled_at IS NOT NULL AND p.scheduled_at <= now()`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []dueTarget
	for rows.Next() {
		var d dueTarget
		if err := rows.Scan(&d.TargetID, &d.AccountID, &d.Body); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (r *Repo) markTargetPosted(ctx context.Context, targetID int64, remotePostID string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE social_post_targets SET status = 'posted', remote_post_id = $1, posted_at = now()
		WHERE id = $2`, remotePostID, targetID)
	return err
}

func (r *Repo) markTargetFailed(ctx context.Context, targetID int64, errMsg string) error {
	_, err := r.pool.Exec(ctx, `
		UPDATE social_post_targets SET status = 'failed', error_message = $1
		WHERE id = $2`, errMsg, targetID)
	return err
}

// PublishTarget attempts to post targetID's body to its account, handling
// a same-provider token refresh first if the access token is expiring
// soon. Always records the outcome (posted or failed) rather than
// returning an error to the caller, since this is meant to be called in a
// loop over several independent targets — one failing shouldn't stop the
// others or bubble up as a request-level error.
func (r *Repo) PublishTarget(ctx context.Context, d dueTarget) {
	account, err := r.GetAccount(ctx, d.AccountID)
	if err != nil {
		r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("account lookup failed: %v", err))
		return
	}

	switch account.Platform {
	case "x":
		r.publishX(ctx, d, account)
	default:
		r.markTargetFailed(ctx, d.TargetID, "unsupported platform: "+account.Platform)
	}
}

func (r *Repo) publishX(ctx context.Context, d dueTarget, account *Account) {
	accessToken := account.AccessToken
	if xTokenExpiringSoon(account.TokenExpiresAt) {
		creds, err := r.GetAppCredentials(ctx, "x")
		if err != nil {
			r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("refresh: load credentials: %v", err))
			return
		}
		tok, err := xRefreshToken(ctx, creds.ClientID, creds.ClientSecret, account.RefreshToken)
		if err != nil {
			r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("refresh token: %v", err))
			return
		}
		expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		if err := r.updateAccountTokens(ctx, account.ID, tok.AccessToken, tok.RefreshToken, expiresAt); err != nil {
			r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("save refreshed token: %v", err))
			return
		}
		accessToken = tok.AccessToken
	}

	tweetID, err := xPublish(ctx, accessToken, d.Body)
	if err != nil {
		r.markTargetFailed(ctx, d.TargetID, err.Error())
		return
	}
	r.markTargetPosted(ctx, d.TargetID, tweetID)
}
