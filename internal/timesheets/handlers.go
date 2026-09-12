package timesheets

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
	"github.com/sociolytik/odoo-clone/internal/projects"
	"github.com/sociolytik/odoo-clone/internal/web"
)

type Handlers struct {
	Repo          *Repo
	Projects      *projects.Repo
	Notifications *notifications.Repo
	Renderer      *web.Renderer
}

func NewHandlers(repo *Repo, projectsRepo *projects.Repo, notificationsRepo *notifications.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Projects: projectsRepo, Notifications: notificationsRepo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /timesheets", mw.RequireAuth(http.HandlerFunc(h.List)))
	mux.Handle("POST /timesheets", mw.RequireAuth(http.HandlerFunc(h.Create)))
	mux.Handle("DELETE /timesheets/{id}", mw.RequireAuth(http.HandlerFunc(h.Delete)))
	mux.Handle("POST /timesheets/{id}/billable", mw.RequireAuth(http.HandlerFunc(h.SetBillable)))
	mux.Handle("GET /timesheets/approvals", mw.RequireAuth(http.HandlerFunc(h.Approvals)))
	mux.Handle("POST /timesheets/{id}/approve", mw.RequireAuth(http.HandlerFunc(h.Approve)))
	mux.Handle("POST /timesheets/{id}/reject", mw.RequireAuth(http.HandlerFunc(h.Reject)))
	mux.Handle("GET /reports", mw.RequireAuth(http.HandlerFunc(h.Reports)))
}

type dayGroup struct {
	Date    time.Time
	Entries []Entry
	Total   float64
}

// bodyData backs both the full /timesheets page (Render, via layout+content)
// and the standalone #timesheet-body fragment (RenderFragment) re-rendered
// after create/delete — mirrors the projects module's pageData pattern.
type bodyData struct {
	auth.PageData
	WeekStart  time.Time
	WeekEnd    time.Time
	PrevWeek   string
	NextWeek   string
	Days       []dayGroup
	WeekTotal  float64
	HasEntries bool
	Projects   []projects.Project
}

// mondayOf returns midnight UTC on the Monday of the week containing t.
func mondayOf(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(t.Weekday()) + 6) % 7 // Monday=0 .. Sunday=6
	return t.AddDate(0, 0, -offset)
}

func weekStartFromRequest(r *http.Request, param string) time.Time {
	if v := r.URL.Query().Get(param); v != "" {
		if d, err := time.Parse("2006-01-02", v); err == nil {
			return mondayOf(d)
		}
	}
	return mondayOf(time.Now())
}

func (h *Handlers) loadBodyData(r *http.Request, weekStart time.Time) (bodyData, error) {
	user := auth.UserFromContext(r.Context())
	weekEnd := weekStart.AddDate(0, 0, 7) // exclusive upper bound

	entries, err := h.Repo.ListForUserWeek(r.Context(), user.ID, weekStart, weekEnd)
	if err != nil {
		return bodyData{}, err
	}

	days := make([]dayGroup, 7)
	for i := range days {
		days[i].Date = weekStart.AddDate(0, 0, i)
	}
	var weekTotal float64
	for _, e := range entries {
		idx := int(e.WorkDate.Sub(weekStart).Hours() / 24)
		if idx < 0 || idx > 6 {
			continue
		}
		days[idx].Entries = append(days[idx].Entries, e)
		days[idx].Total += e.Hours
		weekTotal += e.Hours
	}

	projectList, err := h.Projects.ListProjects(r.Context(), user.ID)
	if err != nil {
		return bodyData{}, err
	}

	return bodyData{
		PageData:   auth.PageData{CurrentUser: user},
		WeekStart:  weekStart,
		WeekEnd:    weekStart.AddDate(0, 0, 6),
		PrevWeek:   weekStart.AddDate(0, 0, -7).Format("2006-01-02"),
		NextWeek:   weekStart.AddDate(0, 0, 7).Format("2006-01-02"),
		Days:       days,
		WeekTotal:  weekTotal,
		HasEntries: len(entries) > 0,
		Projects:   projectList,
	}, nil
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	weekStart := weekStartFromRequest(r, "week")
	data, err := h.loadBodyData(r, weekStart)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "timesheets/list.html", data, "timesheets/week_body.html")
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	projectID, err := strconv.ParseInt(r.FormValue("project_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid project", http.StatusBadRequest)
		return
	}
	if isMember, err := h.Projects.IsMember(r.Context(), projectID, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	} else if !isMember {
		http.Error(w, "not a member of this project", http.StatusForbidden)
		return
	}
	var taskID int64
	if v := r.FormValue("task_id"); v != "" {
		taskID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid task", http.StatusBadRequest)
			return
		}
	}
	workDate, err := time.Parse("2006-01-02", r.FormValue("work_date"))
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	hours, err := strconv.ParseFloat(r.FormValue("hours"), 64)
	if err != nil || hours <= 0 || hours > 24 {
		http.Error(w, "invalid hours", http.StatusBadRequest)
		return
	}

	if _, err := h.Repo.Create(r.Context(), user.ID, EntryInput{
		ProjectID:   projectID,
		TaskID:      taskID,
		WorkDate:    workDate,
		Hours:       hours,
		Description: strings.TrimSpace(r.FormValue("description")),
		Billable:    r.FormValue("billable") != "",
	}); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.renderBody(w, r, mondayOf(workDate))
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
			http.Error(w, "cannot delete: entry not found or already approved", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderBody(w, r, weekStartFromRequest(r, "week"))
}

