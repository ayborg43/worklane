package search

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

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /search", mw.RequireAuth(http.HandlerFunc(h.Search)))
}

type resultsData struct {
	Query   string
	Results []Result
}

func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	q := r.URL.Query().Get("q")
	results, err := h.Repo.Search(r.Context(), user.ID, q)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "search/results.html", resultsData{
		Query:   q,
		Results: results,
	})
}
