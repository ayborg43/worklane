package auth

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sociolytik/odoo-clone/internal/web"
)

type Handlers struct {
	Repo         *Repo
	Renderer     *web.Renderer
	CookieSecure bool
	SessionTTL   time.Duration
}

func NewHandlers(repo *Repo, renderer *web.Renderer, cookieSecure bool, sessionTTL time.Duration) *Handlers {
	return &Handlers{Repo: repo, Renderer: renderer, CookieSecure: cookieSecure, SessionTTL: sessionTTL}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *Middleware) {
	mux.HandleFunc("GET /login", h.LoginForm)
	mux.HandleFunc("POST /login", h.Login)
	mux.HandleFunc("GET /register", h.RegisterForm)
	mux.HandleFunc("POST /register", h.Register)
	mux.Handle("POST /logout", mw.RequireAuth(http.HandlerFunc(h.Logout)))
}

// PageData is embedded into every page's template data across all modules so
// layout.html's nav partial can render logged-in state via the promoted
// CurrentUser field, regardless of which handler rendered the page.
type PageData struct {
	CurrentUser *User
}

type loginData struct {
	PageData
	Error string
	Email string
}

func (h *Handlers) LoginForm(w http.ResponseWriter, r *http.Request) {
	if UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "auth/login.html", loginData{})
}

func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	password := r.FormValue("password")

	user, err := h.Repo.GetUserByEmail(r.Context(), email)
	if err != nil || !CheckPassword(user.PasswordHash, password) {
		h.Renderer.Render(w, http.StatusUnauthorized, "auth/login.html", loginData{
			Error: "Invalid email or password.",
			Email: email,
		})
		return
	}

	h.startSession(w, r, user.ID)
	http.Redirect(w, r, "/projects", http.StatusFound)
}

type registerData struct {
	PageData
	Error string
	Name  string
	Email string
}

func (h *Handlers) RegisterForm(w http.ResponseWriter, r *http.Request) {
	if UserFromContext(r.Context()) != nil {
		http.Redirect(w, r, "/projects", http.StatusFound)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "auth/register.html", registerData{})
}

func (h *Handlers) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(strings.ToLower(r.FormValue("email")))
	password := r.FormValue("password")

	if name == "" || email == "" || len(password) < 8 {
		h.Renderer.Render(w, http.StatusBadRequest, "auth/register.html", registerData{
			Error: "Name, email, and a password of at least 8 characters are required.",
			Name:  name, Email: email,
		})
		return
	}

	hash, err := HashPassword(password)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	user, err := h.Repo.CreateUser(r.Context(), email, name, hash)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			h.Renderer.Render(w, http.StatusConflict, "auth/register.html", registerData{
				Error: "An account with that email already exists.",
				Name:  name, Email: email,
			})
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.startSession(w, r, user.ID)
	http.Redirect(w, r, "/projects", http.StatusFound)
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(CookieName); err == nil {
		_ = h.Repo.DeleteSessionByToken(r.Context(), cookie.Value)
	}
	clearSessionCookie(w, h.CookieSecure)
	http.Redirect(w, r, "/login", http.StatusFound)
}

func (h *Handlers) startSession(w http.ResponseWriter, r *http.Request, userID int64) {
	token, expiresAt, err := h.Repo.CreateSession(r.Context(), userID, h.SessionTTL, r.UserAgent(), r.RemoteAddr)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	setSessionCookie(w, token, expiresAt, h.CookieSecure)
}
