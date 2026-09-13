package social

// TikTok's Content Posting API: its own OAuth 2.0 (with PKCE, like X) and
// its own publish flow, which is asynchronous — initiating a post returns
// a publish_id immediately, and whether it actually finished posting is
// only knowable later via a separate status-check call this integration
// doesn't poll (same one-shot-call level of effort as every other
// provider here). A successful init is recorded as posted, using the
// publish_id as the remote post id.
//
// Posts default to TikTok's most restrictive privacy_level (SELF_ONLY —
// private, visible only to the poster) rather than public. TikTok's API
// rejects PUBLIC_TO_EVERYONE outright for any app that hasn't passed
// their content-posting audit, and SELF_ONLY is the one level guaranteed
// to work regardless of audit state. Once a real app is audited for
// public posting, this is the one line worth revisiting.
//
// TikTok's Content Posting API is video-first — there's no text/photo
// post here, so a post whose attached media isn't a video is rejected
// before ever calling TikTok (see repo.go's publishTikTok).
//
// Untested against a live app (none exists yet at the time this was
// written).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

const (
	tiktokAuthorizeURL   = "https://www.tiktok.com/v2/auth/authorize/"
	tiktokTokenURL       = "https://open.tiktokapis.com/v2/oauth/token/"
	tiktokUserInfoURL    = "https://open.tiktokapis.com/v2/user/info/"
	tiktokPublishInitURL = "https://open.tiktokapis.com/v2/post/publish/video/init/"
	// TikTok's own terminology for what every other provider here calls a
	// client id.
	tiktokScopes = "user.info.basic,video.publish"
)

func tiktokAuthorizeRedirectURL(clientKey, redirectURI, state, codeChallenge string) string {
	q := url.Values{
		"client_key":            {clientKey},
		"redirect_uri":          {redirectURI},
		"scope":                 {tiktokScopes},
		"response_type":         {"code"},
		"state":                 {state},
		"code_challenge":        {codeChallenge},
		"code_challenge_method": {"S256"},
	}
	return tiktokAuthorizeURL + "?" + q.Encode()
}

type tiktokTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	// TikTok's token endpoint returns HTTP 200 even on failure (confirmed
	// against the real endpoint with a deliberately invalid code — it came
	// back 200 with only these two fields populated), unlike every other
	// endpoint this integration talks to. The HTTP status alone is not
	// enough to detect failure here; ErrorCode must be checked too.
	ErrorCode        string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

func tiktokPostForm(ctx context.Context, form url.Values) (*tiktokTokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tiktokTokenURL, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tiktok token endpoint returned %d: %s", resp.StatusCode, body)
	}
	var tok tiktokTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode tiktok token response: %w", err)
	}
	if tok.ErrorCode != "" {
		return nil, fmt.Errorf("tiktok token endpoint error: %s: %s", tok.ErrorCode, tok.ErrorDescription)
	}
	return &tok, nil
}

func tiktokExchangeCode(ctx context.Context, clientKey, clientSecret, redirectURI, code, codeVerifier string) (*tiktokTokenResponse, error) {
	return tiktokPostForm(ctx, url.Values{
		"client_key":    {clientKey},
		"client_secret": {clientSecret},
		"code":          {code},
		"grant_type":    {"authorization_code"},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	})
}

func tiktokRefreshToken(ctx context.Context, clientKey, clientSecret, refreshToken string) (*tiktokTokenResponse, error) {
	return tiktokPostForm(ctx, url.Values{
		"client_key":    {clientKey},
		"client_secret": {clientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
	})
}

type tiktokAPIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e tiktokAPIError) ok() bool { return e.Code == "" || e.Code == "ok" }

// tiktokFetchMe identifies the account that just connected, so the UI can
// show a real display name instead of a bare open id.
func tiktokFetchMe(ctx context.Context, accessToken string) (displayName string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tiktokUserInfoURL+"?fields=display_name", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tiktok user info returned %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Data struct {
			User struct {
				DisplayName string `json:"display_name"`
			} `json:"user"`
		} `json:"data"`
		Error tiktokAPIError `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode tiktok user info response: %w", err)
	}
	if !out.Error.ok() {
		return "", fmt.Errorf("tiktok user info error: %s", out.Error.Message)
	}
	return out.Data.User.DisplayName, nil
}

// tiktokPublish initiates a direct post from a publicly-fetchable video
// URL (PULL_FROM_URL) — same public-media-URL requirement as Instagram.
func tiktokPublish(ctx context.Context, accessToken, videoURL, caption string) (publishID string, err error) {
	payload, err := json.Marshal(map[string]any{
		"post_info": map[string]any{
			"title":         caption,
			"privacy_level": "SELF_ONLY",
		},
		"source_info": map[string]any{
			"source":    "PULL_FROM_URL",
			"video_url": videoURL,
		},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tiktokPublishInitURL, bytes.NewReader(payload))
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
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("tiktok publish init returned %d: %s", resp.StatusCode, body)
	}
	var out struct {
		Data struct {
			PublishID string `json:"publish_id"`
		} `json:"data"`
		Error tiktokAPIError `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode tiktok publish response: %w", err)
	}
	if !out.Error.ok() {
		return "", fmt.Errorf("tiktok publish error: %s", out.Error.Message)
	}
	return out.Data.PublishID, nil
}
