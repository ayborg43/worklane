package activities

import (
	"context"
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

// MembershipChecker is satisfied structurally by *projects.Repo — same
// one-directional dependency shape attachments.Handlers already uses:
// activities never imports projects, only the reverse.
type MembershipChecker interface {
	IsMember(ctx context.Context, projectID, userID int64) (bool, error)
	ListMembers(ctx context.Context, projectID int64) ([]auth.User, error)
}

type Handlers struct {
	Repo          *Repo
	Members       MembershipChecker
	Notifications *notifications.Repo
	Renderer      *web.Renderer
}

func NewHandlers(repo *Repo, members MembershipChecker, notificationsRepo *notifications.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Members: members, Notifications: notificationsRepo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /activities", mw.RequireAuth(http.HandlerFunc(h.MyActivities)))
	mux.Handle("POST /projects/{id}/activities", mw.RequireAuth(http.HandlerFunc(h.CreateForProject)))
	mux.Handle("POST /projects/{id}/tasks/{taskID}/activities", mw.RequireAuth(http.HandlerFunc(h.CreateForTask)))
	mux.Handle("POST /activities/{id}/done", mw.RequireAuth(http.HandlerFunc(h.MarkDone)))
	mux.Handle("DELETE /activities/{id}", mw.RequireAuth(http.HandlerFunc(h.Delete)))
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

var validKinds = map[string]bool{"call": true, "meeting": true, "todo": true, "email": true}

func activityLink(a *Activity) string {
	if a.TaskID != 0 {
		return fmt.Sprintf("/projects/%d/tasks/%d", a.ProjectID, a.TaskID)
	}
	return fmt.Sprintf("/projects/%d", a.ProjectID)
}

// create is shared by CreateForProject/CreateForTask — taskID is 0 for a
// project-level activity.
func (h *Handlers) create(w http.ResponseWriter, r *http.Request, projectID, taskID int64) (*Activity, bool) {
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return nil, false
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return nil, false
	}
	kind := r.FormValue("kind")
	if !validKinds[kind] {
		http.Error(w, "invalid activity type", http.StatusBadRequest)
		return nil, false
	}
	dueDate, err := time.Parse("2006-01-02", r.FormValue("due_date"))
	if err != nil {
		http.Error(w, "invalid due date", http.StatusBadRequest)
		return nil, false
	}
	assignedTo, err := strconv.ParseInt(r.FormValue("assigned_to"), 10, 64)
	if err != nil {
		http.Error(w, "invalid assignee", http.StatusBadRequest)
		return nil, false
	}
	// Re-validated server-side, not just left to the dropdown's own
	// options — the same "don't trust a direct POST just because the UI
	// hides the option" posture as project membership checks elsewhere.
	if !h.checkMember(w, r, projectID, assignedTo) {
		return nil, false
	}
	note := strings.TrimSpace(r.FormValue("note"))

	activity, err := h.Repo.Create(r.Context(), Input{
		ProjectID: projectID, TaskID: taskID, Kind: kind, Note: note,
		DueDate: dueDate, AssignedTo: assignedTo, CreatedBy: user.ID,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, false
	}

	if assignedTo != user.ID {
		body := fmt.Sprintf("%s scheduled a %s for you", user.Name, activity.KindLabel())
		if activity.Note != "" {
			body = fmt.Sprintf("%s scheduled a %s for you: %s", user.Name, activity.KindLabel(), activity.Note)
		}
		if err := h.Notifications.Create(r.Context(), assignedTo, "activity_assigned", body, activityLink(activity)); err != nil {
			log.Printf("notifications: create: %v", err)
		}
	}
	return activity, true
}

func (h *Handlers) CreateForProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.create(w, r, projectID, 0); !ok {
		return
	}
	h.renderProjectActivities(w, r, projectID)
}

func (h *Handlers) CreateForTask(w http.ResponseWriter, r *http.Request) {
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
	if _, ok := h.create(w, r, projectID, taskID); !ok {
		return
	}
	h.renderTaskActivities(w, r, taskID)
}

// MarkDone/Delete are reachable by the assignee (completing their own
// action item) or the creator (cancelling one they scheduled by mistake).
func (h *Handlers) MarkDone(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	activity, ok := h.loadForMutation(w, r, id)
	if !ok {
		return
	}
	if err := h.Repo.MarkDone(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderAfterMutation(w, r, activity)
}

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	activity, ok := h.loadForMutation(w, r, id)
	if !ok {
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderAfterMutation(w, r, activity)
}

func (h *Handlers) loadForMutation(w http.ResponseWriter, r *http.Request, id int64) (*Activity, bool) {
	activity, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return nil, false
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, activity.ProjectID, user.ID) {
		return nil, false
	}
	if activity.AssignedTo != user.ID && activity.CreatedBy != user.ID {
		http.Error(w, "only the assignee or creator can do that", http.StatusForbidden)
		return nil, false
	}
	return activity, true
}

// renderAfterMutation re-renders whichever fragment the request came
// from. The My Activities page marks that via ?view=mine, since an
// activity's own TaskID/ProjectID alone can't distinguish "embedded in its
// project/task page" from "listed on the aggregated My Activities page".
func (h *Handlers) renderAfterMutation(w http.ResponseWriter, r *http.Request, activity *Activity) {
	if r.URL.Query().Get("view") == "mine" {
		h.renderMyActivities(w, r)
		return
	}
	if activity.TaskID != 0 {
		h.renderTaskActivities(w, r, activity.TaskID)
		return
	}
	h.renderProjectActivities(w, r, activity.ProjectID)
}

// sectionData backs the standalone activities-section fragment re-render
// after create/done/delete. It deliberately uses the same field names
// (ProjectID, TaskID, Members, Activities, plus the promoted CurrentUser)
// that projects.pageData/taskDetailData also expose, so the same
// project_activities.html/task_activities.html template files render
// correctly whether invoked from here or embedded in the full project/task
// page — html/template matches by field name via reflection, not by
// concrete Go type, so no shared type between the two packages is needed.
type sectionData struct {
	auth.PageData
	ProjectID  int64
	TaskID     int64
	Members    []auth.User
	Activities []Activity
}

func (h *Handlers) renderProjectActivities(w http.ResponseWriter, r *http.Request, projectID int64) {
	list, err := h.Repo.ListForProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	members, err := h.Members.ListMembers(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "activities/project_activities.html", sectionData{
		PageData:   auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		ProjectID:  projectID,
		Members:    members,
		Activities: list,
	})
}

func (h *Handlers) renderTaskActivities(w http.ResponseWriter, r *http.Request, taskID int64) {
	list, err := h.Repo.ListForTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	projectID := int64(0)
	if len(list) > 0 {
		projectID = list[0].ProjectID
	}
	var members []auth.User
	if projectID != 0 {
		var err error
		members, err = h.Members.ListMembers(r.Context(), projectID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "activities/task_activities.html", sectionData{
		PageData:   auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		ProjectID:  projectID,
		TaskID:     taskID,
		Members:    members,
		Activities: list,
	})
}

type myActivitiesData struct {
	auth.PageData
	Activities []Activity
}

func (h *Handlers) MyActivities(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	list, err := h.Repo.ListPendingForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "activities/index.html", myActivitiesData{
		PageData:   auth.PageData{CurrentUser: user},
		Activities: list,
	}, "activities/my_activities_body.html")
}

func (h *Handlers) renderMyActivities(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	list, err := h.Repo.ListPendingForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "activities/my_activities_body.html", myActivitiesData{
		PageData:   auth.PageData{CurrentUser: user},
		Activities: list,
	})
}

// NotifyDueActivities scans for pending activities due today that haven't
// been reminded about yet and notifies each assignee — same shape as
// projects.Handlers.NotifyDueTasks, meant to be called periodically by a
// ticker in main.go.
func (h *Handlers) NotifyDueActivities(ctx context.Context) {
	list, err := h.Repo.ListDueTodayUnnotified(ctx)
	if err != nil {
		log.Printf("activities: list due: %v", err)
		return
	}
	for _, a := range list {
		body := fmt.Sprintf("%s due today", a.KindLabel())
		if a.Note != "" {
			body = fmt.Sprintf("%s due today: %s", a.KindLabel(), a.Note)
		}
		if err := h.Notifications.Create(ctx, a.AssignedTo, "activity_due", body, activityLink(&a)); err != nil {
			log.Printf("activities: notify due %d: %v", a.ID, err)
			continue
		}
		if err := h.Repo.MarkReminderSent(ctx, a.ID); err != nil {
			log.Printf("activities: mark reminder sent %d: %v", a.ID, err)
		}
	}
}
