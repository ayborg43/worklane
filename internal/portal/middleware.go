package portal

import (
	"context"
	"net/http"
)

// CookieName is deliberately distinct from auth.CookieName — a contact and
// an internal user are different identities with different session
// tables, and using a different cookie name means the two never collide or
// get confused, even if both are somehow set in the same browser.
const CookieName = "portal_session"

type ctxKey int

const contactCtxKey ctxKey = iota

func ContactFromContext(ctx context.Context) *Contact {
	c, _ := ctx.Value(contactCtxKey).(*Contact)
	return c
}

type Middleware struct {
	Repo *Repo
}

func NewMiddleware(repo *Repo) *Middleware {
	return &Middleware{Repo: repo}
}

// RequireContact is the portal's only auth gate — unlike auth.Middleware
// there's no separate "load but don't require" step, since nothing in the
// portal (there's no nav bar shared with a logged-out state) needs a
// contact optionally attached.
func (m *Middleware) RequireContact(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(CookieName)
		if err != nil {
			http.Redirect(w, r, "/portal/expired", http.StatusFound)
			return
		}
		contact, err := m.Repo.GetContactBySessionToken(r.Context(), cookie.Value)
		if err != nil {
			http.Redirect(w, r, "/portal/expired", http.StatusFound)
			return
		}
		ctx := context.WithValue(r.Context(), contactCtxKey, contact)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