// SetBillable toggles an entry's billable flag from the week view's
// checkbox — htmx submits it on change, no separate form/submit needed.
func (h *Handlers) SetBillable(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())
	// This route is only ever hit via the week view's checkbox, whose
	// hx-vals="js:{billable: event.target.checked}" sends the literal
	// string "true"/"false" — unlike a native unchecked checkbox, which
	// omits the field entirely. Both conventions are accepted so a plain
	// form post (e.g. from a test) also works the intuitive way.
	v := r.FormValue("billable")
	billable := v == "true" || v == "on"
	if err := h.Repo.SetBillable(r.Context(), id, user.ID, billable); err != nil {
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "cannot update: entry not found or already invoiced", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderBody(w, r, weekStartFromRequest(r, "week"))
}

func (h *Handlers) renderBody(w http.ResponseWriter, r *http.Request, weekStart time.Time) {
	data, err := h.loadBodyData(r, weekStart)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "timesheets/week_body.html", data)
}

type approvalsData struct {
	auth.PageData
	Entries []Entry
}

func (h *Handlers) Approvals(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	entries, err := h.Repo.ListPendingForOwner(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "timesheets/approvals.html", approvalsData{
		PageData: auth.PageData{CurrentUser: user},
		Entries:  entries,
	}, "timesheets/approvals_body.html")
}

func (h *Handlers) Approve(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	entry, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if entry.ProjectOwnerID != user.ID {
		http.NotFound(w, r) // hide existence from non-owners
		return
	}
	if err := h.Repo.Approve(r.Context(), id, user.ID); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "entry already reviewed", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	link := "/timesheets?week=" + mondayOf(entry.WorkDate).Format("2006-01-02")
	body := fmt.Sprintf("Your %.2fh entry on %s was approved", entry.Hours, entry.WorkDate.Format("Jan 2"))
	if err := h.Notifications.Create(r.Context(), entry.UserID, "timesheet_approved", body, link); err != nil {
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
		http.Error(w, "a reason is required to reject an entry", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())
	entry, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if entry.ProjectOwnerID != user.ID {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.Reject(r.Context(), id, user.ID, reason); err != nil {
		if errors.Is(err, ErrConflict) {
			http.Error(w, "entry already reviewed", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	link := "/timesheets?week=" + mondayOf(entry.WorkDate).Format("2006-01-02")
	body := fmt.Sprintf("Your %.2fh entry on %s was rejected: %s", entry.Hours, entry.WorkDate.Format("Jan 2"), reason)
	if err := h.Notifications.Create(r.Context(), entry.UserID, "timesheet_rejected", body, link); err != nil {
		log.Printf("notifications: create: %v", err)
	}

	h.renderApprovals(w, r)
}

func (h *Handlers) renderApprovals(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	entries, err := h.Repo.ListPendingForOwner(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "timesheets/approvals_body.html", entries)
}
