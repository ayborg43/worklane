package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sociolytik/odoo-clone/internal/ai"
	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/uploads"
	"github.com/sociolytik/odoo-clone/internal/web"
)

const postHistoryLimit = 50

type Handlers struct {
	Repo     *Repo
	AI       *ai.Repo
	Renderer *web.Renderer
	BaseURL  string
	MediaDir string
}

func NewHandlers(repo *Repo, aiRepo *ai.Repo, renderer *web.Renderer, baseURL, mediaDir string) *Handlers {
	return &Handlers{Repo: repo, AI: aiRepo, Renderer: renderer, BaseURL: baseURL, MediaDir: mediaDir}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /social", mw.RequireAuthAndModule("social", http.HandlerFunc(h.Index)))
	mux.Handle("POST /social/posts", mw.RequireAuthAndModule("social", http.HandlerFunc(h.CreatePost)))
	mux.Handle("POST /social/ai/generate", mw.RequireAuthAndModule("social", http.HandlerFunc(h.GeneratePost)))
	mux.Handle("POST /social/ai/adapt", mw.RequireAuthAndModule("social", http.HandlerFunc(h.AdaptCaptions)))

	mux.Handle("GET /social/connections", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Connections))))
	mux.Handle("POST /social/connections/credentials/{platform}", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.UpdateCredentials))))
	mux.Handle("GET /social/connect/{platform}", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Connect))))
	mux.Handle("GET /social/oauth/{platform}/callback", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.OAuthCallback))))
	mux.Handle("POST /social/accounts/{accountID}/disconnect", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Disconnect))))

	// Deliberately not behind auth: Instagram's and TikTok's own servers
	// fetch a post's media directly by URL as part of publishing it (see
	// instagram.go/tiktok.go), with no session cookie of ours to send.
	// Only ever serves what a post's own media_path points at — never a
	// general file browser — and that content is, by construction, about
	// to be published publicly on the target platform anyway.
	mux.HandleFunc("GET /social/media/{postID}", h.ServeMedia)
}

func (h *Handlers) redirectURI(platform string) string {
	return h.BaseURL + "/social/oauth/" + platform + "/callback"
}

// ---------- compose + history ----------

type indexData struct {
	auth.PageData
	Accounts []Account
	Posts    []Post
	Error    string
}

func (h *Handlers) loadIndexData(r *http.Request, errMsg string) (indexData, error) {
	accounts, err := h.Repo.ListAccounts(r.Context())
	if err != nil {
		return indexData{}, err
	}
	posts, err := h.Repo.ListPosts(r.Context(), postHistoryLimit)
	if err != nil {
		return indexData{}, err
	}
	return indexData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Accounts: accounts,
		Posts:    posts,
		Error:    errMsg,
	}, nil
}

func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadIndexData(r, "")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "social/index.html", data, "social/index_body.html")
}

func (h *Handlers) renderIndexBody(w http.ResponseWriter, r *http.Request, errMsg string) {
	data, err := h.loadIndexData(r, errMsg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "social/index_body.html", data)
}

