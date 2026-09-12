package ai

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/sociolytik/odoo-clone/internal/auth"
)

type Handlers struct {
	Repo *Repo
}

func NewHandlers(repo *Repo) *Handlers {
	return &Handlers{Repo: repo}
}

// MountRoutes only wires up the everyday-use endpoint — any logged-in user
// can rephrase text, same as any other page action. Configuring *which*
// provider that hits (base URL/key/model) is an admin-only action that
// lives in internal/settings instead, alongside Mail and User Access.
func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("POST /ai/rephrase", mw.RequireAuth(http.HandlerFunc(h.Rephrase)))
}

type rephraseRequest struct {
	Text string `json:"text"`
}

type rephraseResponse struct {
	Text string `json:"text"`
}

func (h *Handlers) Rephrase(w http.ResponseWriter, r *http.Request) {
	var req rephraseRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	text := strings.TrimSpace(req.Text)
	if text == "" {
		http.Error(w, "text is required", http.StatusBadRequest)
		return
	}

	result, err := h.Repo.Rephrase(r.Context(), text)
	if err != nil {
		if errors.Is(err, ErrNotConfigured) {
			http.Error(w, "AI isn't configured yet — ask an admin to set it up in Settings.", http.StatusServiceUnavailable)
			return
		}
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(rephraseResponse{Text: result})
}
