package cloudserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

type v1PromoteLeadRequest struct {
	LegalName     string          `json:"legal_name"`
	RFC           string          `json:"rfc"`
	FiscalAddress string          `json:"fiscal_address"`
	BillingEmail  string          `json:"billing_email"`
	Contacts      json.RawMessage `json:"contacts,omitempty"` // array de contactos
	Notes         string          `json:"notes,omitempty"`
}

func (s *CloudServer) handleV1CotizadorPromoteLead(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req v1PromoteLeadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.LegalName) == "" {
		http.Error(w, `{"error":"legal_name is required"}`, http.StatusBadRequest)
		return
	}
	contacts := string(req.Contacts)
	if contacts == "" {
		contacts = "[]"
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	c, err := s.cotizador.PromoteLeadToClient(r.Context(), dashboard.PromoteLeadInput{
		LeadID: leadID, LegalName: req.LegalName, RFC: req.RFC,
		FiscalAddress: req.FiscalAddress, BillingEmail: req.BillingEmail,
		ContactsJSON: contacts, Notes: req.Notes, CreatedByUID: byUID,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, c)
}

func (s *CloudServer) handleV1CotizadorClientsList(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	clients, err := s.cotizador.ListClients(r.Context())
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"clients": clients, "count": len(clients)})
}

func (s *CloudServer) handleV1CotizadorClientGet(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	c, err := s.cotizador.GetClient(r.Context(), r.PathValue("clientID"))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, c)
}
