// Inline databases (Notion-style) endpoints. Soporta CRUD del database container,
// rows con validación de schema, y views guardadas (table/kanban/gallery/list/calendar).
//
// Auth: JWT bearer; cualquier usuario autenticado puede operar (la integración
// con permisos finos por aria_pages la implementa el agente PAGES — wave 5).
package cloudserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/databases"
)

// PageDatabaseService es el contrato cloudserver-facing del módulo databases.
// Implementado por *databases.Store via adapter en cmd/aria-core.
type PageDatabaseService interface {
	Create(ctx context.Context, p databases.CreateParams) (*databases.Database, error)
	GetByPage(ctx context.Context, pageID string) (*databases.Database, error)
	GetByID(ctx context.Context, id string) (*databases.Database, error)
	UpdateSchema(ctx context.Context, dbID string, newSchema []databases.PropDef, pruneUnknown bool) (*databases.Database, error)

	CreateRow(ctx context.Context, p databases.CreateRowParams) (*databases.Row, error)
	ListRows(ctx context.Context, dbID string, opts databases.ListRowsOpts) ([]*databases.Row, error)
	CountRows(ctx context.Context, dbID string, filters []databases.Filter) (int, error)
	GetRow(ctx context.Context, rowID string) (*databases.Row, error)
	UpdateRowProps(ctx context.Context, rowID string, patch map[string]any) (*databases.Row, error)
	MoveRow(ctx context.Context, rowID string, newOrder int) error
	DeleteRow(ctx context.Context, rowID string) error

	CreateView(ctx context.Context, p databases.CreateViewParams) (*databases.View, error)
	ListViews(ctx context.Context, dbID string) ([]*databases.View, error)
	GetView(ctx context.Context, viewID string) (*databases.View, error)
	UpdateView(ctx context.Context, viewID string, name, viewType string, config databases.ViewConfig, sortOrder int) (*databases.View, error)
	DeleteView(ctx context.Context, viewID string) error
}

// WithPageDatabases inyecta el servicio de inline databases.
func WithPageDatabases(svc PageDatabaseService) Option {
	return func(s *CloudServer) {
		s.pageDB = svc
	}
}

// ─── Request DTOs ──────────────────────────────────────────────────────────

type v1PageDBInitRequest struct {
	Schema      []databases.PropDef `json:"schema"`
	DefaultView string              `json:"default_view,omitempty"`
}

type v1PageDBSchemaUpdateRequest struct {
	Schema       []databases.PropDef `json:"schema"`
	PruneUnknown bool                `json:"prune_unknown,omitempty"`
}

type v1PageDBRowCreateRequest struct {
	Props     map[string]any `json:"props"`
	SortOrder int            `json:"sort_order,omitempty"`
}

type v1PageDBRowUpdateRequest struct {
	Props     map[string]any `json:"props,omitempty"`
	SortOrder *int           `json:"sort_order,omitempty"`
}

type v1PageDBViewCreateRequest struct {
	Name      string               `json:"name"`
	ViewType  string               `json:"view_type"`
	Config    databases.ViewConfig `json:"config"`
	SortOrder int                  `json:"sort_order,omitempty"`
}

// ─── handlers ──────────────────────────────────────────────────────────────

func (s *CloudServer) handleV1PageDBInit(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"page database service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if pageID == "" {
		http.Error(w, `{"error":"pageID required"}`, http.StatusBadRequest)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req v1PageDBInitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	db, err := s.pageDB.Create(r.Context(), databases.CreateParams{
		PageID:       pageID,
		Schema:       req.Schema,
		DefaultView:  req.DefaultView,
		CreatedByUID: byUID,
	})
	if err != nil {
		writePageDBError(w, err)
		return
	}
	jsonResponse(w, http.StatusCreated, db)
}

func (s *CloudServer) handleV1PageDBGet(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	db, err := s.pageDB.GetByPage(r.Context(), pageID)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	views, _ := s.pageDB.ListViews(r.Context(), db.ID)
	jsonResponse(w, http.StatusOK, map[string]any{
		"database": db,
		"views":    views,
	})
}

func (s *CloudServer) handleV1PageDBSchemaUpdate(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req v1PageDBSchemaUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	db, err := s.pageDB.GetByPage(r.Context(), pageID)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	updated, err := s.pageDB.UpdateSchema(r.Context(), db.ID, req.Schema, req.PruneUnknown)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, updated)
}

func (s *CloudServer) handleV1PageDBRowCreate(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	var req v1PageDBRowCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	db, err := s.pageDB.GetByPage(r.Context(), pageID)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	row, err := s.pageDB.CreateRow(r.Context(), databases.CreateRowParams{
		DatabaseID:   db.ID,
		Props:        req.Props,
		SortOrder:    req.SortOrder,
		CreatedByUID: byUID,
	})
	if err != nil {
		writePageDBError(w, err)
		return
	}
	jsonResponse(w, http.StatusCreated, row)
}

