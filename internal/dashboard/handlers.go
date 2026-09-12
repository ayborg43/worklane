package dashboard

import (
	"net/http"

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

// MountRoutes takes no *auth.Middleware: the root route serves both logged-
// in (dashboard) and logged-out (redirect to /login) visitors, so it checks
// auth itself instead of being wrapped in RequireAuth.
func (h *Handlers) MountRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", h.Index)
}

type indexData struct {
	auth.PageData
	Data
}

func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	data, err := h.Repo.Load(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "dashboard/index.html", indexData{
		PageData: auth.PageData{CurrentUser: user},
		Data:     data,
	})
}
