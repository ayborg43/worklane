package attachments

import (
	"context"
	"mime"
	"net/http"
	"strconv"

	"github.com/sociolytik/odoo-clone/internal/auth"
	"github.com/sociolytik/odoo-clone/internal/uploads"
	"github.com/sociolytik/odoo-clone/internal/web"
)

// MembershipChecker is satisfied structurally by *projects.Repo. Declaring
// it here (rather than importing projects) keeps the dependency
// one-directional: projects imports attachments for Attachment/*Repo, not
// the other way around.
type MembershipChecker interface {
	IsMember(ctx context.Context, projectID, userID int64) (bool, error)
}

type Handlers struct {
	Repo     *Repo
	Members  MembershipChecker
	Renderer *web.Renderer
	BaseDir  string
}

func NewHandlers(repo *Repo, members MembershipChecker, renderer *web.Renderer, baseDir string) *Handlers {
	return &Handlers{Repo: repo, Members: members, Renderer: renderer, BaseDir: baseDir}
}

func (h *Handlers) MountRoutes(mux *http.ServeMux, mw *auth.Middleware) {
	mux.Handle("POST /projects/{id}/attachments", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.UploadProject)))
	mux.Handle("POST /projects/{id}/tasks/{taskID}/attachments", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.UploadTask)))
	mux.Handle("GET /attachments/{attachmentID}/download", mw.RequireAuthAndModule("projects", http.HandlerFunc(h.Download)))
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

func (h *Handlers) UploadProject(w http.ResponseWriter, r *http.Request) {
	projectID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return
	}
	if _, err := h.saveUpload(w, r, projectID, 0, user.ID); err != nil {
		return
	}
	h.renderProjectFiles(w, r, projectID)
}

func (h *Handlers) UploadTask(w http.ResponseWriter, r *http.Request) {
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
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, projectID, user.ID) {
		return
	}
	if _, err := h.saveUpload(w, r, projectID, taskID, user.ID); err != nil {
		return
	}
	h.renderTaskFiles(w, r, taskID)
}

func (h *Handlers) Download(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("attachmentID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	att, err := h.Repo.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user := auth.UserFromContext(r.Context())
	if !h.checkMember(w, r, att.ProjectID, user.ID) {
		return
	}
	w.Header().Set("Content-Type", att.ContentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": att.Filename}))
	http.ServeFile(w, r, att.StoragePath)
}

// saveUpload enforces the size cap via http.MaxBytesReader, delegates the
// actual file-saving to the shared uploads package, and records the result
// in the DB.
func (h *Handlers) saveUpload(w http.ResponseWriter, r *http.Request, projectID, taskID, userID int64) (*Attachment, error) {
	r.Body = http.MaxBytesReader(w, r.Body, uploads.MaxSize)
	if err := r.ParseMultipartForm(uploads.MaxSize); err != nil {
		http.Error(w, "file too large (max 10MB) or invalid upload", http.StatusBadRequest)
		return nil, err
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "a file is required", http.StatusBadRequest)
		return nil, err
	}
	defer file.Close()

	storagePath, size, err := uploads.Save(h.BaseDir, file, header)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, err
	}

	contentType := header.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}

	att, err := h.Repo.Create(r.Context(), Attachment{
		ProjectID:   projectID,
		TaskID:      taskID,
		UploadedBy:  userID,
		Filename:    header.Filename,
		ContentType: contentType,
		SizeBytes:   size,
		StoragePath: storagePath,
	})
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return nil, err
	}
	return att, nil
}

func (h *Handlers) renderProjectFiles(w http.ResponseWriter, r *http.Request, projectID int64) {
	files, err := h.Repo.ListForProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "attachments/project_files.html", files)
}

func (h *Handlers) renderTaskFiles(w http.ResponseWriter, r *http.Request, taskID int64) {
	files, err := h.Repo.ListForTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Renderer.RenderFragment(w, http.StatusOK, "attachments/task_files.html", files)
}
