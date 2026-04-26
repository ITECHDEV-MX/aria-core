package cloudserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// REST endpoints v1 para el módulo de páginas (mini-Notion).
// Los MCP tools (aria_page_create, aria_page_get, aria_page_search, aria_pages_tree)
// hablan contra estos endpoints con JWT user-bound.
//
// Auth: cualquier user autenticado puede crear/leer páginas con scope team o
// public. Páginas con scope=client_knowledge requieren grant explícito (TODO en
// wave 5; por ahora basta role admin).

type v1PageCreateRequest struct {
	ParentID    string `json:"parent_id,omitempty"`
	Title       string `json:"title"`
	Content     string `json:"content,omitempty"`
	Icon        string `json:"icon,omitempty"`
	Project     string `json:"project,omitempty"`
	Scope       string `json:"scope,omitempty"`
	Sensitivity string `json:"sensitivity,omitempty"`
	Template    string `json:"template,omitempty"`
}

func (s *CloudServer) handleV1PageCreate(w http.ResponseWriter, r *http.Request) {
	if s.pagesDash == nil {
		http.Error(w, `{"error":"pages module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	var req v1PageCreateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	if byUID == "" {
		http.Error(w, `{"error":"missing uid in claims"}`, http.StatusUnauthorized)
		return
	}
	pg, err := s.pagesDash.Create(r.Context(), dashboard.CreatePageInput{
		ParentID:     req.ParentID,
		Title:        req.Title,
		ContentMD:    req.Content,
		Icon:         req.Icon,
		Project:      req.Project,
		Scope:        req.Scope,
		Sensitivity:  req.Sensitivity,
		TemplateKey:  req.Template,
		CreatedByUID: byUID,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, pg)
}

func (s *CloudServer) handleV1PageGet(w http.ResponseWriter, r *http.Request) {
	if s.pagesDash == nil {
		http.Error(w, `{"error":"pages module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	pg, err := s.pagesDash.Get(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, pg)
}

func (s *CloudServer) handleV1PagesSearch(w http.ResponseWriter, r *http.Request) {
	if s.pagesDash == nil {
		http.Error(w, `{"error":"pages module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	res, err := s.pagesDash.QuickSearchAll(r.Context(), q, limit)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, res)
}

func (s *CloudServer) handleV1PagesTree(w http.ResponseWriter, r *http.Request) {
	if s.pagesDash == nil {
		http.Error(w, `{"error":"pages module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	tree, err := s.pagesDash.Tree(r.Context(), project, scope)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"pages": tree})
}

