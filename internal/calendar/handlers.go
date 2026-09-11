package calendar

import (
	"fmt"
	"net/http"
	"time"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/projects"
	"github.com/sociolytik/odoo-clone/internal/timesheets"
	"github.com/sociolytik/odoo-clone/internal/web"
)

// Handlers aggregates read-only data from projects (task due dates,
// milestones) and timesheets (logged entries) into a single month view. It
// never writes to either — creating/editing the underlying tasks,
// milestones, and timesheet entries stays on their own pages.
type Handlers struct {
	Projects   *projects.Repo
	Timesheets *timesheets.Repo
	Renderer   *web.Renderer
}

func NewHandlers(projectsRepo *projects.Repo, timesheetsRepo *timesheets.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Projects: projectsRepo, Timesheets: timesheetsRepo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /calendar", mw.RequireAuth(http.HandlerFunc(h.Show)))
}

type event struct {
	Kind  string // "task", "milestone", "timesheet" — used as a CSS modifier class
	Label string
	Link  string
}

type day struct {
	Date    time.Time
	InMonth bool
	IsToday bool
	Events  []event
}

type pageData struct {
	auth.PageData
	MonthLabel string
	PrevMonth  string
	NextMonth  string
	Weeks      [][]day
}

// mondayOf returns midnight UTC on the Monday of the week containing t —
// same convention timesheets.mondayOf uses; duplicated locally since that
// one is unexported and this is the only other place that needs it.
func mondayOf(t time.Time) time.Time {
	t = time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	offset := (int(t.Weekday()) + 6) % 7 // Monday=0 .. Sunday=6
	return t.AddDate(0, 0, -offset)
}

func monthStartFromRequest(r *http.Request) time.Time {
	if v := r.URL.Query().Get("month"); v != "" {
		if d, err := time.Parse("2006-01", v); err == nil {
			return time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC)
		}
	}
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func (h *Handlers) Show(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	monthStart := monthStartFromRequest(r)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// The grid covers complete Monday-Sunday weeks: from the Monday on/before
	// the 1st through the Sunday on/after the last day of the month.
	gridStart := mondayOf(monthStart)
	gridEnd := mondayOf(monthEnd.AddDate(0, 0, -1)).AddDate(0, 0, 7)

	tasks, err := h.Projects.ListTasksDueBetween(r.Context(), user.ID, gridStart, gridEnd)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	milestones, err := h.Projects.ListMilestonesBetween(r.Context(), user.ID, gridStart, gridEnd)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	entries, err := h.Timesheets.ListForUserWeek(r.Context(), user.ID, gridStart, gridEnd)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	events := make(map[string][]event)
	for _, t := range tasks {
		key := t.EndDate.Format("2006-01-02")
		events[key] = append(events[key], event{
			Kind:  "task",
			Label: t.Name,
			Link:  fmt.Sprintf("/projects/%d/tasks/%d", t.ProjectID, t.ID),
		})
	}
	for _, m := range milestones {
		key := m.Date.Format("2006-01-02")
		events[key] = append(events[key], event{
			Kind:  "milestone",
			Label: fmt.Sprintf("%s (%s)", m.Name, m.ProjectName),
			Link:  fmt.Sprintf("/projects/%d", m.ProjectID),
		})
	}
	for _, e := range entries {
		key := e.WorkDate.Format("2006-01-02")
		events[key] = append(events[key], event{
			Kind:  "timesheet",
			Label: fmt.Sprintf("%.1fh logged", e.Hours),
			Link:  "/timesheets?week=" + mondayOf(e.WorkDate).Format("2006-01-02"),
		})
	}

	todayKey := time.Now().UTC().Format("2006-01-02")

	var weeks [][]day
	for weekStart := gridStart; weekStart.Before(gridEnd); weekStart = weekStart.AddDate(0, 0, 7) {
		var week []day
		for i := 0; i < 7; i++ {
			date := weekStart.AddDate(0, 0, i)
			key := date.Format("2006-01-02")
			week = append(week, day{
				Date:    date,
				InMonth: date.Month() == monthStart.Month() && date.Year() == monthStart.Year(),
				IsToday: key == todayKey,
				Events:  events[key],
			})
		}
		weeks = append(weeks, week)
	}

	h.Renderer.Render(w, http.StatusOK, "calendar/index.html", pageData{
		PageData:   auth.PageData{CurrentUser: user},
		MonthLabel: monthStart.Format("January 2006"),
		PrevMonth:  monthStart.AddDate(0, -1, 0).Format("2006-01"),
		NextMonth:  monthStart.AddDate(0, 1, 0).Format("2006-01"),
		Weeks:      weeks,
	})
}
