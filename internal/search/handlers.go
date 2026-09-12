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

// resultModule maps a Result's Kind to the module that gates it, so a
// restricted user's search never surfaces a hit for a module they can't
// open — the same existence-hiding posture RequireModule already applies
// to the routes those hits would link to.
var resultModule = map[string]string{
	"task":    "projects",
	"ticket":  "helpdesk",
	"contact": "crm",
	"wiki":    "projects",
	"invoice": "projects",
}

func (h *Handlers) Search(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	q := r.URL.Query().Get("q")
	results, err := h.Repo.Search(r.Context(), user.ID, q)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	visible := results[:0]
	for _, res := range results {
		if module, ok := resultModule[res.Kind]; ok && !user.CanAccess(module) {
			continue
		}
		visible = append(visible, res)
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "search/results.html", resultsData{
		Query:   q,
		Results: visible,
	})
}
