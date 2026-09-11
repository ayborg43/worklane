package crm

import (
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
	Users         *auth.Repo
	Notifications *notifications.Repo
	Renderer      *web.Renderer
}

func NewHandlers(repo *Repo, users *auth.Repo, notificationsRepo *notifications.Repo, renderer *web.Renderer) *Handlers {
	return &Handlers{Repo: repo, Users: users, Notifications: notificationsRepo, Renderer: renderer}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /crm", mw.RequireAuth(http.HandlerFunc(h.Pipeline)))
	mux.Handle("POST /crm/opportunities", mw.RequireAuth(http.HandlerFunc(h.CreateOpportunity)))
	mux.Handle("GET /crm/opportunities/{id}", mw.RequireAuth(http.HandlerFunc(h.ShowOpportunity)))
	mux.Handle("PUT /crm/opportunities/{id}", mw.RequireAuth(http.HandlerFunc(h.UpdateOpportunity)))
	mux.Handle("DELETE /crm/opportunities/{id}", mw.RequireAuth(http.HandlerFunc(h.DeleteOpportunity)))
	mux.Handle("POST /crm/opportunities/{id}/won", mw.RequireAuth(http.HandlerFunc(h.MarkWon)))
	mux.Handle("POST /crm/opportunities/{id}/lost", mw.RequireAuth(http.HandlerFunc(h.MarkLost)))

	mux.Handle("GET /crm/contacts", mw.RequireAuth(http.HandlerFunc(h.ListContacts)))
	mux.Handle("POST /crm/contacts", mw.RequireAuth(http.HandlerFunc(h.CreateContact)))
	mux.Handle("PUT /crm/contacts/{id}", mw.RequireAuth(http.HandlerFunc(h.UpdateContact)))
	mux.Handle("DELETE /crm/contacts/{id}", mw.RequireAuth(http.HandlerFunc(h.DeleteContact)))

	mux.Handle("POST /crm/stages", mw.RequireAuth(http.HandlerFunc(h.CreateStage)))
	mux.Handle("DELETE /crm/stages/{id}", mw.RequireAuth(http.HandlerFunc(h.DeleteStage)))
	mux.Handle("POST /crm/stages/{id}/move-up", mw.RequireAuth(http.HandlerFunc(h.MoveStageUp)))
	mux.Handle("POST /crm/stages/{id}/move-down", mw.RequireAuth(http.HandlerFunc(h.MoveStageDown)))
}

func (h *Handlers) handleLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if err == ErrNotFound {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}

// ---------- pipeline (Kanban board) ----------

type pipelineData struct {
	auth.PageData
	Stages               []Stage
	OpportunitiesByStage map[int64][]Opportunity
	StageTotals          map[int64]float64
	Contacts             []Contact
	Users                []auth.User
	ShowLost             bool
	LostOpportunities    []Opportunity
}

func (h *Handlers) loadPipelineData(r *http.Request) (pipelineData, error) {
	showLost := r.URL.Query().Get("lost") == "1"

	stages, err := h.Repo.ListStages(r.Context())
	if err != nil {
		return pipelineData{}, err
	}
	opps, err := h.Repo.ListPipeline(r.Context(), false)
	if err != nil {
		return pipelineData{}, err
	}
	totals, err := h.Repo.PipelineValueByStage(r.Context())
	if err != nil {
		return pipelineData{}, err
	}
	contacts, err := h.Repo.ListContacts(r.Context())
	if err != nil {
		return pipelineData{}, err
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		return pipelineData{}, err
	}

	byStage := make(map[int64][]Opportunity, len(stages))
	for _, o := range opps {
		byStage[o.StageID] = append(byStage[o.StageID], o)
	}

	var lost []Opportunity
	if showLost {
		all, err := h.Repo.ListPipeline(r.Context(), true)
		if err != nil {
			return pipelineData{}, err
		}
		for _, o := range all {
			if o.IsLost {
				lost = append(lost, o)
			}
		}
	}

	return pipelineData{
		PageData:             auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Stages:               stages,
		OpportunitiesByStage: byStage,
		StageTotals:          totals,
		Contacts:             contacts,
		Users:                users,
		ShowLost:             showLost,
		LostOpportunities:    lost,
	}, nil
}

// Pipeline renders the full /crm page (layout + nav + pipeline_body.html).
func (h *Handlers) Pipeline(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadPipelineData(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "crm/pipeline.html", data, "crm/pipeline_body.html")
}

// ---------- opportunities ----------

