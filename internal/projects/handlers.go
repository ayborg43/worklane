package projects

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sociolytik/odoo-clone/internal/activities"
	"github.com/sociolytik/odoo-clone/internal/attachments"
	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/notifications"
	"github.com/sociolytik/odoo-clone/internal/web"
)

type Handlers struct {
	Repo          *Repo
	Users         *auth.Repo
	Attachments   *attachments.Repo
	Notifications *notifications.Repo
	Activities    *activities.Repo
	Renderer      *web.Renderer
}

func NewHandlers(repo *Repo, users *auth.Repo, attachmentsRepo *attachments.Repo, notificationsRepo *notifications.Repo, activitiesRepo *activities.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Users: users, Attachments: attachmentsRepo, Notifications: notificationsRepo, Activities: activitiesRepo, Renderer: renderer}
}

// NotifyDueTasks scans for assigned, not-done tasks whose due date is today
// and haven't been reminded about yet, and notifies each assignee (in-app,
// plus email if the admin has configured outbound mail) via the same
// Notifications pipeline task-assignment and comment notifications already
// use. Meant to be called periodically by a ticker in main.go — it takes no
// http.Request, so it isn't wired as a route.
func (h *Handlers) NotifyDueTasks(ctx context.Context) {
	tasks, err := h.Repo.ListTasksDueTodayUnnotified(ctx)
	if err != nil {
		log.Printf("projects: list due tasks: %v", err)
		return
	}
	for _, t := range tasks {
		link := fmt.Sprintf("/projects/%d/tasks/%d", t.ProjectID, t.ID)
		body := fmt.Sprintf("Task %q is due today", t.Name)
		if err := h.Notifications.Create(ctx, t.AssigneeID, "task_due", body, link); err != nil {
			log.Printf("projects: notify due task %d: %v", t.ID, err)
			continue
		}
		if err := h.Repo.MarkDueReminderSent(ctx, t.ID); err != nil {
			log.Printf("projects: mark due reminder sent for task %d: %v", t.ID, err)
		}
	}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /projects", mw.RequireAuth(http.HandlerFunc(h.List)))
	mux.Handle("POST /projects", mw.RequireAuth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /projects/{id}", mw.RequireAuth(http.HandlerFunc(h.Show)))
	mux.Handle("GET /projects/{id}/tasks/options", mw.RequireAuth(http.HandlerFunc(h.TaskOptions)))
	mux.Handle("GET /projects/{id}/tasks/{taskID}", mw.RequireAuth(http.HandlerFunc(h.ShowTask)))
	mux.Handle("POST /projects/{id}/tasks/{taskID}/comments", mw.RequireAuth(http.HandlerFunc(h.CreateComment)))
	mux.Handle("POST /projects/{id}/tasks", mw.RequireAuth(http.HandlerFunc(h.CreateTask)))
	mux.Handle("PUT /projects/{id}/tasks/{taskID}", mw.RequireAuth(http.HandlerFunc(h.UpdateTask)))
	mux.Handle("DELETE /projects/{id}/tasks/{taskID}", mw.RequireAuth(http.HandlerFunc(h.DeleteTask)))
	mux.Handle("POST /projects/{id}/dependencies", mw.RequireAuth(http.HandlerFunc(h.AddDependency)))
	mux.Handle("DELETE /projects/{id}/dependencies/{taskID}/{depID}", mw.RequireAuth(http.HandlerFunc(h.RemoveDependency)))
	mux.Handle("POST /projects/{id}/members", mw.RequireAuth(http.HandlerFunc(h.AddMember)))
	mux.Handle("DELETE /projects/{id}/members/{userID}", mw.RequireAuth(http.HandlerFunc(h.RemoveMember)))
	mux.Handle("POST /projects/{id}/milestones", mw.RequireAuth(http.HandlerFunc(h.CreateMilestone)))
	mux.Handle("DELETE /projects/{id}/milestones/{milestoneID}", mw.RequireAuth(http.HandlerFunc(h.DeleteMilestone)))
	mux.Handle("POST /projects/{id}/tasks/{taskID}/subtasks", mw.RequireAuth(http.HandlerFunc(h.CreateSubtask)))
	mux.Handle("POST /projects/{id}/tasks/{taskID}/recurrence", mw.RequireAuth(http.HandlerFunc(h.SetRecurrence)))
	mux.Handle("POST /projects/{id}/task-templates", mw.RequireAuth(http.HandlerFunc(h.CreateTaskTemplate)))
	mux.Handle("DELETE /projects/{id}/task-templates/{templateID}", mw.RequireAuth(http.HandlerFunc(h.DeleteTaskTemplate)))
	mux.Handle("POST /projects/{id}/task-templates/{templateID}/use", mw.RequireAuth(http.HandlerFunc(h.UseTaskTemplate)))
}

// requireMember loads the project and 404s if the current user isn't a
// member — mirrors chat.Handlers' IsMember-then-404 convention (hides
// existence from non-members rather than a more revealing 403).
func (h *Handlers) requireMember(w http.ResponseWriter, r *http.Request, projectID int64) (*Project, bool) {
	project, err := h.Repo.GetProject(r.Context(), projectID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return nil, false
	}
	user := auth.UserFromContext(r.Context())
	isMember, err := h.Repo.IsMember(r.Context(), projectID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}
	if !isMember {
		http.NotFound(w, r)
		return nil, false
	}
	return project, true
}

// requireOwner is requireMember plus an ownership check (403, not 404 — the
// caller already knows the project exists once they're a member).
func (h *Handlers) requireOwner(w http.ResponseWriter, r *http.Request, projectID int64) (*Project, bool) {
	project, ok := h.requireMember(w, r, projectID)
	if !ok {
		return nil, false
	}
	if project.OwnerID != auth.UserFromContext(r.Context()).ID {
		http.Error(w, "only the project owner can manage members", http.StatusForbidden)
		return nil, false
	}
	return project, true
}

// pageData backs both the full project-show page (Render, via layout+content)
// and the standalone #project-body fragment (RenderFragment) re-rendered
// after every task/dependency mutation — see project_body.html.
type pageData struct {
	auth.PageData
	Project        Project
	Tasks          []Task
	TasksByStatus  map[string][]Task
	Users          []auth.User
	Members        []auth.User
	NonMembers     []auth.User
	Milestones     []Milestone
	ProjectID      int64
	ProjectFiles   []attachments.Attachment
	Activities     []activities.Activity
	Templates      []TaskTemplate
	TaskNames      map[int64]string
	GanttTasksJSON template.JS
	View           string
}

var taskStatuses = []string{"todo", "in_progress", "done"}

func groupTasksByStatus(tasks []Task) map[string][]Task {
	groups := make(map[string][]Task, len(taskStatuses))
	for _, s := range taskStatuses {
		groups[s] = []Task{}
	}
	for _, t := range tasks {
		groups[t.Status] = append(groups[t.Status], t)
	}
	return groups
}

// viewFromRequest reads ?view= and falls back to "list" for anything not one
// of the three views project_body.html knows how to render.
func viewFromRequest(r *http.Request) string {
	switch v := r.URL.Query().Get("view"); v {
	case "board", "gantt":
		return v
	default:
		return "list"
	}
}

type listData struct {
	auth.PageData
	Projects []Project
}

func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	list, err := h.Repo.ListProjects(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "projects/list.html", listData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Projects: list,
	})
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	description := strings.TrimSpace(r.FormValue("description"))

	user := auth.UserFromContext(r.Context())
	project, err := h.Repo.CreateProject(r.Context(), name, description, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%d", project.ID), http.StatusFound)
}