func (h *Handlers) CreatePost(w http.ResponseWriter, r *http.Request) {
	// multipart now that an optional media file can ride along — the
	// compose form always submits this way (hx-encoding="multipart/form-data"),
	// text-only posts (X) just leave the file field empty.
	r.Body = http.MaxBytesReader(w, r.Body, uploads.MaxSize)
	if err := r.ParseMultipartForm(uploads.MaxSize); err != nil {
		h.renderIndexBody(w, r, "Media file too large (max 10MB) or invalid upload.")
		return
	}
	user := auth.UserFromContext(r.Context())

	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		h.renderIndexBody(w, r, "Post text is required.")
		return
	}
	accountIDs := make([]int64, 0, len(r.Form["account_ids"]))
	for _, v := range r.Form["account_ids"] {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid account", http.StatusBadRequest)
			return
		}
		accountIDs = append(accountIDs, id)
	}
	if len(accountIDs) == 0 {
		h.renderIndexBody(w, r, "Pick at least one connected account to post to.")
		return
	}
	// Per-account caption override — populated client-side by the "AI:
	// Tailor caption per platform" step (see index_body.html), one field
	// per selected account named by its id. Left blank, a target just
	// uses the shared body above, same as before this feature existed.
	targets := make([]PostTargetInput, 0, len(accountIDs))
	for _, id := range accountIDs {
		override := strings.TrimSpace(r.FormValue(fmt.Sprintf("caption_override_%d", id)))
		targets = append(targets, PostTargetInput{AccountID: id, CaptionOverride: override})
	}

	var mediaPath, mediaContentType string
	if file, header, err := r.FormFile("media"); err == nil {
		defer file.Close()
		mediaContentType = header.Header.Get("Content-Type")
		if mediaContentType == "" {
			mediaContentType = "application/octet-stream"
		}
		mediaPath, _, err = uploads.Save(h.MediaDir, file, header)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	var scheduledAt *time.Time
	if v := strings.TrimSpace(r.FormValue("scheduled_at")); v != "" {
		t, err := time.Parse("2006-01-02T15:04", v)
		if err != nil {
			h.renderIndexBody(w, r, "Invalid schedule time.")
			return
		}
		scheduledAt = &t
	}

	postID, err := h.Repo.CreatePost(r.Context(), body, mediaPath, mediaContentType, scheduledAt, targets, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Anything not deferred to a future scheduled_at gets published right
	// away, synchronously — a small, bounded number of HTTP calls (one per
	// target account), worth waiting for so the response already reflects
	// success/failure instead of showing "pending" for something that's
	// actually already done.
	ready, err := h.Repo.ListReadyTargets(r.Context(), postID)
	if err == nil {
		for _, t := range ready {
			h.Repo.PublishTarget(r.Context(), t)
		}
	}

	h.renderIndexBody(w, r, "")
}

// ---------- connections (admin) ----------

type connectionsData struct {
	auth.PageData
	Credentials []AppCredentials
	Accounts    []Account
	Message     string
	Error       string
}

func (h *Handlers) loadConnectionsData(r *http.Request, message, errMsg string) (connectionsData, error) {
	creds, err := h.Repo.ListAppCredentials(r.Context())
	if err != nil {
		return connectionsData{}, err
	}
	accounts, err := h.Repo.ListAccounts(r.Context())
	if err != nil {
		return connectionsData{}, err
	}
	return connectionsData{
		PageData:    auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Credentials: creds,
		Accounts:    accounts,
		Message:     message,
		Error:       errMsg,
	}, nil
}

func (h *Handlers) Connections(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadConnectionsData(r, "", "")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "social/connections.html", data)
}

func (h *Handlers) renderConnections(w http.ResponseWriter, r *http.Request, message, errMsg string) {
	data, err := h.loadConnectionsData(r, message, errMsg)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "social/connections.html", data)
}

