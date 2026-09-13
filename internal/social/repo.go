package social

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound          = errors.New("not found")
	ErrOAuthStateInvalid = errors.New("oauth state invalid or expired")
	// ErrNotImplemented covers a platform with no working Provider yet
	// (linkedin, facebook — see AppCredentials.Implemented).
	ErrNotImplemented = errors.New("this platform isn't connected yet")
	// ErrMediaRequired covers publishing to a platform (Instagram, TikTok)
	// that has no text-only post type when the post itself has no media
	// attached.
	ErrMediaRequired = errors.New("this platform requires a photo or video attachment")
)

type Repo struct {
	pool *pgxpool.Pool
	// baseURL must be a real, publicly-reachable hostname for
	// publishInstagram/publishTikTok's media URL to work — Meta's and
	// TikTok's servers fetch it directly, so a local dev "localhost" value
	// only works once this app is actually deployed somewhere public.
	baseURL string
}

func NewRepo(pool *pgxpool.Pool, baseURL string) *Repo {
	return &Repo{pool: pool, baseURL: baseURL}
}

// mediaURL is the publicly-fetchable URL for a post's attached media —
// see handlers.go's ServeMedia for the route this points at.
func (r *Repo) mediaURL(postID int64) string {
	return fmt.Sprintf("%s/social/media/%d", r.baseURL, postID)
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
	case "instagram":
		return instagramAuthorizeRedirectURL(clientID, redirectURI, state), nil
	case "tiktok":
		return tiktokAuthorizeRedirectURL(clientID, redirectURI, state, challenge), nil
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
	creds, err := r.GetAppCredentials(ctx, s.Platform)
	if err != nil {
		return nil, err
	}
	switch s.Platform {
	case "x":
		return r.finishXOAuth(ctx, *creds, redirectURI, code, s.CodeVerifier, connectedBy)
	case "instagram":
		return r.finishInstagramOAuth(ctx, *creds, redirectURI, code, connectedBy)
	case "tiktok":
		return r.finishTikTokOAuth(ctx, *creds, redirectURI, code, s.CodeVerifier, connectedBy)
	default:
		return nil, ErrNotImplemented
	}
}

