package settings

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/web"
)

type Handlers struct {
	Repo     *Repo
	Users    *auth.Repo
	Renderer *web.Renderer
}

func NewHandlers(repo *Repo, users *auth.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Users: users, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /settings", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Show))))
	mux.Handle("POST /settings/mail", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.UpdateMail))))
	mux.Handle("POST /settings/mail/test", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.TestMail))))
	mux.Handle("POST /settings/access/{userID}/{module}/toggle", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.ToggleModuleAccess))))
	mux.Handle("POST /settings/users", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.CreateUser))))
	mux.Handle("POST /settings/access/{userID}/password", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.SetPassword))))
}

type settingsData struct {
	auth.PageData
	Mail    MailSettings
	Message string
	Error   string

	// Populated only for the full-page Show render and the User Access
	// fragment re-render — the mail-section fragment renders never touch
	// these, so renderSection leaves them at zero value.
	Users         []auth.User
	Modules       []string
	ModuleLabels  map[string]string
	AccessMessage string
	AccessError   string
}

func (h *Handlers) Show(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.Repo.Get(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.loadUsersWithAccess(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := settingsData{
		PageData:     auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Mail:         *cfg,
		Users:        users,
		Modules:      auth.AllModules,
		ModuleLabels: auth.ModuleLabels,
	}
	h.Renderer.Render(w, http.StatusOK, "settings/index.html", data, "settings/mail_section.html", "settings/users_section.html")
}

func (h *Handlers) UpdateMail(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	current, err := h.Repo.Get(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	port, err := strconv.Atoi(strings.TrimSpace(r.FormValue("smtp_port")))
	if err != nil || port <= 0 || port > 65535 {
		h.renderSection(w, r, *current, "", "SMTP port must be a valid port number.")
		return
	}

	// An empty password field means "leave the saved password unchanged" —
	// the form never echoes the real password back, so there is no other
	// way to save the other fields without re-entering it every time.
	password := r.FormValue("smtp_password")
	if password == "" {
		password = current.Password
	}

	m := MailSettings{
		SMTPHost:    strings.TrimSpace(r.FormValue("smtp_host")),
		SMTPPort:    port,
		Username:    strings.TrimSpace(r.FormValue("smtp_username")),
		Password:    password,
		FromAddress: strings.TrimSpace(r.FormValue("from_address")),
		FromName:    strings.TrimSpace(r.FormValue("from_name")),
		UseTLS:      r.FormValue("use_tls") == "on",
	}
	if err := h.Repo.Update(r.Context(), m, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	updated, err := h.Repo.Get(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderSection(w, r, *updated, "Mail settings saved.", "")
}

func (h *Handlers) TestMail(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	cfg, err := h.Repo.Get(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if err := h.Repo.Send(r.Context(), user.Email, "Test email from Worklane", "This is a test email confirming your SMTP settings are working."); err != nil {
		h.renderSection(w, r, *cfg, "", "Failed to send test email: "+err.Error())
		return
	}
	h.renderSection(w, r, *cfg, "Test email sent to "+user.Email+".", "")
}

func (h *Handlers) renderSection(w http.ResponseWriter, r *http.Request, cfg MailSettings, message, errMsg string) {
	data := settingsData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Mail:     cfg,
		Message:  message,
		Error:    errMsg,
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "settings/mail_section.html", data)
}

// loadUsersWithAccess fetches every user and attaches each one's
// RestrictedModules (via a single ListAllRestrictions query, not an N+1
// per-user lookup) so the User Access template can just call the same
// User.CanAccess method every route gate and nav link already use.
func (h *Handlers) loadUsersWithAccess(r *http.Request) ([]auth.User, error) {
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		return nil, err
	}
	restrictions, err := h.Users.ListAllRestrictions(r.Context())
	if err != nil {
		return nil, err
	}
	for i := range users {
		users[i].RestrictedModules = restrictions[users[i].ID]
	}
	return users, nil
}

// ToggleModuleAccess flips one user's access to one module. wasBlocked
// doubles as the new "allowed" value: if the module was blocked, the new
// state is allowed (true); if it was allowed, the new state is blocked
// (false) — see auth.Repo.SetModuleAccess.
func (h *Handlers) ToggleModuleAccess(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	module := r.PathValue("module")
	if !auth.IsModule(module) {
		http.NotFound(w, r)
		return
	}
	restrictions, err := h.Users.ListAllRestrictions(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	wasBlocked := restrictions[userID][module]
	if err := h.Users.SetModuleAccess(r.Context(), userID, module, wasBlocked); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderUserAccessSection(w, r, "", "")
}

// CreateUser lets an admin add a user directly (name, email, password)
// without that person self-registering — same validation rule as
// auth.Handlers.Register (password length, duplicate-email conflict), just
// reachable from Settings instead of a logged-out /register form. The new
// account is a regular (non-admin) user; admin status is granted
// separately (ADMIN_EMAIL, or a direct SQL promotion) and deliberately
// isn't a checkbox here.
func (h *Handlers) CreateUser(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	password := r.FormValue("password")

	if name == "" || email == "" || len(password) < 8 {
		h.renderUserAccessSection(w, r, "", "Name, email, and a password of at least 8 characters are required.")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, err := h.Users.CreateUser(r.Context(), email, name, hash); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			h.renderUserAccessSection(w, r, "", "An account with that email already exists.")
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderUserAccessSection(w, r, "User created.", "")
}

// SetPassword lets an admin overwrite a user's password directly (a
// forgotten-password reset, or standing in for someone who hasn't set one
// up yet). Every existing session for that user is invalidated in the same
// action — otherwise a session cookie issued under the old password would
// keep working indefinitely, which defeats the point of a reset done
// because the old credential is no longer trusted.
func (h *Handlers) SetPassword(w http.ResponseWriter, r *http.Request) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	password := r.FormValue("password")
	if len(password) < 8 {
		h.renderUserAccessSection(w, r, "", "Password must be at least 8 characters.")
		return
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Users.SetPassword(r.Context(), userID, hash); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Users.DeleteSessionsByUserID(r.Context(), userID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderUserAccessSection(w, r, "Password updated.", "")
}

// renderUserAccessSection re-renders the #user-access-body fragment after
// any mutation (module toggle, user create, password reset) — the shared
// tail every one of those three handlers ends with.
func (h *Handlers) renderUserAccessSection(w http.ResponseWriter, r *http.Request, message, errMsg string) {
	users, err := h.loadUsersWithAccess(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := settingsData{
		PageData:      auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Users:         users,
		Modules:       auth.AllModules,
		ModuleLabels:  auth.ModuleLabels,
		AccessMessage: message,
		AccessError:   errMsg,
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "settings/users_section.html", data)
}