func (h *Handlers) UpdateCredentials(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	user := auth.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	current, err := h.Repo.GetAppCredentials(r.Context(), platform)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	clientID := strings.TrimSpace(r.FormValue("client_id"))
	// An empty secret field means "leave the saved secret unchanged" — same
	// convention as settings.UpdateMail, so the form never has to echo the
	// real secret back just to let the client id be edited.
	clientSecret := r.FormValue("client_secret")
	if clientSecret == "" {
		clientSecret = current.ClientSecret
	}
	if err := h.Repo.UpdateAppCredentials(r.Context(), platform, clientID, clientSecret, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderConnections(w, r, PlatformLabel(platform)+" credentials saved.", "")
}

func (h *Handlers) Connect(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	user := auth.UserFromContext(r.Context())

	creds, err := h.Repo.GetAppCredentials(r.Context(), platform)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !creds.Configured() {
		h.renderConnections(w, r, "", "Save a client ID and secret for "+PlatformLabel(platform)+" before connecting.")
		return
	}
	redirectURL, err := h.Repo.StartOAuth(r.Context(), platform, creds.ClientID, h.redirectURI(platform), user.ID)
	if err != nil {
		if errors.Is(err, ErrNotImplemented) {
			h.renderConnections(w, r, "", PlatformLabel(platform)+" isn't wired up to post yet.")
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, redirectURL, http.StatusFound)
}

func (h *Handlers) OAuthCallback(w http.ResponseWriter, r *http.Request) {
	platform := r.PathValue("platform")
	user := auth.UserFromContext(r.Context())

	if errParam := r.URL.Query().Get("error"); errParam != "" {
		h.renderConnections(w, r, "", PlatformLabel(platform)+" connection was cancelled or denied.")
		return
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if state == "" || code == "" {
		h.renderConnections(w, r, "", "Missing state or code from "+PlatformLabel(platform)+".")
		return
	}

	account, err := h.Repo.FinishOAuth(r.Context(), state, code, h.redirectURI(platform), user.ID)
	if err != nil {
		if errors.Is(err, ErrOAuthStateInvalid) {
			h.renderConnections(w, r, "", "That connection attempt expired or was invalid — try again.")
			return
		}
		h.renderConnections(w, r, "", "Failed to connect "+PlatformLabel(platform)+": "+err.Error())
		return
	}
	h.renderConnections(w, r, "Connected "+account.Label+" ("+PlatformLabel(account.Platform)+").", "")
}

func (h *Handlers) Disconnect(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("accountID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.DeleteAccount(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderConnections(w, r, "Account disconnected.", "")
}

// PublishDuePosts is the scheduler ticker's entry point (see main.go) —
// attempts every pending target across every post whose scheduled time has
// arrived. Best-effort: an individual target's failure is recorded on that
// target and never stops the rest.
func (h *Handlers) PublishDuePosts(ctx context.Context) {
	due, err := h.Repo.ListAllDueTargets(ctx)
	if err != nil {
		log.Printf("social: list due targets: %v", err)
		return
	}
	for _, t := range due {
		h.Repo.PublishTarget(ctx, t)
	}
}

// ServeMedia streams a post's attached media by post id — see MountRoutes
// for why this route carries no auth check.
func (h *Handlers) ServeMedia(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("postID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	post, err := h.Repo.GetPost(r.Context(), id)
	if err != nil || post.MediaPath == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", post.MediaContentType)
	http.ServeFile(w, r, post.MediaPath)
}

// ---------- AI-assisted composing ----------

type generatePostRequest struct {
	Topic string `json:"topic"`
}

type generatePostResponse struct {
	Text string `json:"text"`
}

// GeneratePost drafts a full post from a short topic/bullet points — a
// separate capability from the generic "Rephrase" button already on this
// textarea (which only edits text that's already there).
func (h *Handlers) GeneratePost(w http.ResponseWriter, r *http.Request) {
	var req generatePostRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	topic := strings.TrimSpace(req.Topic)
	if topic == "" {
		http.Error(w, "topic is required", http.StatusBadRequest)
		return
	}

	text, err := h.AI.Complete(r.Context(), generateSystemPrompt, topic)
	if err != nil {
		writeAIError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(generatePostResponse{Text: text})
}

type adaptCaptionsRequest struct {
	Text      string   `json:"text"`
	Platforms []string `json:"platforms"`
}

// AdaptCaptions tailors one draft into a platform-specific version for
// each requested platform (concurrently — each is an independent AI call).
// Returns whatever succeeded even if some platforms failed, since one
// platform's caption still being useful shouldn't be thrown away over
// another's failure.
func (h *Handlers) AdaptCaptions(w http.ResponseWriter, r *http.Request) {
	var req adaptCaptionsRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}
	if len(req.Platforms) == 0 {
		http.Error(w, "at least one platform is required", http.StatusBadRequest)
		return
	}

	type result struct {
		platform string
		text     string
		err      error
	}
	results := make(chan result, len(req.Platforms))
	for _, p := range req.Platforms {
		p := p
		go func() {
			adapted, err := h.AI.Complete(r.Context(), platformAdaptSystemPrompt(p), text)
			results <- result{platform: p, text: adapted, err: err}
		}()
	}

	out := make(map[string]string, len(req.Platforms))
	var firstErr error
	for range req.Platforms {
		res := <-results
		if res.err != nil {
			if firstErr == nil {
				firstErr = res.err
			}
			continue
		}
		out[res.platform] = res.text
	}
	if len(out) == 0 && firstErr != nil {
		writeAIError(w, firstErr)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

func writeAIError(w http.ResponseWriter, err error) {
	if errors.Is(err, ai.ErrNotConfigured) {
		http.Error(w, "AI isn't configured yet — ask an admin to set it up in Settings.", http.StatusServiceUnavailable)
		return
	}
	http.Error(w, err.Error(), http.StatusBadGateway)
}
