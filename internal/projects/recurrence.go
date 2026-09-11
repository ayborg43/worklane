package projects

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/sociolytik/odoo-clone/internal/auth"
)

// subtasksSectionData backs the standalone subtasks-section fragment
// re-render after creating a subtask. It uses the same field names
// (ProjectID, TaskID, Subtasks, Users) taskDetailData also exposes, so
// subtasks_section.html renders correctly whether embedded in the full
// task page or invoked here — same field-name-reflection trick
// activities.sectionData uses to share templates across packages, applied
// here within the same package for the same reason: two different call
// sites, one template.
type subtasksSectionData struct {
	ProjectID int64
	TaskID    int64
	Subtasks  []Task
	Users     []auth.User
}

func (h *Handlers) CreateSubtask(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	parentID, err := strconv.ParseInt(r.PathValue("taskID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, ok := h.requireMember(w, r, projectID); !ok {
		return
	}
	parent, err := h.Repo.GetTask(r.Context(), parentID)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if parent.ProjectID != projectID {
		http.NotFound(w, r)
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
	in.ParentTaskID = parentID

	if _, err := h.Repo.CreateTask(r.Context(), projectID, in); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderSubtasks(w, r, parentID)
}

func (h *Handlers) renderSubtasks(w http.ResponseWriter, r *http.Request, parentID int64) {
	parent, err := h.Repo.GetTask(r.Context(), parentID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	subtasks, err := h.Repo.ListSubtasks(r.Context(), parentID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "projects/subtasks_section.html", subtasksSectionData{
		ProjectID: parent.ProjectID,
		TaskID:    parentID,
		Subtasks:  subtasks,
		Users:     users,
	})
}

// recurrenceSectionData mirrors subtasksSectionData's cross-call-site
// field-name-matching rationale, for task_recurrence_section.html.
type recurrenceSectionData struct {
	ProjectID int64
	TaskID    int64
	Task      Task
}

var validRecurrenceUnits = map[string]bool{"day": true, "week": true, "month": true}

func addRecurrenceInterval(t time.Time, unit string, interval int) time.Time {
	switch unit {
	case "day":
		return t.AddDate(0, 0, interval)
	case "week":
		return t.AddDate(0, 0, 7*interval)
	case "month":
		return t.AddDate(0, interval, 0)
	default:
		return t
	}
}

// SetRecurrence replaces a task's recurrence settings — a dedicated
// endpoint (rather than folding into UpdateTask's field-overlay form)
// because UpdateTask always re-renders project_body.html, which isn't
// present on the task detail page this form lives on.
func (h *Handlers) SetRecurrence(w http.ResponseWriter, r *http.Request) {
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

	unit := r.FormValue("unit")
	var interval int
	var until *time.Time
	if unit != "" {
		if !validRecurrenceUnits[unit] {
			http.Error(w, "invalid recurrence unit", http.StatusBadRequest)
			return
		}
		interval, err = strconv.Atoi(r.FormValue("interval"))
		if err != nil || interval < 1 {
			http.Error(w, "invalid interval", http.StatusBadRequest)
			return
		}
		if v := r.FormValue("until"); v != "" {
			d, err := time.Parse("2006-01-02", v)
			if err != nil {
				http.Error(w, "invalid until date", http.StatusBadRequest)
				return
			}
			until = &d
		}
	}
	if err := h.Repo.SetRecurrence(r.Context(), taskID, unit, interval, until); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderTaskRecurrence(w, r, taskID)
}

func (h *Handlers) renderTaskRecurrence(w http.ResponseWriter, r *http.Request, taskID int64) {
	task, err := h.Repo.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "projects/task_recurrence_section.html", recurrenceSectionData{
		ProjectID: task.ProjectID,
		TaskID:    task.ID,
		Task:      *task,
	})
}

// spawnNextRecurrence creates the next occurrence of a task that was just
// marked done, if it's set to recur and hasn't reached its optional end
// date. Called from UpdateTask right after a successful save; failures are
// only logged — a recurrence hiccup must never fail the status update that
// triggered it, same posture as the notification calls alongside it.
func (h *Handlers) spawnNextRecurrence(ctx context.Context, taskID, projectID int64, previous Task, saved TaskInput) {
	if saved.Status != "done" || previous.Status == "done" || previous.RecurrenceUnit == "" {
		return
	}
	nextStart := addRecurrenceInterval(saved.StartDate, previous.RecurrenceUnit, previous.RecurrenceInterval)
	nextEnd := addRecurrenceInterval(saved.EndDate, previous.RecurrenceUnit, previous.RecurrenceInterval)
	if previous.RecurrenceUntil != nil && nextStart.After(*previous.RecurrenceUntil) {
		return
	}

	next, err := h.Repo.CreateTask(ctx, projectID, TaskInput{
		Name: saved.Name, Description: saved.Description, AssigneeID: saved.AssigneeID,
		StartDate: nextStart, EndDate: nextEnd, Progress: 0, Status: "todo",
		ParentTaskID: previous.ParentTaskID,
	})
	if err != nil {
		log.Printf("projects: create next recurrence for task %d: %v", taskID, err)
		return
	}
	if err := h.Repo.SetRecurrence(ctx, next.ID, previous.RecurrenceUnit, previous.RecurrenceInterval, previous.RecurrenceUntil); err != nil {
		log.Printf("projects: carry recurrence forward to task %d: %v", next.ID, err)
	}
}
