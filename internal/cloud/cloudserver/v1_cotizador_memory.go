package cloudserver

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// === Close quote (con outcome obligatorio + lesson opcional pero recomendada) ===

type v1CloseQuoteRequest struct {
	Status      string   `json:"status"`       // approved|rejected|expired
	Reason      string   `json:"reason,omitempty"`
	LessonText  string   `json:"lesson_text,omitempty"`  // si vacío, no se inserta lesson
	LessonTags  []string `json:"lesson_tags,omitempty"`
}

func (s *CloudServer) handleV1CotizadorQuoteClose(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var req v1CloseQuoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	if err := s.cotizador.CloseQuoteWithOutcome(r.Context(), quoteID,
		strings.TrimSpace(req.Status), byUID, req.Reason, req.LessonText, req.LessonTags); err != nil {
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

// === Similar items search ===

func (s *CloudServer) handleV1CotizadorSimilarItems(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		http.Error(w, `{"error":"query param q is required"}`, http.StatusBadRequest)
		return
	}
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	hits, err := s.cotizador.SearchSimilarItems(r.Context(), query, limit)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"results": hits, "count": len(hits)})
}

// === Outcome stats ===

func (s *CloudServer) handleV1CotizadorOutcomeStats(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	stats, err := s.cotizador.GetOutcomeStats(r.Context())
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, stats)
}

// === Client history ===

func (s *CloudServer) handleV1CotizadorClientHistory(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		http.Error(w, `{"error":"query param q is required"}`, http.StatusBadRequest)
		return
	}
	hist, err := s.cotizador.GetClientHistory(r.Context(), q)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"results": hist, "count": len(hist)})
}

// === Lessons search ===

func (s *CloudServer) handleV1CotizadorLessonsSearch(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	tag := strings.TrimSpace(r.URL.Query().Get("tag"))
	limit := 20
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	lessons, err := s.cotizador.SearchLessons(r.Context(), query, tag, limit)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"lessons": lessons, "count": len(lessons)})
}

// === Lesson create ===

type v1CreateLessonRequest struct {
	QuoteID string   `json:"quote_id,omitempty"`
	LeadID  string   `json:"lead_id,omitempty"`
	Text    string   `json:"text"`
	Tags    []string `json:"tags,omitempty"`
}

func (s *CloudServer) handleV1CotizadorLessonCreate(w http.ResponseWriter, r *http.Request) {
	if s.cotizador == nil {
		http.Error(w, `{"error":"cotizador module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var req v1CreateLessonRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Text) == "" {
		http.Error(w, `{"error":"text is required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	role := "cotizador"
	if claims != nil {
		byUID = claims.UID
		if claims.HasRole("cotizador") {
			role = "cotizador"
		} else if claims.HasRole("admin") {
			role = "admin"
		}
	}
	l, err := s.cotizador.CreateLesson(r.Context(), dashboard.CreateLessonInput{
		QuoteID: req.QuoteID, LeadID: req.LeadID, Text: req.Text, Tags: req.Tags,
		CreatedByUID: byUID, Role: role,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, l)
}
