package social

// Instagram publishing goes through Meta's Graph API via Facebook Login —
// there is no separate "Instagram API" OAuth of its own. A connected
// account here is really a Facebook Page with a linked Instagram
// professional (Business/Creator) account; the first Page the admin's
// login manages that has one linked is what gets connected (no page-picker
// step in this first pass, same shape as every other provider here having
// exactly one OAuth-callback-does-everything step, no mid-flow UI).
//
// Every real call needs a Meta developer app with the Instagram Graph API
// product added, and — for anything beyond the app's own test users —
// Meta's app review approved for instagram_basic and
// instagram_content_publish. Untested against a live app (none exists yet
// at the time this was written).

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	metaGraphVersion = "v19.0"
	metaAuthorizeURL = "https://www.facebook.com/" + metaGraphVersion + "/dialog/oauth"
	metaGraphBaseURL = "https://graph.facebook.com/" + metaGraphVersion
	// business_management + pages_* are needed to list the admin's Pages
	// and resolve which one has a linked Instagram account.
	instagramScopes = "instagram_basic,instagram_content_publish,pages_show_list,pages_read_engagement,business_management"
)

// Meta's OAuth doesn't use PKCE for a confidential (server-side,
// client-secret) app, unlike X and TikTok — so no code_challenge here.
func instagramAuthorizeRedirectURL(clientID, redirectURI, state string) string {
	q := url.Values{
		"client_id":     {clientID},
		"redirect_uri":  {redirectURI},
		"scope":         {instagramScopes},
		"state":         {state},
		"response_type": {"code"},
	}
	return metaAuthorizeURL + "?" + q.Encode()
}

type metaTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

func metaGet(ctx context.Context, endpoint string, query url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+query.Encode(), nil)
	if err != nil {
		return nil, err
	}
	return doMetaRequest(req)
}

func metaPost(ctx context.Context, endpoint string, form url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return doMetaRequest(req)
}

func doMetaRequest(req *http.Request) ([]byte, error) {
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("meta graph api returned %d: %s", resp.StatusCode, body)
	}
	return body, nil
}

// instagramExchangeCode swaps the authorization code for a short-lived
// user access token, then immediately exchanges that for a long-lived one
// (~60 days) — Instagram publishing tokens are meant to be the long-lived
// kind, periodically re-exchanged rather than re-authorized every couple
// of hours like X's.
func instagramExchangeCode(ctx context.Context, clientID, clientSecret, redirectURI, code string) (*metaTokenResponse, error) {
	body, err := metaGet(ctx, metaGraphBaseURL+"/oauth/access_token", url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"code":          {code},
	})
	if err != nil {
		return nil, err
	}
	var tok metaTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode meta token response: %w", err)
	}
	return instagramExchangeLongLivedToken(ctx, clientID, clientSecret, tok.AccessToken)
}

// instagramExchangeLongLivedToken both performs the initial short->long
// exchange right after connecting and doubles as the "refresh" call
// later — Meta's long-lived tokens are extended by re-running the same
// fb_exchange_token grant on the still-valid current token, not a
// separate refresh_token the way X and TikTok work.
func instagramExchangeLongLivedToken(ctx context.Context, clientID, clientSecret, currentToken string) (*metaTokenResponse, error) {
	body, err := metaGet(ctx, metaGraphBaseURL+"/oauth/access_token", url.Values{
		"grant_type":        {"fb_exchange_token"},
		"client_id":         {clientID},
		"client_secret":     {clientSecret},
		"fb_exchange_token": {currentToken},
	})
	if err != nil {
		return nil, err
	}
	var tok metaTokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("decode meta long-lived token response: %w", err)
	}
	return &tok, nil
}

type instagramIdentity struct {
	PageAccessToken string
	InstagramUserID string
	Username        string
}

// instagramFindBusinessAccount walks the Pages the connecting user
// manages and returns the first one with a linked Instagram professional
// account. Publishing is authenticated with the *Page's* access token, not
// the user token that discovered it — a Graph API quirk specific to
// Instagram publishing through a Page.
func instagramFindBusinessAccount(ctx context.Context, userAccessToken string) (*instagramIdentity, error) {
	body, err := metaGet(ctx, metaGraphBaseURL+"/me/accounts", url.Values{
		"fields":       {"id,access_token,instagram_business_account"},
		"access_token": {userAccessToken},
	})
	if err != nil {
		return nil, err
	}
	var pages struct {
		Data []struct {
			AccessToken              string `json:"access_token"`
			InstagramBusinessAccount *struct {
				ID string `json:"id"`
			} `json:"instagram_business_account"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &pages); err != nil {
		return nil, fmt.Errorf("decode meta pages response: %w", err)
	}
	for _, p := range pages.Data {
		if p.InstagramBusinessAccount == nil {
			continue
		}
		username, err := instagramFetchUsername(ctx, p.InstagramBusinessAccount.ID, p.AccessToken)
		if err != nil {
			return nil, err
		}
		return &instagramIdentity{
			PageAccessToken: p.AccessToken,
			InstagramUserID: p.InstagramBusinessAccount.ID,
			Username:        username,
		}, nil
	}
	return nil, fmt.Errorf("no Facebook Page with a linked Instagram professional account was found for this login")
}

func instagramFetchUsername(ctx context.Context, igUserID, pageAccessToken string) (string, error) {
	body, err := metaGet(ctx, metaGraphBaseURL+"/"+igUserID, url.Values{
		"fields":       {"username"},
		"access_token": {pageAccessToken},
	})
	if err != nil {
		return "", err
	}
	var out struct {
		Username string `json:"username"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("decode instagram username response: %w", err)
	}
	return out.Username, nil
}

// instagramPublish is the documented two-step Graph API flow: create a
// media container from a publicly-fetchable media URL, then publish that
// container. mediaURL must be reachable by Meta's own servers, which is
// what BaseURL (a real public hostname in production) is for.
func instagramPublish(ctx context.Context, pageAccessToken, igUserID, mediaURL, caption string, isVideo bool) (mediaID string, err error) {
	form := url.Values{
		"caption":      {caption},
		"access_token": {pageAccessToken},
	}
	if isVideo {
		form.Set("media_type", "REELS")
		form.Set("video_url", mediaURL)
	} else {
		form.Set("image_url", mediaURL)
	}
	body, err := metaPost(ctx, metaGraphBaseURL+"/"+igUserID+"/media", form)
	if err != nil {
		return "", err
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		return "", fmt.Errorf("decode instagram media-create response: %w", err)
	}

	publishBody, err := metaPost(ctx, metaGraphBaseURL+"/"+igUserID+"/media_publish", url.Values{
		"creation_id":  {created.ID},
		"access_token": {pageAccessToken},
	})
	if err != nil {
		return "", err
	}
	var published struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(publishBody, &published); err != nil {
		return "", fmt.Errorf("decode instagram media-publish response: %w", err)
	}
	return published.ID, nil
}
