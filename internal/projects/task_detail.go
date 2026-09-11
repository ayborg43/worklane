package projects

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/sociolytik/odoo-clone/internal/activities"
	"github.com/sociolytik/odoo-clone/internal/attachments"
	"github.com/sociolytik/odoo-clone/internal/auth"
)

type taskDetailData struct {
	auth.PageData
	Project    Project
	Task       Task
	Comments   []Comment
	Users      []auth.User
	Files      []attachments.Attachment
	ProjectID  int64
	TaskID     int64
	Members    []auth.User
	Activities []activities.Activity
}

func (h *Handlers) ShowTask(w http.ResponseWriter, r *http.Request) {
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
	project, ok := h.requireMember(w, r, projectID)
	if !ok {
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
	comments, err := h.Repo.ListComments(r.Context(), taskID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	files, err := h.Attachments.ListForTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	members, err := h.Repo.ListMembers(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	taskActivities, err := h.Activities.ListForTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	h.Renderer.Render(w, http.StatusOK, "projects/task_detail.html", taskDetailData{
		PageData:   auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Project:    *project,
		Task:       *task,
		Comments:   comments,
		Users:      users,
		Files:      files,
		ProjectID:  projectID,
		TaskID:     taskID,
		Members:    members,
		Activities: taskActivities,
	}, "projects/comments_section.html", "attachments/task_files.html", "activities/task_activities.html")
}

func (h *Handlers) CreateComment(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	body := strings.TrimSpace(r.FormValue("body"))
	if body == "" {
		http.Error(w, "comment cannot be empty", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())
	if _, err := h.Repo.CreateComment(r.Context(), taskID, user.ID, body); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	if task.AssigneeID != 0 && task.AssigneeID != user.ID {
		link := fmt.Sprintf("/projects/%d/tasks/%d", projectID, taskID)
		notifBody := fmt.Sprintf("%s commented on task %q", user.Name, task.Name)
		if err := h.Notifications.Create(r.Context(), task.AssigneeID, "task_comment", notifBody, link); err != nil {
			log.Printf("notifications: create: %v", err)
		}
	}

	h.renderComments(w, r, taskID)
}

func (h *Handlers) renderComments(w http.ResponseWriter, r *http.Request, taskID int64) {
	comments, err := h.Repo.ListComments(r.Context(), taskID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "projects/comments_section.html", comments)
}
