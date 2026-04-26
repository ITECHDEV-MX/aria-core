// Page attachments + share links HTTP endpoints.
//
// Two surface areas live in this file:
//
//   /v1/pages/{pageID}/attachments       (POST multipart, GET list)
//   /v1/attachments/{id}                 (GET metadata)
//   /v1/attachments/{id}/download        (GET streaming binary)
//   /v1/attachments/{id}/thumbnail       (GET cached preview)
//   /v1/attachments/{id}                 (DELETE soft-delete)
//   /v1/pages/{pageID}/share             (POST create link)
//   /v1/pages/{pageID}/shares            (GET list active)
//   /v1/shares/{id}                      (DELETE revoke)
//
// All of the above are JWT-bound. The PUBLIC route /p/{token} lives separately
// in cloudserver.routes() and is mounted OUTSIDE /dashboard so it bypasses
// session auth.
package cloudserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/attachments"
)

// PageAttachmentService is the runtime contract used by /v1/pages/* + /v1/attachments/*.
// Implemented by *attachments.AttachmentStore via a thin adapter in cmd/aria-core.
type PageAttachmentService interface {
	Upload(ctx context.Context, p attachments.UploadParams) (*attachments.Attachment, error)
	Get(ctx context.Context, id string) (*attachments.Attachment, error)
	ListByPage(ctx context.Context, pageID string) ([]*attachments.Attachment, error)
	OpenContent(att *attachments.Attachment) (io.ReadCloser, int64, error)
	OpenThumbnail(att *attachments.Attachment) (io.ReadCloser, int64, error)
	Delete(ctx context.Context, id, byUID string) error
	MaxBytes() int64
}

// PageShareService is the runtime contract for share links.
type PageShareService interface {
	Create(ctx context.Context, p attachments.CreateShareParams) (*attachments.ShareLink, error)
	Get(ctx context.Context, id string) (*attachments.ShareLink, error)
	GetByToken(ctx context.Context, token string) (*attachments.ShareLink, error)
	ListByPage(ctx context.Context, pageID string, includeRevoked bool) ([]*attachments.ShareLink, error)
	Revoke(ctx context.Context, id, byUID string) error
	Resolve(ctx context.Context, token, password, ip, ua string) (*attachments.ShareLink, error)
}

// PagePublicViewService renders a shared page (markdown + attachments) for the
// public /p/{token} route. It is intentionally distinct from PageShareService
// to keep the responsibility narrow and to let the dashboard layer skip mounting
// the public renderer when no PAGES module is wired.
type PagePublicViewService interface {
	// PageMarkdown returns the page title, raw markdown body, and sensitivity tag.
	// Returns an error containing the substring "not found" if the page does not exist.
	PageMarkdown(ctx context.Context, pageID string) (title, contentMD, sensitivity string, err error)
	// AttachmentsForPage lists attachments visible on the public view.
	AttachmentsForPage(ctx context.Context, pageID string) ([]*attachments.Attachment, error)
}

// WithPageAttachments inyecta el AttachmentStore.
func WithPageAttachments(svc PageAttachmentService) Option {
	return func(s *CloudServer) {
		s.pageAttachments = svc
	}
}

// WithPageShares inyecta el ShareStore.
func WithPageShares(svc PageShareService) Option {
	return func(s *CloudServer) {
		s.pageShares = svc
	}
}

// WithPagePublicView inyecta el renderizador read-only del /p/{token}.
func WithPagePublicView(svc PagePublicViewService) Option {
	return func(s *CloudServer) {
		s.pagePublicView = svc
	}
}

// ─── handlers — attachments ─────────────────────────────────────────────────

