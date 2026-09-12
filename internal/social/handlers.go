package social

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/web"
)

const postHistoryLimit = 50

type Handlers struct {
	Repo     *Repo
	Renderer *web.Renderer
	BaseURL  string
}

func NewHandlers(repo *Repo, renderer *web.Renderer, baseURL string) *Handlers {
	return &Handlers{Repo: repo, Renderer: renderer, BaseURL: baseURL}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /social", mw.RequireAuthAndModule("social", http.HandlerFunc(h.Index)))
	mux.Handle("POST /social/posts", mw.RequireAuthAndModule("social", http.HandlerFunc(h.CreatePost)))

	mux.Handle("GET /social/connections", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Connections))))
	mux.Handle("POST /social/connections/credentials/{platform}", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.UpdateCredentials))))
	mux.Handle("GET /social/connect/{platform}", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Connect))))
	mux.Handle("GET /social/oauth/{platform}/callback", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.OAuthCallback))))
	mux.Handle("POST /social/accounts/{accountID}/disconnect", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Disconnect))))
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
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

	var scheduledAt *time.Time
	if v := strings.TrimSpace(r.FormValue("scheduled_at")); v != "" {
		t, err := time.Parse("2006-01-02T15:04", v)
		if err != nil {
			h.renderIndexBody(w, r, "Invalid schedule time.")
			return
		}
		scheduledAt = &t
	}

	postID, err := h.Repo.CreatePost(r.Context(), body, scheduledAt, accountIDs, user.ID)
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