func (h *Handlers) Show(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	project, ok := h.requireMember(w, r, projectID)
	if !ok {
		return
	}
	tasks, err := h.Repo.ListTasks(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	members, err := h.Repo.ListMembers(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonMembers, err := h.Repo.ListNonMembers(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	files, err := h.Attachments.ListForProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	milestones, err := h.Repo.ListMilestones(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projectActivities, err := h.Activities.ListForProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	templates, err := h.Repo.ListTaskTemplates(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ganttJSON, err := BuildGanttTasksJSON(tasks)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Renderer.Render(w, http.StatusOK, "projects/show.html", pageData{
		PageData:       auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Project:        *project,
		Tasks:          tasks,
		TasksByStatus:  groupTasksByStatus(tasks),
		Users:          users,
		Members:        members,
		NonMembers:     nonMembers,
		Milestones:     milestones,
		ProjectID:      projectID,
		ProjectFiles:   files,
		Activities:     projectActivities,
		Templates:      templates,
		TaskNames:      taskNameLookup(tasks),
		GanttTasksJSON: template.JS(ganttJSON),
		View:           viewFromRequest(r),
	}, "projects/project_body.html", "projects/members_section.html", "projects/milestones_section.html",
		"attachments/project_files.html", "activities/project_activities.html", "projects/task_templates_section.html")
}

func (h *Handlers) TaskOptions(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	tasks, err := h.Repo.ListTasks(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "projects/task_options.html", tasks)
}

func (h *Handlers) CreateTask(w http.ResponseWriter, r *http.Request) {
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

	in, err := parseTaskInput(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	task, err := h.Repo.CreateTask(r.Context(), projectID, in)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if v := r.FormValue("depends_on"); v != "" {
		if depID, err := strconv.ParseInt(v, 10, 64); err == nil && depID != task.ID {
			// Only wire the dependency if the referenced task actually
			// belongs to this project — the form only ever offers this
			// project's own tasks, but a direct POST could name any task id.
			if depTask, err := h.Repo.GetTask(r.Context(), depID); err == nil && depTask.ProjectID == projectID {
				_ = h.Repo.AddDependency(r.Context(), task.ID, depID)
			}
		}
	}

	h.renderProjectBody(w, r, projectID)
}

func (h *Handlers) UpdateTask(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	taskID, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
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

	current, err := h.Repo.GetTask(r.Context(), taskID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if current.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}

	in := TaskInput{
		Name: current.Name, Description: current.Description, AssigneeID: current.AssigneeID,
		StartDate: current.StartDate, EndDate: current.EndDate,
		Progress: current.Progress, Status: current.Status,
	}

	// Only fields actually present in the submitted form are overlaid onto
	// the current task — this lets the Gantt's drag handlers PUT just
	// start_date/end_date (or progress) without clobbering the rest.
	if r.Form.Has("name") {
		if v := strings.TrimSpace(r.FormValue("name")); v != "" {
			in.Name = v
		}
	}
	if r.Form.Has("description") {
		in.Description = strings.TrimSpace(r.FormValue("description"))
	}
	if r.Form.Has("assignee_id") {
		v := r.FormValue("assignee_id")
		if v == "" {
			in.AssigneeID = 0
		} else {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "invalid assignee", http.StatusBadRequest)
				return
			}
			in.AssigneeID = id
		}
	}
	if v := r.FormValue("start_date"); v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			http.Error(w, "invalid start date", http.StatusBadRequest)
			return
		}
		in.StartDate = d
	}
	if v := r.FormValue("end_date"); v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			http.Error(w, "invalid end date", http.StatusBadRequest)
			return
		}
		in.EndDate = d
	}
	if v := r.FormValue("progress"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 0 || p > 100 {
			http.Error(w, "invalid progress", http.StatusBadRequest)
			return
		}
		in.Progress = p
	}
	if v := r.FormValue("status"); v != "" {
		in.Status = v
	}
	if in.EndDate.Before(in.StartDate) {
		http.Error(w, "end date must be on or after start date", http.StatusBadRequest)
		return
	}

	if _, err := h.Repo.UpdateTask(r.Context(), taskID, in); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if in.AssigneeID != 0 && in.AssigneeID != current.AssigneeID {
		link := fmt.Sprintf("/projects/%d/tasks/%d", projectID, taskID)
		body := fmt.Sprintf("You were assigned to task %q", in.Name)
		if err := h.Notifications.Create(r.Context(), in.AssigneeID, "task_assigned", body, link); err != nil {
			log.Printf("notifications: create: %v", err)
		}
	}

	h.spawnNextRecurrence(r.Context(), taskID, projectID, *current, in)

	h.renderProjectBody(w, r, projectID)
}

