package helpdesk

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

// Handlers has no membership checker — helpdesk is a shared, company-wide
// queue like CRM (not project-scoped), so every logged-in user can see and
// work every ticket. There's no per-record ACL to enforce here.
type Handlers struct {
	Repo          *Repo
	Users         *auth.Repo
	Notifications *notifications.Repo
	Renderer      *web.Renderer
}

func NewHandlers(repo *Repo, users *auth.Repo, notificationsRepo *notifications.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Users: users, Notifications: notificationsRepo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /helpdesk", mw.RequireAuth(http.HandlerFunc(h.Board)))
	mux.Handle("POST /helpdesk/tickets", mw.RequireAuth(http.HandlerFunc(h.Create)))
	mux.Handle("GET /helpdesk/tickets/{id}", mw.RequireAuth(http.HandlerFunc(h.Show)))
	mux.Handle("PUT /helpdesk/tickets/{id}", mw.RequireAuth(http.HandlerFunc(h.Update)))
	mux.Handle("DELETE /helpdesk/tickets/{id}", mw.RequireAuth(http.HandlerFunc(h.Delete)))
	mux.Handle("POST /helpdesk/tickets/{id}/comments", mw.RequireAuth(http.HandlerFunc(h.CreateComment)))
	mux.Handle("PUT /helpdesk/sla/{priority}", mw.RequireAuth(http.HandlerFunc(h.UpdateSLAPolicy)))
}

var ticketStatuses = []string{"open", "in_progress", "resolved"}

func groupTicketsByStatus(tickets []Ticket) map[string][]Ticket {
	groups := make(map[string][]Ticket, len(ticketStatuses))
	for _, s := range ticketStatuses {
		groups[s] = []Ticket{}
	}
	for _, t := range tickets {
		groups[t.Status] = append(groups[t.Status], t)
	}
	return groups
}

type boardData struct {
	auth.PageData
	TicketsByStatus map[string][]Ticket
	Contacts        []ContactOption
	Users           []auth.User
	SLAPolicies     []SLAPolicy
}

func (h *Handlers) loadBoardData(r *http.Request) (boardData, error) {
	tickets, err := h.Repo.ListAll(r.Context())
	if err != nil {
		return boardData{}, err
	}
	contacts, err := h.Repo.ListContacts(r.Context())
	if err != nil {
		return boardData{}, err
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		return boardData{}, err
	}
	policies, err := h.Repo.ListSLAPolicies(r.Context())
	if err != nil {
		return boardData{}, err
	}
	return boardData{
		PageData:        auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		TicketsByStatus: groupTicketsByStatus(tickets),
		Contacts:        contacts,
		Users:           users,
		SLAPolicies:     policies,
	}, nil
}

func (h *Handlers) Board(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadBoardData(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "helpdesk/index.html", data, "helpdesk/board_body.html")
}

// renderBoard re-renders just the #helpdesk-board fragment after a
// mutation (create/move/delete) — unlike Board, it never wraps the result
// in the full layout+nav.
func (h *Handlers) renderBoard(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadBoardData(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "helpdesk/board_body.html", data)
}

func parseTicketInput(r *http.Request) (TicketInput, error) {
	subject := strings.TrimSpace(r.FormValue("subject"))
	if subject == "" {
		return TicketInput{}, fmt.Errorf("subject is required")
	}
	var contactID int64
	var err error
	if v := r.FormValue("contact_id"); v != "" {
		contactID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return TicketInput{}, fmt.Errorf("invalid contact")
		}
	}
	var assigneeID int64
	if v := r.FormValue("assignee_id"); v != "" {
		assigneeID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return TicketInput{}, fmt.Errorf("invalid assignee")
		}
	}
	priority := r.FormValue("priority")
	if priority == "" {
		priority = "medium"
	}
	status := r.FormValue("status")
	if status == "" {
		status = "open"
	}
	return TicketInput{
		Subject:       subject,
		Description:   strings.TrimSpace(r.FormValue("description")),
		ContactID:     contactID,
		CustomerName:  strings.TrimSpace(r.FormValue("customer_name")),
		CustomerEmail: strings.TrimSpace(r.FormValue("customer_email")),
		AssigneeID:    assigneeID,
		Priority:      priority,
		Status:        status,
	}, nil
}

func (h *Handlers) Create(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	in, err := parseTicketInput(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ticket, err := h.Repo.Create(r.Context(), in, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.notifyIfAssigned(r, ticket.ID, 0, ticket.AssigneeID, ticket.Subject)
	h.renderBoard(w, r)
}

type showData struct {
	auth.PageData
	Ticket   Ticket
	Comments []Comment
	Contacts []ContactOption
	Users    []auth.User
}

func (h *Handlers) loadShowData(r *http.Request, id int64) (showData, error) {
	ticket, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		return showData{}, err
	}
	comments, err := h.Repo.ListComments(r.Context(), id)
	if err != nil {
		return showData{}, err
	}
	contacts, err := h.Repo.ListContacts(r.Context())
	if err != nil {
		return showData{}, err
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		return showData{}, err
	}
	return showData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Ticket:   *ticket,
		Comments: comments,
		Contacts: contacts,
		Users:    users,
	}, nil
}

func (h *Handlers) Show(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, err := h.loadShowData(r, id)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "helpdesk/ticket_detail.html", data,
		"helpdesk/ticket_detail_body.html", "helpdesk/comments_section.html")
}

