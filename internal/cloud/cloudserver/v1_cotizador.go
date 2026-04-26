package cloudserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

func (s *CloudServer) handleV1CotizadorLeadsList(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	leads, err := s.cotizador.ListLeads(r.Context(), status)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"leads": leads, "count": len(leads)})
}

type v1CreateLeadRequest struct {
	Name    string `json:"name"`
	Company string `json:"company"`
	Email   string `json:"email"`
	Phone   string `json:"phone"`
	Source  string `json:"source"`
	Notes   string `json:"notes"`
}

func (s *CloudServer) handleV1CotizadorLeadCreate(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req v1CreateLeadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	role := "cotizador"
	createdBy := ""
	if claims != nil {
		createdBy = claims.UID
		// si admin Y cotizador, prefiero etiquetar como cotizador (más restrictivo).
		if claims.HasRole("cotizador") {
			role = "cotizador"
		} else if claims.HasRole("admin") {
			role = "admin"
		}
	}
	lead, err := s.cotizador.CreateLead(r.Context(), s.cotizadorCreateInput(req, createdBy, role))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, lead)
}

func (s *CloudServer) cotizadorCreateInput(req v1CreateLeadRequest, createdBy, role string) dashboard.CreateLeadInput {
	return dashboard.CreateLeadInput{
		Name: req.Name, Company: req.Company, Email: req.Email, Phone: req.Phone,
		Source: req.Source, Notes: req.Notes, CreatedByUID: createdBy, Role: role,
	}
}

func (s *CloudServer) handleV1CotizadorLeadGet(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	lead, err := s.cotizador.GetLead(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, lead)
}

type v1StatusRequest struct {
	Status string `json:"status"`
	Notes  string `json:"notes,omitempty"`
}

func (s *CloudServer) handleV1CotizadorLeadStatus(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req v1StatusRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	if err := s.cotizador.UpdateLeadStatus(r.Context(), id, strings.TrimSpace(req.Status), byUID, req.Notes); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	lead, err := s.cotizador.GetLead(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, lead)
}

func (s *CloudServer) handleV1CotizadorLeadHistory(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	entries, err := s.cotizador.LeadHistory(r.Context(), id, 50)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"history": entries, "count": len(entries)})
}

