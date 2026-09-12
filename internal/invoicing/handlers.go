package invoicing

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/web"
)

// Handlers scopes everything to the project owner — billing is treated as
// an owner-only concern here (like adding/removing members), simpler than
// splitting "any member can view, only the owner can generate/change
// status" into two permission levels for a first pass. Non-owners get a
// 404 (hides existence), same convention as non-members elsewhere.
type Handlers struct {
	Repo     *Repo
	Renderer *web.Renderer
}

func NewHandlers(repo *Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /projects/{id}/invoices", mw.RequireAuth(http.HandlerFunc(h.Index)))
	mux.Handle("POST /projects/{id}/invoices/rate", mw.RequireAuth(http.HandlerFunc(h.SetRate)))
	mux.Handle("POST /projects/{id}/invoices/contact", mw.RequireAuth(http.HandlerFunc(h.SetContact)))
	mux.Handle("POST /projects/{id}/invoices", mw.RequireAuth(http.HandlerFunc(h.Generate)))
	mux.Handle("GET /invoices/{id}", mw.RequireAuth(http.HandlerFunc(h.Show)))
	mux.Handle("POST /invoices/{id}/status", mw.RequireAuth(http.HandlerFunc(h.SetStatus)))
	mux.Handle("DELETE /invoices/{id}", mw.RequireAuth(http.HandlerFunc(h.Delete)))
}

// requireOwner loads the project's owner/name/rate/contact and 404s anyone
// else — invoicing has no separate view-only membership tier, see the
// Handlers doc comment.
func (h *Handlers) requireOwner(w http.ResponseWriter, r *http.Request, projectID int64) (name string, rate float64, contactID int64, ok bool) {
	name, ownerID, rate, contactID, err := h.Repo.ProjectInfo(r.Context(), projectID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return "", 0, 0, false
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return "", 0, 0, false
	}
	user := auth.UserFromContext(r.Context())
	if user.ID != ownerID {
		http.NotFound(w, r)
		return "", 0, 0, false
	}
	return name, rate, contactID, true
}

type indexData struct {
	auth.PageData
	ProjectID    int64
	ProjectName  string
	BillableRate float64
	ContactID    int64
	Contacts     []ContactOption
	Invoices     []Invoice
	Error        string
}

func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	projectName, rate, contactID, ok := h.requireOwner(w, r, projectID)
	if !ok {
		return
	}
	invoices, err := h.Repo.ListForProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.render(w, r, projectID, projectName, rate, contactID, invoices, "")
}

func (h *Handlers) render(w http.ResponseWriter, r *http.Request, projectID int64, projectName string, rate float64, contactID int64, invoices []Invoice, errMsg string) {
	contacts, err := h.Repo.ListContactOptions(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "invoicing/index.html", indexData{
		PageData:     auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		ProjectID:    projectID,
		ProjectName:  projectName,
		BillableRate: rate,
		ContactID:    contactID,
		Contacts:     contacts,
		Invoices:     invoices,
		Error:        errMsg,
	})
}

func (h *Handlers) SetRate(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, _, _, ok := h.requireOwner(w, r, projectID); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	rate, err := strconv.ParseFloat(r.FormValue("rate"), 64)
	if err != nil || rate < 0 {
		http.Error(w, "invalid rate", http.StatusBadRequest)
		return
	}
	if err := h.Repo.SetBillableRate(r.Context(), projectID, rate); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(projectID, 10)+"/invoices", http.StatusSeeOther)
}

// SetContact assigns which CRM contact is this project's "client" — the one
// whose customer portal sees its invoices. Uses a plain native form (not
// htmx) in the template, same as the "Generate invoice" form on this same
// page, sidestepping any question of how an htmx-driven POST should handle
// a full-page redirect response.
func (h *Handlers) SetContact(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, _, _, ok := h.requireOwner(w, r, projectID); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	var contactID int64
	if v := r.FormValue("contact_id"); v != "" {
		contactID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid contact", http.StatusBadRequest)
			return
		}
	}
	if err := h.Repo.SetContact(r.Context(), projectID, contactID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(projectID, 10)+"/invoices", http.StatusSeeOther)
}

func (h *Handlers) Generate(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	projectName, rate, contactID, ok := h.requireOwner(w, r, projectID)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	from, err := time.Parse("2006-01-02", r.FormValue("period_start"))
	if err != nil {
		http.Error(w, "invalid period start", http.StatusBadRequest)
		return
	}
	// Both dates are inclusive here — GenerateInvoice handles the
	// [from, to) exclusive-bound conversion internally, so what's stored
	// and later displayed matches exactly what was typed.
	to, err := time.Parse("2006-01-02", r.FormValue("period_end"))
	if err != nil {
		http.Error(w, "invalid period end", http.StatusBadRequest)
		return
	}

	user := auth.UserFromContext(r.Context())
	_, err = h.Repo.GenerateInvoice(r.Context(), projectID, user.ID, from, to)
	if err != nil {
		if errors.Is(err, ErrNoBillableHours) {
			invoices, listErr := h.Repo.ListForProject(r.Context(), projectID)
			if listErr != nil {
				http.Error(w, "internal error", http.StatusInternalServerError)
				return
			}
			h.render(w, r, projectID, projectName, rate, contactID, invoices, "No billable, approved hours in that period.")
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/projects/"+strconv.FormatInt(projectID, 10)+"/invoices", http.StatusSeeOther)
}

type showData struct {
	auth.PageData
	Invoice Invoice
}

func (h *Handlers) Show(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	inv, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, _, _, ok := h.requireOwner(w, r, inv.ProjectID); !ok {
		return
	}
	h.Renderer.Render(w, http.StatusOK, "invoicing/detail.html", showData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Invoice:  *inv,
	})
}

var validStatuses = map[string]bool{"draft": true, "sent": true, "paid": true}

func (h *Handlers) SetStatus(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	inv, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, _, _, ok := h.requireOwner(w, r, inv.ProjectID); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	status := r.FormValue("status")
	if !validStatuses[status] {
		http.Error(w, "invalid status", http.StatusBadRequest)
		return
	}
	if err := h.Repo.SetStatus(r.Context(), id, status); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Show(w, r)
}

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	inv, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if _, _, _, ok := h.requireOwner(w, r, inv.ProjectID); !ok {
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "only draft invoices can be deleted", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Redirect", "/projects/"+strconv.FormatInt(inv.ProjectID, 10)+"/invoices")
	w.WriteHeader(http.StatusOK)
}
