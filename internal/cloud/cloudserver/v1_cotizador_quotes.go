package cloudserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// === RFPs ===

type v1CreateRFPRequest struct {
	SourceType    string `json:"source_type"`
	SourceContent string `json:"source_content"`
	AnalysisJSON  string `json:"analysis_json,omitempty"`
}

func (s *CloudServer) handleV1CotizadorRFPCreate(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024*1024)
	var req v1CreateRFPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	createdBy := ""
	if claims != nil {
		createdBy = claims.UID
	}
	rfp, err := s.cotizador.CreateRFP(r.Context(), dashboard.CreateRFPInput{
		LeadID: leadID, SourceType: req.SourceType, SourceContent: req.SourceContent,
		AnalysisJSON: req.AnalysisJSON, CreatedByUID: createdBy,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, rfp)
}

func (s *CloudServer) handleV1CotizadorRFPList(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	rfps, err := s.cotizador.ListRFPsByLead(r.Context(), leadID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"rfps": rfps, "count": len(rfps)})
}

func (s *CloudServer) handleV1CotizadorRFPGet(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	rfp, err := s.cotizador.GetRFP(r.Context(), r.PathValue("rfpID"))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, rfp)
}

type v1RFPAnalysisUpdate struct {
	AnalysisJSON json.RawMessage `json:"analysis_json"`
}

func (s *CloudServer) handleV1CotizadorRFPAnalysisUpdate(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	rfpID := r.PathValue("rfpID")
	r.Body = http.MaxBytesReader(w, r.Body, 4*1024*1024)
	var req v1RFPAnalysisUpdate
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if len(req.AnalysisJSON) == 0 {
		http.Error(w, `{"error":"analysis_json is required"}`, http.StatusBadRequest)
		return
	}
	if err := s.cotizador.UpdateRFPAnalysis(r.Context(), rfpID, string(req.AnalysisJSON)); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	rfp, err := s.cotizador.GetRFP(r.Context(), rfpID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, rfp)
}

// === Quotes ===

type v1QuoteItem struct {
	SKU         string  `json:"sku,omitempty"`
	Description string  `json:"description"`
	Qty         float64 `json:"qty"`
	UnitPrice   float64 `json:"unit_price"`
}

type v1CreateQuoteRequest struct {
	RFPID         string        `json:"rfp_id,omitempty"`
	Currency      string        `json:"currency,omitempty"`
	ValidUntil    string        `json:"valid_until,omitempty"` // YYYY-MM-DD
	Terms         string        `json:"terms,omitempty"`
	Justification string        `json:"justification,omitempty"`
	Items         []v1QuoteItem `json:"items"`
}

func (s *CloudServer) handleV1CotizadorQuoteCreate(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	var req v1CreateQuoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if len(req.Items) == 0 {
		http.Error(w, `{"error":"at least one item is required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	createdBy := ""
	role := "cotizador"
	if claims != nil {
		createdBy = claims.UID
		if claims.HasRole("cotizador") {
			role = "cotizador"
		} else if claims.HasRole("admin") {
			role = "admin"
		}
	}
	in := dashboard.CreateQuoteInput{
		LeadID:        leadID,
		RFPID:         strings.TrimSpace(req.RFPID),
		Currency:      strings.TrimSpace(req.Currency),
		Terms:         req.Terms,
		Justification: req.Justification,
		CreatedByUID:  createdBy,
		Role:          role,
	}
	if v := strings.TrimSpace(req.ValidUntil); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			in.ValidUntil = &t
		}
	}
	for _, it := range req.Items {
		in.Items = append(in.Items, dashboard.CreateQuoteItemInput{
			SKU: it.SKU, Description: it.Description, Qty: it.Qty, UnitPrice: it.UnitPrice,
		})
	}
	q, err := s.cotizador.CreateQuote(r.Context(), in)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, q)
}

func (s *CloudServer) handleV1CotizadorQuoteList(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	quotes, err := s.cotizador.ListQuotesByLead(r.Context(), leadID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"quotes": quotes, "count": len(quotes)})
}

func (s *CloudServer) handleV1CotizadorQuoteGet(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	q, err := s.cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	items, _ := s.cotizador.ListQuoteItems(r.Context(), quoteID)
	jsonResponse(w, http.StatusOK, map[string]any{"quote": q, "items": items})
}

func (s *CloudServer) handleV1CotizadorQuoteStatus(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
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
	if err := s.cotizador.UpdateQuoteStatus(r.Context(), quoteID, strings.TrimSpace(req.Status), byUID, req.Notes); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	q, err := s.cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, q)
}