func (s *CloudServer) handleV1AttachmentUpload(w http.ResponseWriter, r *http.Request) {
	if s.pageAttachments == nil {
		http.Error(w, `{"error":"attachments not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if pageID == "" {
		http.Error(w, `{"error":"pageID required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	maxBytes := s.pageAttachments.MaxBytes()
	if maxBytes <= 0 {
		maxBytes = attachments.DefaultMaxBytes
	}
	// Cap the request body at maxBytes + 1MB headroom for multipart envelope.
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1<<20)
	if err := r.ParseMultipartForm(int64(8 << 20)); err != nil {
		http.Error(w, `{"error":"invalid multipart form"}`, http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `{"error":"file field is required"}`, http.StatusBadRequest)
		return
	}
	defer file.Close()
	att, err := s.pageAttachments.Upload(r.Context(), attachments.UploadParams{
		PageID:           pageID,
		OriginalFilename: header.Filename,
		Body:             file,
		UploadedByUID:    claims.UID,
		Description:      strings.TrimSpace(r.FormValue("description")),
	})
	if err != nil {
		switch {
		case errors.Is(err, attachments.ErrMIMENotAllowed):
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusUnsupportedMediaType)
		case errors.Is(err, attachments.ErrFileTooBig):
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusRequestEntityTooLarge)
		case errors.Is(err, attachments.ErrEmptyFile):
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		default:
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		}
		return
	}
	jsonResponse(w, http.StatusCreated, attachmentResponseView(att))
}