func (s *CloudServer) handleV1PageDBRowsList(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	db, err := s.pageDB.GetByPage(r.Context(), pageID)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	q := r.URL.Query()
	opts := databases.ListRowsOpts{}
	if v := strings.TrimSpace(q.Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Limit = n
		}
	}
	if v := strings.TrimSpace(q.Get("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			opts.Offset = n
		}
	}
	// filter=key:op:value (CSV)
	if rawF := q.Get("filter"); rawF != "" {
		filters, err := parseFiltersQuery(rawF)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		opts.Filters = filters
	}
	if rawS := q.Get("sort"); rawS != "" {
		opts.Sorts = parseSortsQuery(rawS)
	}
	rows, err := s.pageDB.ListRows(r.Context(), db.ID, opts)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	total, _ := s.pageDB.CountRows(r.Context(), db.ID, opts.Filters)
	jsonResponse(w, http.StatusOK, map[string]any{
		"rows":   rows,
		"total":  total,
		"limit":  opts.Limit,
		"offset": opts.Offset,
	})
}

func (s *CloudServer) handleV1PageDBRowUpdate(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	rowID := strings.TrimSpace(r.PathValue("rowID"))
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	var req v1PageDBRowUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.Props != nil {
		if _, err := s.pageDB.UpdateRowProps(r.Context(), rowID, req.Props); err != nil {
			writePageDBError(w, err)
			return
		}
	}
	if req.SortOrder != nil {
		if err := s.pageDB.MoveRow(r.Context(), rowID, *req.SortOrder); err != nil {
			writePageDBError(w, err)
			return
		}
	}
	row, err := s.pageDB.GetRow(r.Context(), rowID)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, row)
}

func (s *CloudServer) handleV1PageDBRowDelete(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	rowID := strings.TrimSpace(r.PathValue("rowID"))
	if err := s.pageDB.DeleteRow(r.Context(), rowID); err != nil {
		writePageDBError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *CloudServer) handleV1PageDBViewCreate(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req v1PageDBViewCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	db, err := s.pageDB.GetByPage(r.Context(), pageID)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	v, err := s.pageDB.CreateView(r.Context(), databases.CreateViewParams{
		DatabaseID:   db.ID,
		Name:         req.Name,
		ViewType:     req.ViewType,
		Config:       req.Config,
		SortOrder:    req.SortOrder,
		CreatedByUID: byUID,
	})
	if err != nil {
		writePageDBError(w, err)
		return
	}
	jsonResponse(w, http.StatusCreated, v)
}

func (s *CloudServer) handleV1PageDBViewUpdate(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	viewID := strings.TrimSpace(r.PathValue("viewID"))
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req v1PageDBViewCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	v, err := s.pageDB.UpdateView(r.Context(), viewID, req.Name, req.ViewType, req.Config, req.SortOrder)
	if err != nil {
		writePageDBError(w, err)
		return
	}
	jsonResponse(w, http.StatusOK, v)
}

func (s *CloudServer) handleV1PageDBViewDelete(w http.ResponseWriter, r *http.Request) {
	if s.pageDB == nil {
		http.Error(w, `{"error":"not configured"}`, http.StatusServiceUnavailable)
		return
	}
	viewID := strings.TrimSpace(r.PathValue("viewID"))
	if err := s.pageDB.DeleteView(r.Context(), viewID); err != nil {
		writePageDBError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ─── helpers ───────────────────────────────────────────────────────────────

// parseFiltersQuery parsea query param "key:op:value,key2:op2:value2".
func parseFiltersQuery(raw string) ([]databases.Filter, error) {
	parts := strings.Split(raw, ",")
	out := make([]databases.Filter, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		segs := strings.SplitN(p, ":", 3)
		if len(segs) < 2 {
			return nil, errors.New("filter format is key:op[:value]")
		}
		f := databases.Filter{
			Key: strings.TrimSpace(segs[0]),
			Op:  databases.FilterOp(strings.TrimSpace(segs[1])),
		}
		if len(segs) == 3 {
			f.Value = strings.TrimSpace(segs[2])
		}
		out = append(out, f)
	}
	return out, nil
}

// parseSortsQuery parsea "key:asc,key2:desc".
func parseSortsQuery(raw string) []databases.Sort {
	parts := strings.Split(raw, ",")
	out := []databases.Sort{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		segs := strings.SplitN(p, ":", 2)
		s := databases.Sort{Key: strings.TrimSpace(segs[0]), Direction: "asc"}
		if len(segs) == 2 {
			s.Direction = strings.TrimSpace(segs[1])
		}
		out = append(out, s)
	}
	return out
}

func writePageDBError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, databases.ErrNotFound):
		http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
	case errors.Is(err, databases.ErrConflict):
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusConflict)
	case errors.Is(err, databases.ErrInvalidSchema), errors.Is(err, databases.ErrInvalidRow):
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
	default:
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
	}
}