func (r *Repo) finishXOAuth(ctx context.Context, creds AppCredentials, redirectURI, code, codeVerifier string, connectedBy int64) (*Account, error) {
	tok, err := xExchangeCode(ctx, creds.ClientID, creds.ClientSecret, redirectURI, code, codeVerifier)
	if err != nil {
		return nil, err
	}
	_, username, err := xFetchMe(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	id, err := r.CreateAccount(ctx, "x", "@"+username, username, tok.AccessToken, tok.RefreshToken, expiresAt, connectedBy)
	if err != nil {
		return nil, err
	}
	return r.GetAccount(ctx, id)
}

// finishInstagramOAuth has no refresh_token to store (Meta's long-lived
// tokens are extended by re-exchanging the current token itself — see
// instagramExchangeLongLivedToken), so RefreshToken is left empty on the
// created account; publishInstagram re-exchanges AccessToken directly
// when it's expiring soon.
func (r *Repo) finishInstagramOAuth(ctx context.Context, creds AppCredentials, redirectURI, code string, connectedBy int64) (*Account, error) {
	tok, err := instagramExchangeCode(ctx, creds.ClientID, creds.ClientSecret, redirectURI, code)
	if err != nil {
		return nil, err
	}
	identity, err := instagramFindBusinessAccount(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	id, err := r.CreateAccount(ctx, "instagram", "@"+identity.Username, identity.InstagramUserID, identity.PageAccessToken, "", expiresAt, connectedBy)
	if err != nil {
		return nil, err
	}
	return r.GetAccount(ctx, id)
}

func (r *Repo) finishTikTokOAuth(ctx context.Context, creds AppCredentials, redirectURI, code, codeVerifier string, connectedBy int64) (*Account, error) {
	tok, err := tiktokExchangeCode(ctx, creds.ClientID, creds.ClientSecret, redirectURI, code, codeVerifier)
	if err != nil {
		return nil, err
	}
	displayName, err := tiktokFetchMe(ctx, tok.AccessToken)
	if err != nil {
		return nil, err
	}
	expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	id, err := r.CreateAccount(ctx, "tiktok", displayName, displayName, tok.AccessToken, tok.RefreshToken, expiresAt, connectedBy)
	if err != nil {
		return nil, err
	}
	return r.GetAccount(ctx, id)
}

// ---------- posts ----------

func (r *Repo) CreatePost(ctx context.Context, body, mediaPath, mediaContentType string, scheduledAt *time.Time, targets []PostTargetInput, createdBy int64) (int64, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var postID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO social_posts (body, media_path, media_content_type, created_by, scheduled_at)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		body, mediaPath, mediaContentType, createdBy, scheduledAt,
	).Scan(&postID); err != nil {
		return 0, err
	}
	for _, t := range targets {
		if _, err := tx.Exec(ctx,
			`INSERT INTO social_post_targets (post_id, account_id, caption_override) VALUES ($1, $2, $3)`,
			postID, t.AccountID, t.CaptionOverride); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return postID, nil
}

const postSelect = `
	SELECT p.id, p.body, p.media_path, p.media_content_type, COALESCE(u.name, ''), p.scheduled_at, p.created_at
	FROM social_posts p
	JOIN users u ON u.id = p.created_by`

func scanPost(row pgx.Row) (*Post, error) {
	var p Post
	err := row.Scan(&p.ID, &p.Body, &p.MediaPath, &p.MediaContentType, &p.CreatedByName, &p.ScheduledAt, &p.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &p, nil
}

// GetPost is used by the public media-serving route (see handlers.go) —
// it needs the post's media path/content type to stream the file back to
// whichever platform's servers are fetching it.
func (r *Repo) GetPost(ctx context.Context, id int64) (*Post, error) {
	return scanPost(r.pool.QueryRow(ctx, postSelect+` WHERE p.id = $1`, id))
}

func (r *Repo) ListPosts(ctx context.Context, limit int) ([]Post, error) {
	rows, err := r.pool.Query(ctx, postSelect+` ORDER BY p.created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	var posts []Post
	for rows.Next() {
		var p Post
		if err := rows.Scan(&p.ID, &p.Body, &p.MediaPath, &p.MediaContentType, &p.CreatedByName, &p.ScheduledAt, &p.CreatedAt); err != nil {
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
	TargetID         int64
	AccountID        int64
	PostID           int64
	Body             string
	MediaPath        string
	MediaContentType string
}

const dueTargetSelect = `
	SELECT t.id, t.account_id, p.id,
	       CASE WHEN t.caption_override <> '' THEN t.caption_override ELSE p.body END,
	       p.media_path, p.media_content_type
	FROM social_post_targets t
	JOIN social_posts p ON p.id = t.post_id`

func scanDueTargets(rows pgx.Rows) ([]dueTarget, error) {
	defer rows.Close()
	var out []dueTarget
	for rows.Next() {
		var d dueTarget
		if err := rows.Scan(&d.TargetID, &d.AccountID, &d.PostID, &d.Body, &d.MediaPath, &d.MediaContentType); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ListReadyTargets returns pending targets whose post should be attempted
// now: either scheduled_at is null (send immediately, right after
// CreatePost) or it has passed (picked up by the scheduler ticker).
func (r *Repo) ListReadyTargets(ctx context.Context, postID int64) ([]dueTarget, error) {
	rows, err := r.pool.Query(ctx, dueTargetSelect+`
		WHERE t.post_id = $1 AND t.status = 'pending'
		  AND (p.scheduled_at IS NULL OR p.scheduled_at <= now())`, postID)
	if err != nil {
		return nil, err
	}
	return scanDueTargets(rows)
}

// ListAllDueTargets is the scheduler ticker's entry point: every pending
// target anywhere whose post's scheduled time has arrived.
func (r *Repo) ListAllDueTargets(ctx context.Context) ([]dueTarget, error) {
	rows, err := r.pool.Query(ctx, dueTargetSelect+`
		WHERE t.status = 'pending' AND p.scheduled_at IS NOT NULL AND p.scheduled_at <= now()`)
	if err != nil {
		return nil, err
	}
	return scanDueTargets(rows)
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
	case "instagram":
		r.publishInstagram(ctx, d, account)
	case "tiktok":
		r.publishTikTok(ctx, d, account)
	default:
		r.markTargetFailed(ctx, d.TargetID, "unsupported platform: "+account.Platform)
	}
}

func (r *Repo) publishX(ctx context.Context, d dueTarget, account *Account) {
	accessToken := account.AccessToken
	if tokenExpiringSoon(account.TokenExpiresAt) {
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

func (r *Repo) publishInstagram(ctx context.Context, d dueTarget, account *Account) {
	if d.MediaPath == "" {
		r.markTargetFailed(ctx, d.TargetID, ErrMediaRequired.Error())
		return
	}

	accessToken := account.AccessToken
	if tokenExpiringSoon(account.TokenExpiresAt) {
		creds, err := r.GetAppCredentials(ctx, "instagram")
		if err != nil {
			r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("refresh: load credentials: %v", err))
			return
		}
		tok, err := instagramExchangeLongLivedToken(ctx, creds.ClientID, creds.ClientSecret, account.AccessToken)
		if err != nil {
			r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("refresh token: %v", err))
			return
		}
		expiresAt := time.Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
		if err := r.updateAccountTokens(ctx, account.ID, tok.AccessToken, "", expiresAt); err != nil {
			r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("save refreshed token: %v", err))
			return
		}
		accessToken = tok.AccessToken
	}

	isVideo := strings.HasPrefix(d.MediaContentType, "video/")
	mediaID, err := instagramPublish(ctx, accessToken, account.ExternalAccountID, r.mediaURL(d.PostID), d.Body, isVideo)
	if err != nil {
		r.markTargetFailed(ctx, d.TargetID, err.Error())
		return
	}
	r.markTargetPosted(ctx, d.TargetID, mediaID)
}

func (r *Repo) publishTikTok(ctx context.Context, d dueTarget, account *Account) {
	if d.MediaPath == "" {
		r.markTargetFailed(ctx, d.TargetID, ErrMediaRequired.Error())
		return
	}
	if !strings.HasPrefix(d.MediaContentType, "video/") {
		r.markTargetFailed(ctx, d.TargetID, "TikTok requires a video attachment (a photo was attached instead)")
		return
	}

	accessToken := account.AccessToken
	if tokenExpiringSoon(account.TokenExpiresAt) {
		creds, err := r.GetAppCredentials(ctx, "tiktok")
		if err != nil {
			r.markTargetFailed(ctx, d.TargetID, fmt.Sprintf("refresh: load credentials: %v", err))
			return
		}
		tok, err := tiktokRefreshToken(ctx, creds.ClientID, creds.ClientSecret, account.RefreshToken)
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

	publishID, err := tiktokPublish(ctx, accessToken, r.mediaURL(d.PostID), d.Body)
	if err != nil {
		r.markTargetFailed(ctx, d.TargetID, err.Error())
		return
	}
	r.markTargetPosted(ctx, d.TargetID, publishID)
}