func (s *CloudServer) handleV1AttachmentList(w http.ResponseWriter, r *http.Request) {
	if s.pageAttachments == nil {
		http.Error(w, `{"error":"attachments not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if pageID == "" {
		http.Error(w, `{"error":"pageID required"}`, http.StatusBadRequest)
		return
	}
	rs, err := s.pageAttachments.ListByPage(r.Context(), pageID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(rs))
	for _, a := range rs {
		out = append(out, attachmentResponseView(a))
	}
	jsonResponse(w, http.StatusOK, map[string]any{"attachments": out})
}

func (s *CloudServer) handleV1AttachmentGet(w http.ResponseWriter, r *http.Request) {
	if s.pageAttachments == nil {
		http.Error(w, `{"error":"attachments not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	att, err := s.pageAttachments.Get(r.Context(), id)
	if err != nil {
		if errors.Is(err, attachments.ErrAttachmentNotFound) {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, attachmentResponseView(att))
}

func (s *CloudServer) handleV1AttachmentDownload(w http.ResponseWriter, r *http.Request) {
	if s.pageAttachments == nil {
		http.Error(w, `attachments not configured`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	att, err := s.pageAttachments.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rc, size, err := s.pageAttachments.OpenContent(att)
	if err != nil {
		http.Error(w, fmt.Sprintf("open: %v", err), http.StatusInternalServerError)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", att.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, attachments.SanitizeFilename(att.OriginalFilename)))
	_, _ = io.Copy(w, rc)
}

func (s *CloudServer) handleV1AttachmentThumbnail(w http.ResponseWriter, r *http.Request) {
	if s.pageAttachments == nil {
		http.Error(w, `attachments not configured`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	att, err := s.pageAttachments.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rc, size, err := s.pageAttachments.OpenThumbnail(att)
	if err != nil {
		http.Error(w, "thumbnail not ready", http.StatusNotFound)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	_, _ = io.Copy(w, rc)
}

func (s *CloudServer) handleV1AttachmentDelete(w http.ResponseWriter, r *http.Request) {
	if s.pageAttachments == nil {
		http.Error(w, `{"error":"attachments not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	claims, _ := claimsFromContext(r.Context())
	uid := ""
	if claims != nil {
		uid = claims.UID
	}
	if err := s.pageAttachments.Delete(r.Context(), id, uid); err != nil {
		if errors.Is(err, attachments.ErrAttachmentNotFound) {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true, "deleted": id})
}

// ─── handlers — share links ─────────────────────────────────────────────────

type v1ShareCreateRequest struct {
	ExpiresIn string `json:"expires_in,omitempty"` // "1h" | "24h" | "7d" | "30d" | ""
	Password  string `json:"password,omitempty"`
}

func (s *CloudServer) handleV1ShareCreate(w http.ResponseWriter, r *http.Request) {
	if s.pageShares == nil {
		http.Error(w, `{"error":"page shares not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if pageID == "" {
		http.Error(w, `{"error":"pageID required"}`, http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req v1ShareCreateRequest
	_ = json.NewDecoder(r.Body).Decode(&req) // empty body is OK (no expiry, no pw)

	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	var expiresAt *time.Time
	if d, ok := parseExpiry(req.ExpiresIn); ok {
		t := time.Now().UTC().Add(d)
		expiresAt = &t
	}
	link, err := s.pageShares.Create(r.Context(), attachments.CreateShareParams{
		PageID:       pageID,
		Password:     req.Password,
		ExpiresAt:    expiresAt,
		CreatedByUID: claims.UID,
	})
	if err != nil {
		if errors.Is(err, attachments.ErrShareForbiddenSensitivity) {
			http.Error(w, `{"error":"page is confidential and cannot be shared"}`, http.StatusForbidden)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusBadRequest)
		return
	}
	publicURL := strings.TrimRight(s.publicURL, "/") + "/p/" + link.Token
	jsonResponse(w, http.StatusCreated, map[string]any{
		"id":            link.ID,
		"page_id":       link.PageID,
		"token":         link.Token,
		"url":           publicURL,
		"has_password":  link.HasPassword,
		"expires_at":    link.ExpiresAt,
		"view_count":    link.ViewCount,
		"is_revoked":    link.IsRevoked,
		"created_at":    link.CreatedAt,
	})
}

func (s *CloudServer) handleV1SharesList(w http.ResponseWriter, r *http.Request) {
	if s.pageShares == nil {
		http.Error(w, `{"error":"page shares not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if pageID == "" {
		http.Error(w, `{"error":"pageID required"}`, http.StatusBadRequest)
		return
	}
	includeRevoked := r.URL.Query().Get("revoked") == "true"
	rs, err := s.pageShares.ListByPage(r.Context(), pageID, includeRevoked)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(rs))
	for _, link := range rs {
		out = append(out, map[string]any{
			"id":            link.ID,
			"page_id":       link.PageID,
			"token":         link.Token,
			"url":           strings.TrimRight(s.publicURL, "/") + "/p/" + link.Token,
			"has_password":  link.HasPassword,
			"expires_at":    link.ExpiresAt,
			"view_count":    link.ViewCount,
			"last_viewed":   link.LastViewedAt,
			"is_revoked":    link.IsRevoked,
			"created_at":    link.CreatedAt,
		})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"shares": out})
}

func (s *CloudServer) handleV1ShareRevoke(w http.ResponseWriter, r *http.Request) {
	if s.pageShares == nil {
		http.Error(w, `{"error":"page shares not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	claims, _ := claimsFromContext(r.Context())
	uid := ""
	if claims != nil {
		uid = claims.UID
	}
	if err := s.pageShares.Revoke(r.Context(), id, uid); err != nil {
		if errors.Is(err, attachments.ErrShareNotFound) {
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true, "revoked": id})
}

// ─── helpers ────────────────────────────────────────────────────────────────

func parseExpiry(s string) (time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return 0, false
	case "1h":
		return time.Hour, true
	case "24h", "1d":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	case "never":
		return 0, false
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, true
	}
	return 0, false
}

func attachmentResponseView(a *attachments.Attachment) map[string]any {
	if a == nil {
		return nil
	}
	return map[string]any{
		"id":                a.ID,
		"page_id":           a.PageID,
		"filename":          a.Filename,
		"original_filename": a.OriginalFilename,
		"mime_type":         a.MIMEType,
		"size_bytes":        a.SizeBytes,
		"size_human":        attachments.HumanSize(a.SizeBytes),
		"sha256":            a.SHA256,
		"description":       a.Description,
		"uploaded_by":       a.UploadedByUID,
		"has_thumbnail":     a.ThumbnailPath != "",
		"download_url":      "/v1/attachments/" + a.ID + "/download",
		"thumbnail_url":     thumbnailURLOrEmpty(a),
		"created_at":        a.CreatedAt,
	}
}

func thumbnailURLOrEmpty(a *attachments.Attachment) string {
	if a == nil || a.ThumbnailPath == "" {
		return ""
	}
	return "/v1/attachments/" + a.ID + "/thumbnail"
}
