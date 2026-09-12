package timeoff

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/notifications"
	"github.com/sociolytik/odoo-clone/internal/web"
)

type Handlers struct {
	Repo          *Repo
	Notifications *notifications.Repo
	Renderer      *web.Renderer
}

func NewHandlers(repo *Repo, notificationsRepo *notifications.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Notifications: notificationsRepo, Renderer: renderer}
}

// Approvals/Approve/Reject are admin-only (RequireAdmin, same as Settings)
// — time off has no project-owner concept to reuse the way timesheets'
// approval queue does, and an org-wide admin is the only elevated role
// this app already has.
func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /timeoff", mw.RequireAuthAndModule("timeoff", http.HandlerFunc(h.List)))
	mux.Handle("POST /timeoff", mw.RequireAuthAndModule("timeoff", http.HandlerFunc(h.Create)))
	mux.Handle("DELETE /timeoff/{id}", mw.RequireAuthAndModule("timeoff", http.HandlerFunc(h.Delete)))
	mux.Handle("GET /timeoff/approvals", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Approvals))))
	mux.Handle("POST /timeoff/{id}/approve", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Approve))))
	mux.Handle("POST /timeoff/{id}/reject", mw.RequireAuth(mw.RequireAdmin(http.HandlerFunc(h.Reject))))
}

var leaveTypes = map[string]bool{"vacation": true, "sick": true, "unpaid": true, "other": true}

type listData struct {
	auth.PageData
	Requests []Request
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	requests, err := h.Repo.ListForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "timeoff/list.html", listData{
		PageData: auth.PageData{CurrentUser: user},
		Requests: requests,
	}, "timeoff/list_body.html")
}

// renderBody re-renders just the #timeoff-body fragment after a
// create/withdraw — unlike List, it never wraps the result in the full
// layout+nav.
func (h *Handlers) renderBody(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	requests, err := h.Repo.ListForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "timeoff/list_body.html", requests)
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	leaveType := r.FormValue("leave_type")
	if !leaveTypes[leaveType] {
		http.Error(w, "invalid leave type", http.StatusBadRequest)
		return
	}
	start, err := time.Parse("2006-01-02", r.FormValue("start_date"))
	if err != nil {
		http.Error(w, "invalid start date", http.StatusBadRequest)
		return
	}
	end, err := time.Parse("2006-01-02", r.FormValue("end_date"))
	if err != nil {
		http.Error(w, "invalid end date", http.StatusBadRequest)
		return
	}
	if end.Before(start) {
		http.Error(w, "end date must be on or after start date", http.StatusBadRequest)
		return
	}

	if _, err := h.Repo.Create(r.Context(), user.ID, RequestInput{
		LeaveType: leaveType,
		StartDate: start,
		EndDate:   end,
		Reason:    strings.TrimSpace(r.FormValue("reason")),
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderBody(w, r)
}

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if err := h.Repo.Delete(r.Context(), id, user.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "cannot withdraw: request not found or already reviewed", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderBody(w, r)
}

type approvalsData struct {
	auth.PageData
	Requests []Request
}

func (h *Handlers) Approvals(w http.ResponseWriter, r *http.Request) {
	requests, err := h.Repo.ListPending(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "timeoff/approvals.html", approvalsData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Requests: requests,
	}, "timeoff/approvals_body.html")
}

func (h *Handlers) renderApprovals(w http.ResponseWriter, r *http.Request) {
	requests, err := h.Repo.ListPending(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "timeoff/approvals_body.html", requests)
}

func (h *Handlers) Approve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	req, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	admin := auth.UserFromContext(r.Context())
	if err := h.Repo.Approve(r.Context(), id, admin.ID); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "request already reviewed", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	body := fmt.Sprintf("Your %s request (%s – %s) was approved",
		req.LeaveType, req.StartDate.Format("Jan 2"), req.EndDate.Format("Jan 2"))
	if err := h.Notifications.Create(r.Context(), req.UserID, "leave_approved", body, "/timeoff"); err != nil {
		log.Printf("notifications: create: %v", err)
	}
	h.renderApprovals(w, r)
}

func (h *Handlers) Reject(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	reason := strings.TrimSpace(r.FormValue("reason"))
	if reason == "" {
		http.Error(w, "a reason is required to reject a request", http.StatusBadRequest)
		return
	}
	req, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	admin := auth.UserFromContext(r.Context())
	if err := h.Repo.Reject(r.Context(), id, admin.ID, reason); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "request already reviewed", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	body := fmt.Sprintf("Your %s request (%s – %s) was rejected: %s",
		req.LeaveType, req.StartDate.Format("Jan 2"), req.EndDate.Format("Jan 2"), reason)
	if err := h.Notifications.Create(r.Context(), req.UserID, "leave_rejected", body, "/timeoff"); err != nil {
		log.Printf("notifications: create: %v", err)
	}
	h.renderApprovals(w, r)
}
