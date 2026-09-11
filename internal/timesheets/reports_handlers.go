package timesheets

import (
	"net/http"

	"github.com/sociolytik/odoo-clone/internal/auth"
)

type reportsData struct {
	auth.PageData
	ByProject []ProjectHours
	ByPerson  []PersonHours
	ByWeek    []WeekHours
}

func (h *Handlers) Reports(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())

	byProject, err := h.Repo.HoursByProject(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	byPerson, err := h.Repo.HoursByPerson(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	byWeek, err := h.Repo.HoursByWeek(r.Context(), user.ID, 8)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	setProjectPercents(byProject)
	setPersonPercents(byPerson)
	setWeekPercents(byWeek)

	h.Renderer.Render(w, http.StatusOK, "timesheets/reports.html", reportsData{
		PageData:  auth.PageData{CurrentUser: user},
		ByProject: byProject,
		ByPerson:  byPerson,
		ByWeek:    byWeek,
	})
}

// setXPercents fills each row's Pct relative to that slice's own max hours,
// for CSS bar widths — computed here rather than as a template func, since
// web.Renderer registers none today and this avoids adding any.
func setProjectPercents(rows []ProjectHours) {
	max := 0.0
	for _, r := range rows {
		if r.Hours > max {
			max = r.Hours
		}
	}
	for i := range rows {
		if max > 0 {
			rows[i].Pct = int(rows[i].Hours / max * 100)
		}
	}
}

func setPersonPercents(rows []PersonHours) {
	max := 0.0
	for _, r := range rows {
		if r.Hours > max {
			max = r.Hours
		}
	}
	for i := range rows {
		if max > 0 {
			rows[i].Pct = int(rows[i].Hours / max * 100)
		}
	}
}

func setWeekPercents(rows []WeekHours) {
	max := 0.0
	for _, r := range rows {
		if r.Hours > max {
			max = r.Hours
		}
	}
	for i := range rows {
		if max > 0 {
			rows[i].Pct = int(rows[i].Hours / max * 100)
		}
	}
}
