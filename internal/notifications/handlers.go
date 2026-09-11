package notifications

import (
	"net/http"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/web"
)

const recentLimit = 20

type Handlers struct {
	Repo     *Repo
	Renderer *web.Renderer
}

func NewHandlers(repo *Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /notifications/panel", mw.RequireAuth(http.HandlerFunc(h.Panel)))
	mux.Handle("POST /notifications/read", mw.RequireAuth(http.HandlerFunc(h.MarkRead)))
}

type panelData struct {
	Items       []Notification
	UnreadCount int
}

func (h *Handlers) Panel(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	items, err := h.Repo.ListRecentForUser(r.Context(), user.ID, recentLimit)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	unread, err := h.Repo.UnreadCount(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "notifications/panel.html", panelData{Items: items, UnreadCount: unread})
}

func (h *Handlers) MarkRead(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if err := h.Repo.MarkAllRead(r.Context(), user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Panel(w, r)
}