func (h *Handlers) DeleteTask(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	taskID, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	task, err := h.Repo.GetTask(r.Context(), taskID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if task.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.DeleteTask(r.Context(), taskID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderProjectBody(w, r, projectID)
}

func (h *Handlers) AddDependency(w http.ResponseWriter, r *http.Request) {
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
	taskID, err1 := strconv.ParseInt(r.FormValue("task_id"), 10, 64)
	dependsOnID, err2 := strconv.ParseInt(r.FormValue("depends_on_task_id"), 10, 64)
	if err1 != nil || err2 != nil || taskID == dependsOnID {
		http.Error(w, "invalid dependency", http.StatusBadRequest)
		return
	}
	task, err := h.Repo.GetTask(r.Context(), taskID)
	if err != nil || task.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	dep, err := h.Repo.GetTask(r.Context(), dependsOnID)
	if err != nil || dep.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.AddDependency(r.Context(), taskID, dependsOnID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderProjectBody(w, r, projectID)
}

func (h *Handlers) RemoveDependency(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	taskID, err1 := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
	depID, err2 := strconv.ParseInt(r.PathValue("depID"), 10, 64)
	if err1 != nil || err2 != nil {
		http.NotFound(w, r)
		return
	}
	task, err := h.Repo.GetTask(r.Context(), taskID)
	if err != nil || task.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.RemoveDependency(r.Context(), taskID, depID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderProjectBody(w, r, projectID)
}

func (h *Handlers) AddMember(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireOwner(w, r, projectID); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid user", http.StatusBadRequest)
		return
	}
	if err := h.Repo.AddMember(r.Context(), projectID, userID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderMembersSection(w, r, projectID)
}

func (h *Handlers) RemoveMember(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	memberID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	project, ok := h.requireOwner(w, r, projectID)
	if !ok {
		return
	}
	if memberID == project.OwnerID {
		http.Error(w, "cannot remove the project owner", http.StatusBadRequest)
		return
	}
	if err := h.Repo.RemoveMember(r.Context(), projectID, memberID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderMembersSection(w, r, projectID)
}

// CreateMilestone/DeleteMilestone use requireMember (not requireOwner) —
// milestones are a planning artifact like tasks, not an access-control
// concern like membership, so any member can manage them.
func (h *Handlers) CreateMilestone(w http.ResponseWriter, r *http.Request) {
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
	date, err := time.Parse("2006-01-02", r.FormValue("date"))
	if err != nil {
		http.Error(w, "invalid date", http.StatusBadRequest)
		return
	}
	if _, err := h.Repo.CreateMilestone(r.Context(), projectID, name, date); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderMilestonesSection(w, r, projectID)
}

func (h *Handlers) DeleteMilestone(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	milestoneID, err := strconv.ParseInt(r.PathValue("milestoneID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	milestone, err := h.Repo.GetMilestone(r.Context(), milestoneID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if milestone.ProjectID != projectID {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.DeleteMilestone(r.Context(), milestoneID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderMilestonesSection(w, r, projectID)
}

func (h *Handlers) renderMilestonesSection(w http.ResponseWriter, r *http.Request, projectID int64) {
	project, err := h.Repo.GetProject(r.Context(), projectID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	milestones, err := h.Repo.ListMilestones(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "projects/milestones_section.html", pageData{
		PageData:   auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Project:    *project,
		Milestones: milestones,
	})
}

func (h *Handlers) renderMembersSection(w http.ResponseWriter, r *http.Request, projectID int64) {
	project, err := h.Repo.GetProject(r.Context(), projectID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	members, err := h.Repo.ListMembers(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	nonMembers, err := h.Repo.ListNonMembers(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "projects/members_section.html", pageData{
		PageData:   auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Project:    *project,
		Members:    members,
		NonMembers: nonMembers,
	})
}

func (h *Handlers) renderProjectBody(w http.ResponseWriter, r *http.Request, projectID int64) {
	project, err := h.Repo.GetProject(r.Context(), projectID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	tasks, err := h.Repo.ListTasks(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	ganttJSON, err := BuildGanttTasksJSON(tasks)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Renderer.RenderFragment(w, http.StatusOK, "projects/project_body.html", pageData{
		Project:        *project,
		Tasks:          tasks,
		TasksByStatus:  groupTasksByStatus(tasks),
		TaskNames:      taskNameLookup(tasks),
		GanttTasksJSON: template.JS(ganttJSON),
		View:           viewFromRequest(r),
	})
}

func (h *Handlers) handleLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func taskNameLookup(tasks []Task) map[int64]string {
	m := make(map[int64]string, len(tasks))
	for _, t := range tasks {
		m[t.ID] = t.Name
	}
	return m
}

func parseTaskInput(r *http.Request) (TaskInput, error) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return TaskInput{}, fmt.Errorf("name is required")
	}
	start, err := time.Parse("2006-01-02", r.FormValue("start_date"))
	if err != nil {
		return TaskInput{}, fmt.Errorf("invalid start date")
	}
	end, err := time.Parse("2006-01-02", r.FormValue("end_date"))
	if err != nil {
		return TaskInput{}, fmt.Errorf("invalid end date")
	}
	if end.Before(start) {
		return TaskInput{}, fmt.Errorf("end date must be on or after start date")
	}

	var assigneeID int64
	if v := r.FormValue("assignee_id"); v != "" {
		assigneeID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return TaskInput{}, fmt.Errorf("invalid assignee")
		}
	}

	status := strings.TrimSpace(r.FormValue("status"))
	if status == "" {
		status = "todo"
	}

	return TaskInput{
		Name:        name,
		Description: strings.TrimSpace(r.FormValue("description")),
		AssigneeID:  assigneeID,
		StartDate:   start,
		EndDate:     end,
		Progress:    0,
		Status:      status,
	}, nil
}
