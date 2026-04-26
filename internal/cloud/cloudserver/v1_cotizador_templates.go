package cloudserver

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *CloudServer) handleV1CotizadorTemplatesList(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	tmpls := s.cotizador.ListTemplates()
	jsonResponse(w, http.StatusOK, map[string]any{"templates": tmpls, "count": len(tmpls)})
}

type v1ApplyTemplateRequest struct {
	TemplateKey string `json:"template_key"`
}

func (s *CloudServer) handleV1CotizadorApplyTemplate(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024)
	var req v1ApplyTemplateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.TemplateKey) == "" {
		http.Error(w, `{"error":"template_key is required"}`, http.StatusBadRequest)
		return
	}
	if err := s.cotizador.ApplyTemplate(r.Context(), quoteID, req.TemplateKey); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	q, err := s.cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	sections, _ := s.cotizador.ListSections(r.Context(), quoteID)
	jsonResponse(w, http.StatusOK, map[string]any{"quote": q, "sections_applied": len(sections)})
}
