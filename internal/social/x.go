package social

// X (Twitter) is the one platform with a real, working integration in this
// first pass — see models.go's AppCredentials.Implemented. It's plain
// net/http against X's documented OAuth 2.0 + API v2 endpoints, no new
// dependency. Untested against a live X developer app (none exists yet at
// the time this was written) — the first real connection attempt may
// surface a small discrepancy worth debugging together, same as any
// first-time third-party integration.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

const (
	xAuthorizeURL = "https://x.com/i/oauth2/authorize"
	xTokenURL     = "https://api.x.com/2/oauth2/token"
	xTweetsURL    = "https://api.x.com/2/tweets"
	xMeURL        = "https://api.x.com/2/users/me"
	// offline.access is required to receive a refresh_token at all — X
	// access tokens are short-lived (about two hours).
	xScopes = "tweet.read tweet.write users.read offline.access"
)

// newPKCEPair returns a random code_verifier and its S256 code_challenge,
// per RFC 7636 — X requires PKCE even for confidential (client-secret)
// apps.
func newPKCEPair() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", err
	}
	verifier = base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func xAuthorizeRedirectURL(clientID, redirectURI, state, codeChallenge string) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {xScopes},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return xAuthorizeURL + "?" + q.Encode()
}

type xTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func xPostForm(ctx context.Context, clientID, clientSecret string, form url.Values) (*xTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xTokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, clientSecret)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("x token endpoint returned %d: %s", resp.StatusCode, body)
	}
	var tok xTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode x token response: %w", err)
	}
	return &tok, nil
}

func xExchangeCode(ctx context.Context, clientID, clientSecret, redirectURI, code, codeVerifier string) (*xTokenResponse, error) {
	return xPostForm(ctx, clientID, clientSecret, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"client_id":     {clientID},
		"code_verifier": {codeVerifier},
	})
}

func xRefreshToken(ctx context.Context, clientID, clientSecret, refreshToken string) (*xTokenResponse, error) {
	return xPostForm(ctx, clientID, clientSecret, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
	})
}

// xFetchMe identifies the account that just connected, so the UI can show
// a real handle instead of a bare account id.
func xFetchMe(ctx context.Context, accessToken string) (id, username string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, xMeURL, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("x users/me returned %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Data struct {
			ID       string `json:"id"`
			Username string `json:"username"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", "", fmt.Errorf("decode x users/me response: %w", err)
	}
	return out.Data.ID, out.Data.Username, nil
}

// xPublish posts a tweet and returns its id.
func xPublish(ctx context.Context, accessToken, text string) (tweetID string, err error) {
	payload, err := json.Marshal(map[string]string{"text": text})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, xTweetsURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("x tweet publish returned %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode x publish response: %w", err)
	}
	return out.Data.ID, nil
}

// tokenExpiringSoon guards a refresh call with a minute of slack so a
// token that's valid-but-about-to-expire doesn't fail mid-publish. Shared
// across all three providers (X, Instagram, TikTok) — it's a pure
// time.Time comparison with nothing platform-specific about it.
func tokenExpiringSoon(expiresAt *time.Time) bool {
	return expiresAt == nil || time.Now().Add(1*time.Minute).After(*expiresAt)
}
