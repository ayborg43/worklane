package portal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/web"
)

const (
	magicLinkTTL = 1 * time.Hour
	sessionTTL   = 30 * 24 * time.Hour

	layout = "portal/layout.html"
)

// Mailer is satisfied structurally by *settings.Repo — same
// one-directional interface-not-import shape as notifications.Mailer.
type Mailer interface {
	Send(ctx context.Context, to, subject, body string) error
}

type Handlers struct {
	Repo         *Repo
	Mailer       Mailer
	Renderer     *web.Renderer
	BaseURL      string
	CookieSecure bool
}

func NewHandlers(repo *Repo, mailer Mailer, renderer *web.Renderer, baseURL string, cookieSecure bool) *Handlers {
	return &Handlers{Repo: repo, Mailer: mailer, Renderer: renderer, BaseURL: baseURL, CookieSecure: cookieSecure}
}

// MountRoutes takes both the main app's auth middleware (SendLink is
// triggered by a logged-in internal user from the CRM contacts page, so it
// rides the regular session) and the portal's own middleware (everything
// else rides the contact session instead).
func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware, portalMW *Middleware) {
	mux.Handle("POST /portal/send-link/{contactID}", mw.RequireAuth(http.HandlerFunc(h.SendLink)))

	mux.HandleFunc("GET /portal/login/{token}", h.Login)
	mux.HandleFunc("GET /portal/expired", h.Expired)
	mux.Handle("POST /portal/logout", portalMW.RequireContact(http.HandlerFunc(h.Logout)))
	mux.Handle("GET /portal", portalMW.RequireContact(http.HandlerFunc(h.Home)))
	mux.Handle("GET /portal/tickets/{id}", portalMW.RequireContact(http.HandlerFunc(h.ShowTicket)))
	mux.Handle("GET /portal/invoices/{id}", portalMW.RequireContact(http.HandlerFunc(h.ShowInvoice)))
}

// SendLink emails a fresh magic link to a contact — triggered from a small
// button on the CRM contacts page. Returns a short plain-text status
// swapped into a span next to the button, not a redirect: the contacts
// list itself doesn't need to change.
func (h *Handlers) SendLink(w http.ResponseWriter, r *http.Request) {
	contactID, err := strconv.ParseInt(r.PathValue("contactID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contact, err := h.Repo.GetContact(r.Context(), contactID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if contact.Email == "" {
		w.Write([]byte("This contact has no email address on file."))
		return
	}
	token, err := h.Repo.CreateMagicLink(r.Context(), contactID, magicLinkTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	link := h.BaseURL + "/portal/login/" + token
	body := fmt.Sprintf(
		"Hi %s,\n\nHere is your secure link to view your support tickets and invoices:\n\n%s\n\nThis link expires in 1 hour and can only be used once.",
		contact.Name, link)
	if err := h.Mailer.Send(r.Context(), contact.Email, "Your Worklane customer portal link", body); err != nil {
		w.Write([]byte("Couldn't send the email — check mail settings."))
		return
	}
	w.Write([]byte("Portal link sent to " + contact.Email + "."))
}

// Login redeems a one-time magic link and, on success, starts a long-lived
// portal session — the link itself is never reusable past this point.
func (h *Handlers) Login(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	contactID, err := h.Repo.RedeemMagicLink(r.Context(), token)
	if err != nil {
		http.Redirect(w, r, "/portal/expired", http.StatusFound)
		return
	}
	sessionToken, expiresAt, err := h.Repo.CreateSession(r.Context(), contactID, sessionTTL)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	// Path is scoped to /portal — this cookie is meaningless anywhere else
	// in the app and should never even be transmitted outside the portal.
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    sessionToken,
		Path:     "/portal",
		Expires:  expiresAt,
		HttpOnly: true,
		Secure:   h.CookieSecure,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/portal", http.StatusFound)
}

// expiredData exists only so layout.html's {{if .Contact}} check has a field
// to evaluate — the Expired page is reached precisely when there's no
// contact, so it's always nil here.
type expiredData struct {
	Contact *Contact
}

func (h *Handlers) Expired(w http.ResponseWriter, r *http.Request) {
	h.Renderer.RenderWithLayout(w, http.StatusOK, layout, "portal/expired.html", expiredData{})
}

func (h *Handlers) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(CookieName); err == nil {
		_ = h.Repo.DeleteSessionByToken(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: "", Path: "/portal", Expires: time.Unix(0, 0), MaxAge: -1,
		HttpOnly: true, Secure: h.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/portal/expired", http.StatusFound)
}

type homeData struct {
	Contact  *Contact
	Tickets  []Ticket
	Invoices []Invoice
}

func (h *Handlers) Home(w http.ResponseWriter, r *http.Request) {
	contact := ContactFromContext(r.Context())
	tickets, err := h.Repo.ListTicketsForContact(r.Context(), contact.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	invoices, err := h.Repo.ListInvoicesForContact(r.Context(), contact.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderWithLayout(w, http.StatusOK, layout, "portal/home.html", homeData{
		Contact: contact, Tickets: tickets, Invoices: invoices,
	})
}

type ticketData struct {
	Contact  *Contact
	Ticket   Ticket
	Comments []Comment
}

func (h *Handlers) ShowTicket(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contact := ContactFromContext(r.Context())
	ticket, err := h.Repo.GetTicketForContact(r.Context(), contact.ID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	comments, err := h.Repo.ListTicketComments(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderWithLayout(w, http.StatusOK, layout, "portal/ticket.html", ticketData{
		Contact: contact, Ticket: *ticket, Comments: comments,
	})
}

type invoiceData struct {
	Contact *Contact
	Invoice Invoice
}

func (h *Handlers) ShowInvoice(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contact := ContactFromContext(r.Context())
	invoice, err := h.Repo.GetInvoiceForContact(r.Context(), contact.ID, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderWithLayout(w, http.StatusOK, layout, "portal/invoice.html", invoiceData{
		Contact: contact, Invoice: *invoice,
	})
}
