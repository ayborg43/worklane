package auth

import (
	"context"
	"net/http"
)

type ctxKey int

const userCtxKey ctxKey = iota

// UserFromContext returns the logged-in user attached by Middleware.LoadUser,
// or nil if the request is unauthenticated.
func UserFromContext(ctx context.Context) *User {
	u, _ := ctx.Value(userCtxKey).(*User)
	return u
}

type Middleware struct {
	Repo *Repo
}

func NewMiddleware(repo *Repo) *Middleware {
	return &Middleware{Repo: repo}
}

// LoadUser attaches the current user to the request context if a valid
// session cookie is present, but never blocks the request. Wire this
// globally so any page (e.g. the nav bar) can render logged-in state.
func (m *Middleware) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		user, err := m.Repo.GetUserBySessionToken(r.Context(), cookie.Value)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), userCtxKey, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireAuth blocks the request unless LoadUser already attached a user.
// For htmx-triggered requests it responds with HX-Redirect so the client
// swaps the whole page instead of just the targeted fragment.
func (m *Middleware) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if UserFromContext(r.Context()) == nil {
			if r.Header.Get("HX-Request") == "true" {
				w.Header().Set("HX-Redirect", "/login")
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin blocks the request unless the logged-in user is an admin.
// Mount after RequireAuth. 404s rather than 403s, hiding the route's
// existence from non-admins the same way requireMember hides projects from
// non-members — non-admins never see a Settings link in the first place.
func (m *Middleware) RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil || !user.IsAdmin {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}
