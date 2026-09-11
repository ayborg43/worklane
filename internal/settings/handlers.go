package settings

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/web"
)

type Handlers struct {
	Repo     *Repo
	Renderer *web.Renderer
}

func NewHandlers(repo *Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /settings", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Show))))
	mux.Handle("POST /settings/mail", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.UpdateMail))))
	mux.Handle("POST /settings/mail/test", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.TestMail))))
}

type settingsData struct {
	auth.PageData
	Mail    MailSettings
	Message string
	Error   string
}

func (h *Handlers) Show(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.Repo.Get(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	data := settingsData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Mail:     *cfg,
	}
	h.Renderer.Render(w, http.StatusOK, "settings/index.html", data, "settings/mail_section.html")
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