func parseOpportunityInput(r *http.Request) (OpportunityInput, error) {
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return OpportunityInput{}, fmt.Errorf("name is required")
	}
	stageID, err := strconv.ParseInt(r.FormValue("stage_id"), 10, 64)
	if err != nil {
		return OpportunityInput{}, fmt.Errorf("invalid stage")
	}
	ownerID, err := strconv.ParseInt(r.FormValue("owner_id"), 10, 64)
	if err != nil {
		return OpportunityInput{}, fmt.Errorf("invalid owner")
	}
	var contactID int64
	if v := r.FormValue("contact_id"); v != "" {
		contactID, err = strconv.ParseInt(v, 10, 64)
		if err != nil {
			return OpportunityInput{}, fmt.Errorf("invalid contact")
		}
	}
	var value float64
	if v := r.FormValue("value_amount"); v != "" {
		value, err = strconv.ParseFloat(v, 64)
		if err != nil {
			return OpportunityInput{}, fmt.Errorf("invalid value")
		}
	}
	var closeDate *time.Time
	if v := r.FormValue("expected_close_date"); v != "" {
		d, err := time.Parse("2006-01-02", v)
		if err != nil {
			return OpportunityInput{}, fmt.Errorf("invalid close date")
		}
		closeDate = &d
	}
	return OpportunityInput{
		Name: name, ContactID: contactID, StageID: stageID, OwnerID: ownerID,
		ValueAmount: value, ExpectedCloseDate: closeDate,
	}, nil
}

func (h *Handlers) CreateOpportunity(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	in, err := parseOpportunityInput(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	opp, err := h.Repo.CreateOpportunity(r.Context(), in, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.notifyIfReassigned(r, opp.ID, 0, opp.OwnerID, opp.Name)
	h.renderPipeline(w, r)
}

func (h *Handlers) ShowOpportunity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	opp, err := h.Repo.GetOpportunity(r.Context(), id)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	stages, err := h.Repo.ListStages(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	contacts, err := h.Repo.ListContacts(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "crm/opportunity_detail.html", opportunityDetailData{
		PageData:    auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Opportunity: *opp,
		Stages:      stages,
		Contacts:    contacts,
		Users:       users,
	})
}

type opportunityDetailData struct {
	auth.PageData
	Opportunity Opportunity
	Stages      []Stage
	Contacts    []Contact
	Users       []auth.User
}

// UpdateOpportunity overlays only the fields present in the submitted
// form onto the current record — same convention as
// projects.Handlers.UpdateTask, so the Kanban drag handler can PUT just
// stage_id without clobbering the rest of the deal.
func (h *Handlers) UpdateOpportunity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	current, err := h.Repo.GetOpportunity(r.Context(), id)
	if err != nil {
		h.handleLookupError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}

	in := OpportunityInput{
		Name: current.Name, ContactID: current.ContactID, StageID: current.StageID,
		OwnerID: current.OwnerID, ValueAmount: current.ValueAmount, ExpectedCloseDate: current.ExpectedCloseDate,
	}
	if r.Form.Has("name") {
		if v := strings.TrimSpace(r.FormValue("name")); v != "" {
			in.Name = v
		}
	}
	if r.Form.Has("stage_id") {
		stageID, err := strconv.ParseInt(r.FormValue("stage_id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid stage", http.StatusBadRequest)
			return
		}
		in.StageID = stageID
	}
	if r.Form.Has("owner_id") {
		ownerID, err := strconv.ParseInt(r.FormValue("owner_id"), 10, 64)
		if err != nil {
			http.Error(w, "invalid owner", http.StatusBadRequest)
			return
		}
		in.OwnerID = ownerID
	}
	if r.Form.Has("contact_id") {
		v := r.FormValue("contact_id")
		if v == "" {
			in.ContactID = 0
		} else {
			contactID, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "invalid contact", http.StatusBadRequest)
				return
			}
			in.ContactID = contactID
		}
	}
	if r.Form.Has("value_amount") {
		v := r.FormValue("value_amount")
		if v == "" {
			in.ValueAmount = 0
		} else {
			value, err := strconv.ParseFloat(v, 64)
			if err != nil {
				http.Error(w, "invalid value", http.StatusBadRequest)
				return
			}
			in.ValueAmount = value
		}
	}
	if r.Form.Has("expected_close_date") {
		v := r.FormValue("expected_close_date")
		if v == "" {
			in.ExpectedCloseDate = nil
		} else {
			d, err := time.Parse("2006-01-02", v)
			if err != nil {
				http.Error(w, "invalid close date", http.StatusBadRequest)
				return
			}
			in.ExpectedCloseDate = &d
		}
	}

	if err := h.Repo.UpdateOpportunity(r.Context(), id, in); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.notifyIfReassigned(r, id, current.OwnerID, in.OwnerID, in.Name)

	// The detail page and the Kanban board post to the same endpoint (the
	// detail page edits fields; the Kanban drag moves stage_id) — a
	// "from" hidden field tells this handler which fragment to send back,
	// the same disambiguation activities.Handlers uses for its
	// My-Activities-vs-project-page mark-done button.
	if r.URL.Query().Get("from") == "detail" {
		h.renderOpportunityDetail(w, r)
		return
	}
	h.renderPipeline(w, r)
}

