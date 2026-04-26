// Comments inline en bloques de aria_pages markdown. Threading + @mentions +
// resolve flow. Auth: JWT bearer; el author o admin pueden borrar.
package cloudserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/comments"
)

// PageCommentsService es el contrato cloudserver-facing para comments.
// Implementado por *comments.Store via adapter en cmd/aria-core.
type PageCommentsService interface {
	Create(ctx context.Context, p comments.CreateParams) (*comments.Comment, error)
	Get(ctx context.Context, id string) (*comments.Comment, error)
	ListPage(ctx context.Context, pageID string, opts comments.ListPageOpts) ([]*comments.Comment, error)
	ListReplies(ctx context.Context, parentID string) ([]*comments.Comment, error)
	UpdateContent(ctx context.Context, id, authorUID, newContent string) (*comments.Comment, error)
	Resolve(ctx context.Context, id, byUID string) error
	Unresolve(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	CountUnresolved(ctx context.Context, pageID string) (int, error)
	CountUnreadMentions(ctx context.Context, uid string) (int, error)
	MarkMentionsRead(ctx context.Context, uid, pageID string) error
	ListMentions(ctx context.Context, uid string, onlyUnread bool, limit int) ([]comments.MentionRow, error)

	// NotifyMentioned dispatches email para mentions pendientes.
	NotifyMentioned(ctx context.Context, commentID, pageTitle, pageURL, mentionedBy string) error
}

// WithPageComments inyecta el servicio de comments.
func WithPageComments(svc PageCommentsService) Option {
	return func(s *CloudServer) {
		s.pageComments = svc
	}
}

// ─── DTOs ──────────────────────────────────────────────────────────────────

type v1CommentCreateRequest struct {
	BlockAnchor     string `json:"block_anchor,omitempty"`
	ParentCommentID string `json:"parent_comment_id,omitempty"`
	ContentMD       string `json:"content_md"`
	// PageTitle / PageURL son metadata para email notification (opcional).
	PageTitle string `json:"page_title,omitempty"`
	PageURL   string `json:"page_url,omitempty"`
}

type v1CommentUpdateRequest struct {
	ContentMD string `json:"content_md"`
}

// ─── handlers ──────────────────────────────────────────────────────────────

func (s *CloudServer) handleV1CommentCreate(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"comments not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if pageID == "" {
		http.Error(w, `{"error":"pageID required"}`, http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req v1CommentCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	c, err := s.pageComments.Create(r.Context(), comments.CreateParams{
		PageID:          pageID,
		BlockAnchor:     req.BlockAnchor,
		ParentCommentID: req.ParentCommentID,
		ContentMD:       req.ContentMD,
		AuthorUID:       claims.UID,
		MentionResolver: s.usernameToUIDResolver(),
	})
	if err != nil {
		writeCommentsError(w, err)
		return
	}
	// Dispatch email async-style (best-effort; ignoramos errores aquí).
	go func() {
		_ = s.pageComments.NotifyMentioned(context.Background(), c.ID, req.PageTitle, req.PageURL, claims.Email)
	}()
	jsonResponse(w, http.StatusCreated, c)
}

func (s *CloudServer) handleV1CommentsList(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"comments not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	q := r.URL.Query()
	opts := comments.ListPageOpts{
		BlockAnchor:    strings.TrimSpace(q.Get("anchor")),
		IncludeReplies: q.Get("include_replies") == "true",
	}
	switch strings.ToLower(strings.TrimSpace(q.Get("filter"))) {
	case "unresolved":
		opts.OnlyUnresolved = true
	case "resolved":
		opts.OnlyResolved = true
	}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Limit = n
		}
	}
	cs, err := s.pageComments.ListPage(r.Context(), pageID, opts)
	if err != nil {
		writeCommentsError(w, err)
		return
	}
	unresolved, _ := s.pageComments.CountUnresolved(r.Context(), pageID)
	jsonResponse(w, http.StatusOK, map[string]any{
		"comments":         cs,
		"unresolved_count": unresolved,
	})
}

func (s *CloudServer) handleV1CommentRepliesList(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	replies, err := s.pageComments.ListReplies(r.Context(), id)
	if err != nil {
		writeCommentsError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"replies": replies})
}

func (s *CloudServer) handleV1CommentUpdate(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req v1CommentUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	c, err := s.pageComments.UpdateContent(r.Context(), id, claims.UID, req.ContentMD)
	if err != nil {
		writeCommentsError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, c)
}

func (s *CloudServer) handleV1CommentResolve(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	if err := s.pageComments.Resolve(r.Context(), id, claims.UID); err != nil {
		writeCommentsError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"resolved": true})
}

func (s *CloudServer) handleV1CommentUnresolve(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if err := s.pageComments.Unresolve(r.Context(), id); err != nil {
		writeCommentsError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"resolved": false})
}

func (s *CloudServer) handleV1CommentDelete(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	// Solo author o admin pueden borrar.
	current, err := s.pageComments.Get(r.Context(), id)
	if err != nil {
		writeCommentsError(w, err)
		return
	}
	isAdmin := claims.HasRole("admin")
	if current.AuthorUID != claims.UID && !isAdmin {
		http.Error(w, `{"error":"forbidden: only author or admin can delete"}`, http.StatusForbidden)
		return
	}
	if err := s.pageComments.Delete(r.Context(), id); err != nil {
		writeCommentsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleV1MentionsList retorna las mentions del usuario autenticado.
func (s *CloudServer) handleV1MentionsList(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	q := r.URL.Query()
	onlyUnread := q.Get("unread") == "true"
	limit := 50
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	rows, err := s.pageComments.ListMentions(r.Context(), claims.UID, onlyUnread, limit)
	if err != nil {
		writeCommentsError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"mentions": rows})
}

// handleV1MentionsMarkRead marca las mentions de una page como leídas.
func (s *CloudServer) handleV1MentionsMarkRead(w http.ResponseWriter, r *http.Request) {
	if s.pageComments == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if err := s.pageComments.MarkMentionsRead(r.Context(), claims.UID, pageID); err != nil {
		writeCommentsError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// usernameToUIDResolver retorna una función que mapea username/email → uid
// usando el userStore configurado. Si no hay match, retorna "".
func (s *CloudServer) usernameToUIDResolver() func(string) string {
	if s.userStore == nil {
		return nil
	}
	return func(name string) string {
		ctx := context.Background()
		// Intentar como email primero.
		if strings.Contains(name, "@") {
			if u, err := s.userStore.GetByEmail(ctx, name); err == nil && u != nil {
				return u.UID
			}
		}
		// Si no, intentar como prefijo email "name" → "name@*".
		// userStore no tiene GetByUsername; best-effort sólo email.
		if u, err := s.userStore.GetByEmail(ctx, name); err == nil && u != nil {
			return u.UID
		}
		return ""
	}
}

func writeCommentsError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, comments.ErrNotFound):
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	case errors.Is(err, comments.ErrForbidden):
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
	case errors.Is(err, comments.ErrInvalidInput):
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
	default:
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
	}
}