// renderTicketDetailBody re-renders just the #ticket-detail fragment after
// a save from the edit form — unlike Show, it never wraps the result in
// the full layout+nav. The comments data it loads along the way goes
// unused by ticket_detail_body.html but costs little, and reusing
// loadShowData keeps this and Show from drifting apart.
func (h *Handlers) renderTicketDetailBody(w http.ResponseWriter, r *http.Request, id int64) {
	data, err := h.loadShowData(r, id)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "helpdesk/ticket_detail_body.html", data)
}

// Update overlays only the fields present in the submitted form onto the
// current record — same convention as projects.Handlers.UpdateTask, so the
// Kanban drag handler can PUT just status without clobbering the rest of
// the ticket. resolved_at is set the moment status first becomes
// "resolved" and cleared the moment it leaves that status.
func (h *Handlers) Update(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	current, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	in := TicketInput{
		Subject: current.Subject, Description: current.Description, ContactID: current.ContactID,
		CustomerName: current.CustomerName, CustomerEmail: current.CustomerEmail,
		AssigneeID: current.AssigneeID, Priority: current.Priority, Status: current.Status,
	}
	if r.Form.Has("subject") {
		if v := strings.TrimSpace(r.FormValue("subject")); v != "" {
			in.Subject = v
		}
	}
	if r.Form.Has("description") {
		in.Description = strings.TrimSpace(r.FormValue("description"))
	}
	if r.Form.Has("contact_id") {
		v := r.FormValue("contact_id")
		if v == "" {
			in.ContactID = 0
		} else if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.ContactID = id
		}
	}
	if r.Form.Has("customer_name") {
		in.CustomerName = strings.TrimSpace(r.FormValue("customer_name"))
	}
	if r.Form.Has("customer_email") {
		in.CustomerEmail = strings.TrimSpace(r.FormValue("customer_email"))
	}
	if r.Form.Has("assignee_id") {
		v := r.FormValue("assignee_id")
		if v == "" {
			in.AssigneeID = 0
		} else if id, err := strconv.ParseInt(v, 10, 64); err == nil {
			in.AssigneeID = id
		}
	}
	if r.Form.Has("priority") {
		if v := r.FormValue("priority"); v != "" {
			in.Priority = v
		}
	}
	if r.Form.Has("status") {
		if v := r.FormValue("status"); v != "" {
			in.Status = v
		}
	}

	var resolvedAt *time.Time
	switch {
	case in.Status == "resolved" && current.Status != "resolved":
		now := time.Now()
		resolvedAt = &now
	case in.Status != "resolved":
		resolvedAt = nil
	default:
		resolvedAt = current.ResolvedAt
	}

	if err := h.Repo.Update(r.Context(), id, in, resolvedAt); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.notifyIfAssigned(r, id, current.AssigneeID, in.AssigneeID, in.Subject)

	if r.URL.Query().Get("from") == "detail" {
		h.renderTicketDetailBody(w, r, id)
		return
	}
	h.renderBoard(w, r)
}

func (h *Handlers) Delete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.Delete(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("from") == "detail" {
		w.Header().Set("HX-Redirect", "/helpdesk")
		w.WriteHeader(http.StatusOK)
		return
	}
	h.renderBoard(w, r)
}

func (h *Handlers) CreateComment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := h.Repo.Get(r.Context(), id); err != nil {
		h.handleLookupError(w, r, err)
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
	if err := h.Repo.CreateComment(r.Context(), id, user.ID, body); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err := h.Repo.MarkFirstResponse(r.Context(), id); err != nil {
		log.Printf("helpdesk: mark first response: %v", err)
	}
	comments, err := h.Repo.ListComments(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "helpdesk/comments_section.html", comments)
}

var slaPriorities = map[string]bool{"low": true, "medium": true, "high": true}

// UpdateSLAPolicy re-renders the whole board (not just a small SLA-panel
// fragment) because changing a target can flip the Breached() status of
// every ticket at that priority — the Kanban cards need to reflect that
// immediately, not just the settings row that was edited.
func (h *Handlers) UpdateSLAPolicy(w http.ResponseWriter, r *http.Request) {
	priority := r.PathValue("priority")
	if !slaPriorities[priority] {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	responseHours, err := strconv.Atoi(r.FormValue("response_hours"))
	if err != nil || responseHours <= 0 {
		http.Error(w, "response hours must be a positive number", http.StatusBadRequest)
		return
	}
	resolutionHours, err := strconv.Atoi(r.FormValue("resolution_hours"))
	if err != nil || resolutionHours <= 0 {
		http.Error(w, "resolution hours must be a positive number", http.StatusBadRequest)
		return
	}
	if err := h.Repo.UpdateSLAPolicy(r.Context(), priority, responseHours, resolutionHours); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderBoard(w, r)
}

// notifyIfAssigned fires the same in-app+email pipeline task/opportunity
// assignment already use, whenever a ticket's assignee changes to someone
// other than themselves.
func (h *Handlers) notifyIfAssigned(r *http.Request, ticketID, previousAssignee, newAssignee int64, subject string) {
	if newAssignee == 0 || newAssignee == previousAssignee {
		return
	}
	user := auth.UserFromContext(r.Context())
	if newAssignee == user.ID {
		return
	}
	link := fmt.Sprintf("/helpdesk/tickets/%d", ticketID)
	body := fmt.Sprintf("You were assigned ticket %q", subject)
	if err := h.Notifications.Create(r.Context(), newAssignee, "ticket_assigned", body, link); err != nil {
		log.Printf("notifications: create: %v", err)
	}
}

func (h *Handlers) handleLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
