package wiki

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/web"
)

// MembershipChecker is satisfied structurally by *projects.Repo — same
// interface-not-import pattern attachments/activities already use, so
// wiki never imports projects, only the reverse.
type MembershipChecker interface {
	IsMember(ctx context.Context, projectID, userID int64) (bool, error)
}

type Handlers struct {
	Repo     *Repo
	Members  MembershipChecker
	Renderer *web.Renderer
}

func NewHandlers(repo *Repo, members MembershipChecker, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Members: members, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /projects/{id}/wiki", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.Index)))
	mux.Handle("POST /projects/{id}/wiki", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.Create)))
	mux.Handle("GET /projects/{id}/wiki/{pageID}", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.Show)))
	mux.Handle("PUT /projects/{id}/wiki/{pageID}", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.Update)))
	mux.Handle("DELETE /projects/{id}/wiki/{pageID}", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.Delete)))
}

func (h *Handlers) checkMember(w http.ResponseWriter, r *http.Request, projectID, userID int64) bool {
	isMember, err := h.Members.IsMember(r.Context(), projectID, userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return false
	}
	if !isMember {
		http.NotFound(w, r)
		return false
	}
	return true
}

type indexData struct {
	auth.PageData
	ProjectID   int64
	ProjectName string
	Pages       []Page
}

func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return
	}
	pages, err := h.Repo.ListForProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projectName, err := h.Repo.ProjectName(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "wiki/index.html", indexData{
		PageData:    auth.PageData{CurrentUser: user},
		ProjectID:   projectID,
		ProjectName: projectName,
		Pages:       pages,
	})
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	page, err := h.Repo.Create(r.Context(), projectID, PageInput{
		Title: title,
		Body:  r.FormValue("body"),
	}, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(projectID, 10)+"/wiki/"+strconv.FormatInt(page.ID, 10), http.StatusSeeOther)
}

type showData struct {
	auth.PageData
	ProjectID    int64
	ProjectName  string
	Page         Page
	RenderedBody template.HTML
}

func (h *Handlers) Show(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pageID, err := strconv.ParseInt(r.PathValue("pageID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return
	}
	page, err := h.Repo.Get(r.Context(), pageID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if page.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	titles, err := h.Repo.TitleIndex(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projectName, err := h.Repo.ProjectName(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "wiki/page.html", showData{
		PageData:     auth.PageData{CurrentUser: user},
		ProjectID:    projectID,
		ProjectName:  projectName,
		Page:         *page,
		RenderedBody: RenderMarkdown(page.Body, titles, projectID),
	}, "wiki/page_body.html")
}

// renderPageBody re-renders just the #wiki-page fragment after a save —
// unlike Show, it never wraps the result in the full layout+nav.
func (h *Handlers) renderPageBody(w http.ResponseWriter, r *http.Request, projectID, pageID int64) {
	page, err := h.Repo.Get(r.Context(), pageID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	titles, err := h.Repo.TitleIndex(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "wiki/page_body.html", showData{
		PageData:     auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		ProjectID:    projectID,
		Page:         *page,
		RenderedBody: RenderMarkdown(page.Body, titles, projectID),
	})
}

func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pageID, err := strconv.ParseInt(r.PathValue("pageID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return
	}
	page, err := h.Repo.Get(r.Context(), pageID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if page.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title == "" {
		http.Error(w, "title is required", http.StatusBadRequest)
		return
	}
	if err := h.Repo.Update(r.Context(), pageID, PageInput{Title: title, Body: r.FormValue("body")}, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderPageBody(w, r, projectID, pageID)
}

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pageID, err := strconv.ParseInt(r.PathValue("pageID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return
	}
	page, err := h.Repo.Get(r.Context(), pageID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if page.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.Delete(r.Context(), pageID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/projects/"+strconv.FormatInt(projectID, 10)+"/wiki")
	w.WriteHeader(http.StatusOK)
}
