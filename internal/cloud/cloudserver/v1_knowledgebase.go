package cloudserver

import (
	"fmt"
	"net/http"
	"strings"
)

// handleV1KBStatus retorna counts globales del tracking aria_kb_synced_entities.
func (s *CloudServer) handleV1KBStatus(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		http.Error(w, `{"error":"knowledge-base service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	stats, err := s.kb.Status(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"total":    stats.Total,
		"by_state": stats.ByState,
	})
}

// handleV1KBSyncAll re-sincea TODAS las entidades con sync_status='failed'.
// Reservado para admin (re-issue manual cuando un commit falló).
func (s *CloudServer) handleV1KBSyncAll(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		http.Error(w, `{"error":"knowledge-base service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	count, err := s.kb.ResyncFailed(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"resynced": count})
}

// handleV1KBSyncProject re-sincea todo lo asociado a un project_id.
func (s *CloudServer) handleV1KBSyncProject(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		http.Error(w, `{"error":"knowledge-base service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	projectID := strings.TrimSpace(r.PathValue("projectID"))
	if projectID == "" {
		http.Error(w, `{"error":"project_id is required"}`, http.StatusBadRequest)
		return
	}
	count, err := s.kb.ResyncProject(r.Context(), projectID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"resynced": count})
}

// handleV1QuoteExportDOCX retorna los bytes DOCX de una quote, generados
// fresh sin commit al repo. Útil para download manual desde el browser.
func (s *CloudServer) handleV1QuoteExportDOCX(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		http.Error(w, `{"error":"knowledge-base service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	quoteID := strings.TrimSpace(r.PathValue("quoteID"))
	if quoteID == "" {
		http.Error(w, `{"error":"quote_id is required"}`, http.StatusBadRequest)
		return
	}
	docx, err := s.kb.GenerateQuoteDOCX(r.Context(), quoteID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.wordprocessingml.document")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="propuesta-%s.docx"`, quoteID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(docx)
}

// handleV1QuoteExportMarkdown retorna el .md fuente de una cotización para
// preview/edición en clientes externos.
func (s *CloudServer) handleV1QuoteExportMarkdown(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		http.Error(w, `{"error":"knowledge-base service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	quoteID := strings.TrimSpace(r.PathValue("quoteID"))
	if quoteID == "" {
		http.Error(w, `{"error":"quote_id is required"}`, http.StatusBadRequest)
		return
	}
	md, err := s.kb.GenerateQuoteMarkdown(r.Context(), quoteID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="propuesta-%s.md"`, quoteID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(md))
}

// handleV1QuoteSyncToKB fuerza la sincronización de una cotización al repo
// central, regardless de su current sync state.
func (s *CloudServer) handleV1QuoteSyncToKB(w http.ResponseWriter, r *http.Request) {
	if s.kb == nil {
		http.Error(w, `{"error":"knowledge-base service not configured"}`, http.StatusServiceUnavailable)
		return
	}
	quoteID := strings.TrimSpace(r.PathValue("quoteID"))
	if quoteID == "" {
		http.Error(w, `{"error":"quote_id is required"}`, http.StatusBadRequest)
		return
	}
	commit, path, err := s.kb.SyncCotizacion(r.Context(), quoteID)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"commit_sha": commit,
		"repo_path":  path,
	})
}
