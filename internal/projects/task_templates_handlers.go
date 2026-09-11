package projects

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sociolytik/odoo-clone/internal/auth"
)

// taskTemplatesSectionData mirrors subtasksSectionData's rationale — same
// field names (ProjectID, Templates, Users) pageData also exposes, so
// task_templates_section.html renders correctly whether embedded in the
// full project page (via Show) or invoked standalone here.
type taskTemplatesSectionData struct {
	ProjectID int64
	Templates []TaskTemplate
	Users     []auth.User
}

func (h *Handlers) CreateTaskTemplate(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	duration, err := strconv.Atoi(r.FormValue("duration_days"))
	if err != nil || duration < 1 {
		http.Error(w, "duration must be at least 1 day", http.StatusBadRequest)
		return
	}
	var assigneeID int64
	if v := r.FormValue("assignee_id"); v != "" {
		assigneeID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			http.Error(w, "invalid assignee", http.StatusBadRequest)
			return
		}
	}
	description := strings.TrimSpace(r.FormValue("description"))

	if _, err := h.Repo.CreateTaskTemplate(r.Context(), projectID, name, description, duration, assigneeID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderTaskTemplates(w, r, projectID)
}

func (h *Handlers) DeleteTaskTemplate(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	templateID, err := strconv.ParseInt(r.PathValue("templateID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	tmpl, err := h.Repo.GetTaskTemplate(r.Context(), templateID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if tmpl.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.DeleteTaskTemplate(r.Context(), templateID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderTaskTemplates(w, r, projectID)
}

// UseTaskTemplate creates a real task from a template plus a chosen start
// date (end date computed from the template's default duration), then
// re-renders project_body.html the same way CreateTask does — the "Use"
// button lives in the Task Templates section but its hx-target points at
// #project-body elsewhere on the page, which htmx supports fine since
// hx-target is just a CSS selector, not restricted to an ancestor.
func (h *Handlers) UseTaskTemplate(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	templateID, err := strconv.ParseInt(r.PathValue("templateID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	tmpl, err := h.Repo.GetTaskTemplate(r.Context(), templateID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if tmpl.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	start, err := time.Parse("2006-01-02", r.FormValue("start_date"))
	if err != nil {
		http.Error(w, "invalid start date", http.StatusBadRequest)
		return
	}
	end := start.AddDate(0, 0, tmpl.DefaultDurationDays)

	_, err = h.Repo.CreateTask(r.Context(), projectID, TaskInput{
		Name: tmpl.Name, Description: tmpl.Description, AssigneeID: tmpl.DefaultAssigneeID,
		StartDate: start, EndDate: end, Progress: 0, Status: "todo",
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderProjectBody(w, r, projectID)
}

func (h *Handlers) renderTaskTemplates(w http.ResponseWriter, r *http.Request, projectID int64) {
	templates, err := h.Repo.ListTaskTemplates(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "projects/task_templates_section.html", taskTemplatesSectionData{
		ProjectID: projectID,
		Templates: templates,
		Users:     users,
	})
}
