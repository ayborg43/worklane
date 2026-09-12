package chat

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/uploads"
	"github.com/sociolytik/odoo-clone/internal/web"
)

const recentMessageLimit = 50

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	// Same-origin app served from one host; no cross-site WS clients expected.
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Handlers struct {
	Repo           *Repo
	Users          *auth.Repo
	Renderer       *web.Renderer
	Hub            *Hub
	AttachmentsDir string
}

func NewHandlers(repo *Repo, users *auth.Repo, renderer *web.Renderer, hub *Hub, attachmentsDir string) *Handlers {
	return &Handlers{Repo: repo, Users: users, Renderer: renderer, Hub: hub, AttachmentsDir: attachmentsDir}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("GET /chat", mw.RequireAuth(http.HandlerFunc(h.Index)))
	mux.Handle("GET /chat/channels/{id}", mw.RequireAuth(http.HandlerFunc(h.ShowChannel)))
	mux.Handle("POST /chat/channels", mw.RequireAuth(http.HandlerFunc(h.CreateChannel)))
	mux.Handle("POST /chat/dm/{userID}", mw.RequireAuth(http.HandlerFunc(h.StartDM)))
	mux.Handle("GET /chat/ws/{channelID}", mw.RequireAuth(http.HandlerFunc(h.ServeWS)))
	mux.Handle("GET /chat/unread-badge", mw.RequireAuth(http.HandlerFunc(h.UnreadBadge)))
	mux.Handle("POST /chat/channels/{id}/attachments", mw.RequireAuth(http.HandlerFunc(h.UploadAttachment)))
	mux.Handle("GET /chat/messages/{id}/download", mw.RequireAuth(http.HandlerFunc(h.DownloadAttachment)))
}

// UploadAttachment saves the file, persists it as a chat message (with an
// optional caption), and broadcasts it over the same Hub every WS text
// message goes through — the requesting connection sees it via that
// broadcast too, so this handler itself returns no body for htmx to swap.
func (h *Handlers) UploadAttachment(w http.ResponseWriter, r *http.Request) {
	channelID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	isMember, err := h.Repo.IsMember(r.Context(), channelID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, uploads.MaxSize)
	if err := r.ParseMultipartForm(uploads.MaxSize); err != nil {
		http.Error(w, "file too large (max 10MB) or invalid upload", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "a file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	path, size, err := uploads.Save(h.AttachmentsDir, file, header)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	caption := strings.TrimSpace(r.FormValue("caption"))

	msg, err := h.Repo.InsertAttachmentMessage(r.Context(), channelID, user.ID, caption, header.Filename, contentType, size, path)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	msg.UserName = user.Name

	var buf bytes.Buffer
	if err := h.Renderer.RenderFragmentTo(&buf, "chat/message_row.html", msg); err != nil {
		log.Printf("chat: render attachment message: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Hub.Broadcast(channelID, buf.Bytes())

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handlers) DownloadAttachment(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	msg, err := h.Repo.GetMessage(r.Context(), id)
	if err != nil || !msg.HasAttachment() {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	isMember, err := h.Repo.IsMember(r.Context(), msg.ChannelID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", msg.AttachmentContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": msg.AttachmentFilename}))
	http.ServeFile(w, r, msg.AttachmentPath)
}

func (h *Handlers) UnreadBadge(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	total, err := h.Repo.TotalUnreadForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "chat/unread_badge.html", total)
}

type chatData struct {
	auth.PageData
	Channels      []Channel
	Users         []auth.User
	ActiveChannel *Channel
	Messages      []Message
}

func (h *Handlers) renderChat(w http.ResponseWriter, r *http.Request, activeChannelID int64) {
	user := auth.UserFromContext(r.Context())

	// Backfills membership into any channel created before this user
	// registered — otherwise a new user's Discuss page is permanently
	// empty, since CreateChannel only adds users who already existed at
	// the time a channel was made.
	if err := h.Repo.JoinAllChannels(r.Context(), user.ID); err != nil {
		log.Printf("chat: join all channels: %v", err)
	}

	// Mark-read happens before ListChannelsForUser so the just-opened
	// channel's own sidebar badge is already zero in this same response,
	// rather than waiting for the next unread-badge poll.
	if activeChannelID != 0 {
		isMember, err := h.Repo.IsMember(r.Context(), activeChannelID, user.ID)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !isMember {
			http.NotFound(w, r)
			return
		}
		if err := h.Repo.MarkRead(r.Context(), activeChannelID, user.ID); err != nil {
			log.Printf("chat: mark read: %v", err)
		}
	}

	channels, err := h.Repo.ListChannelsForUser(r.Context(), user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	users, err := h.Users.ListUsers(r.Context())
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	data := chatData{
		PageData: auth.PageData{CurrentUser: user},
		Channels: channels,
		Users:    users,
	}

	if activeChannelID != 0 {
		channel, err := h.Repo.GetChannel(r.Context(), activeChannelID, user.ID)
		if err != nil {
			h.handleLookupError(w, r, err)
			return
		}
		messages, err := h.Repo.ListRecentMessages(r.Context(), activeChannelID, recentMessageLimit)
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		data.ActiveChannel = channel
		data.Messages = messages
	}

	h.Renderer.Render(w, http.StatusOK, "chat/index.html", data, "chat/message_row.html")
}

func (h *Handlers) Index(w http.ResponseWriter, r *http.Request) {
	h.renderChat(w, r, 0)
}

func (h *Handlers) ShowChannel(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	h.renderChat(w, r, id)
}

func (h *Handlers) CreateChannel(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	user := auth.UserFromContext(r.Context())

	channel, err := h.Repo.CreateChannel(r.Context(), name, user.ID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			http.Error(w, "a channel with that name already exists", http.StatusConflict)
			return
		}
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/channels/%d", channel.ID), http.StatusFound)
}

func (h *Handlers) StartDM(w http.ResponseWriter, r *http.Request) {
	otherID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if otherID == user.ID {
		http.Error(w, "cannot start a DM with yourself", http.StatusBadRequest)
		return
	}

	channel, err := h.Repo.FindOrCreateDM(r.Context(), user.ID, otherID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/chat/channels/%d", channel.ID), http.StatusFound)
}

// ServeWS upgrades the connection only after re-checking membership at the
// handshake (auth.Middleware already ran, but that just confirms the user
// is logged in, not that they belong to this specific channel).
func (h *Handlers) ServeWS(w http.ResponseWriter, r *http.Request) {
	channelID, err := strconv.ParseInt(r.PathValue("channelID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())

	isMember, err := h.Repo.IsMember(r.Context(), channelID, user.ID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if !isMember {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("chat: ws upgrade: %v", err)
		return
	}

	client := &Client{
		hub:       h.Hub,
		conn:      conn,
		send:      make(chan []byte, 16),
		userID:    user.ID,
		channelID: channelID,
	}
	client.onMessage = func(body string) {
		msg, err := h.Repo.InsertMessage(context.Background(), channelID, user.ID, body)
		if err != nil {
			log.Printf("chat: insert message: %v", err)
			return
		}
		msg.UserName = user.Name

		var buf bytes.Buffer
		if err := h.Renderer.RenderFragmentTo(&buf, "chat/message_row.html", msg); err != nil {
			log.Printf("chat: render message: %v", err)
			return
		}
		h.Hub.Broadcast(channelID, buf.Bytes())
	}

	h.Hub.register <- registration{client: client, add: true}

	go client.writePump()
	client.readPump()
}

func (h *Handlers) handleLookupError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, "internal error", http.StatusInternalServerError)
}