func (h *Handlers) DeleteOpportunity(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.DeleteOpportunity(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Deleting from the detail page leaves nothing on that page worth
	// re-rendering — send the client back to the pipeline instead of a
	// fragment swap, the same HX-Redirect convention auth.Middleware uses.
	if r.URL.Query().Get("from") == "detail" {
		w.Header().Set("HX-Redirect", "/crm")
		w.WriteHeader(http.StatusOK)
		return
	}
	h.renderPipeline(w, r)
}

func (h *Handlers) MarkWon(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stages, err := h.Repo.ListStages(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	var wonStageID int64
	for _, s := range stages {
		if s.IsWon {
			wonStageID = s.ID
			break
		}
	}
	if wonStageID == 0 {
		http.Error(w, "no stage is marked as the won stage", http.StatusBadRequest)
		return
	}
	if err := h.Repo.MarkWon(r.Context(), id, wonStageID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("from") == "detail" {
		h.renderOpportunityDetail(w, r)
		return
	}
	h.renderPipeline(w, r)
}

func (h *Handlers) MarkLost(w http.ResponseWriter, r *http.Request) {
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
	if err := h.Repo.MarkLost(r.Context(), id, reason); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("from") == "detail" {
		h.renderOpportunityDetail(w, r)
		return
	}
	h.renderPipeline(w, r)
}

// notifyIfReassigned fires the same in-app+email pipeline task assignment
// already uses, whenever an opportunity's owner changes to someone other
// than themselves.
func (h *Handlers) notifyIfReassigned(r *http.Request, oppID, previousOwner, newOwner int64, name string) {
	if newOwner == 0 || newOwner == previousOwner {
		return
	}
	user := auth.UserFromContext(r.Context())
	if newOwner == user.ID {
		return
	}
	link := fmt.Sprintf("/crm/opportunities/%d", oppID)
	body := fmt.Sprintf("You were assigned the opportunity %q", name)
	if err := h.Notifications.Create(r.Context(), newOwner, "opportunity_assigned", body, link); err != nil {
		log.Printf("notifications: create: %v", err)
	}
}

// renderPipeline re-renders just the #crm-page fragment after a mutation
// (opportunity create/move/delete/won/lost, stage add/delete/reorder) —
// unlike Pipeline, it never wraps the result in the full layout+nav.
func (h *Handlers) renderPipeline(w http.ResponseWriter, r *http.Request) {
	data, err := h.loadPipelineData(r)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "crm/pipeline_body.html", data)
}

// renderOpportunityDetail re-renders the detail page for the current
// request. It doesn't need id explicitly — r.PathValue("id") already holds
// it, since PUT/POST /crm/opportunities/{id} and GET .../{id} bind the
// same path parameter regardless of method.
func (h *Handlers) renderOpportunityDetail(w http.ResponseWriter, r *http.Request) {
	h.ShowOpportunity(w, r)
}

// ---------- contacts ----------

func (h *Handlers) ListContacts(w http.ResponseWriter, r *http.Request) {
	contacts, err := h.Repo.ListContacts(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.Render(w, http.StatusOK, "crm/contacts.html", contactsData{
		PageData: auth.PageData{CurrentUser: auth.UserFromContext(r.Context())},
		Contacts: contacts,
	}, "crm/contacts_body.html")
}

type contactsData struct {
	auth.PageData
	Contacts []Contact
}

func (h *Handlers) CreateContact(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	company := strings.TrimSpace(r.FormValue("company"))
	email := strings.TrimSpace(r.FormValue("email"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	if _, err := h.Repo.CreateContact(r.Context(), name, company, email, phone, user.ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.ListContactsFragment(w, r)
}

func (h *Handlers) UpdateContact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
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
	company := strings.TrimSpace(r.FormValue("company"))
	email := strings.TrimSpace(r.FormValue("email"))
	phone := strings.TrimSpace(r.FormValue("phone"))
	if err := h.Repo.UpdateContact(r.Context(), id, name, company, email, phone); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.ListContactsFragment(w, r)
}

func (h *Handlers) DeleteContact(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Repo.DeleteContact(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.ListContactsFragment(w, r)
}

func (h *Handlers) ListContactsFragment(w http.ResponseWriter, r *http.Request) {
	contacts, err := h.Repo.ListContacts(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "crm/contacts_body.html", contacts)
}

// ---------- stages ----------

func (h *Handlers) CreateStage(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	if _, err := h.Repo.CreateStage(r.Context(), name); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderPipeline(w, r)
}

func (h *Handlers) DeleteStage(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	count, err := h.Repo.CountOpportunitiesInStage(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if count > 0 {
		http.Error(w, "move or delete every opportunity in this stage first", http.StatusConflict)
		return
	}
	if err := h.Repo.DeleteStage(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderPipeline(w, r)
}

func (h *Handlers) MoveStageUp(w http.ResponseWriter, r *http.Request) {
	h.swapStage(w, r, -1)
}

func (h *Handlers) MoveStageDown(w http.ResponseWriter, r *http.Request) {
	h.swapStage(w, r, 1)
}

func (h *Handlers) swapStage(w http.ResponseWriter, r *http.Request, direction int) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	stages, err := h.Repo.ListStages(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	idx := -1
	for i, s := range stages {
		if s.ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		http.NotFound(w, r)
		return
	}
	neighbor := idx + direction
	if neighbor < 0 || neighbor >= len(stages) {
		h.renderPipeline(w, r)
		return
	}
	if err := h.Repo.SwapStageOrder(r.Context(), stages[idx].ID, stages[neighbor].ID); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.renderPipeline(w, r)
}
