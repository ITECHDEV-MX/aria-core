package cloudserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// === Quote-Chat REST endpoints (wave 6) ===
//
// These are JWT-bound (admin|agent|cotizador) and back the MCP tools as well
// as any external client (e.g. ARIA Studio).
//
//   POST /v1/cotizador/chat                 -> create session
//   GET  /v1/cotizador/chat/{id}            -> session detail (+ messages + sections)
//   POST /v1/cotizador/chat/{id}/send       -> append user message + dispatch to LLM
//   POST /v1/cotizador/chat/{id}/finalize   -> mark session finalized + create quote
//   POST /v1/cotizador/chat/{id}/email-send -> manual email dispatch (preview already shown client-side)

type v1CreateChatSessionRequest struct {
	LeadID      string `json:"lead_id"`
	RFPID       string `json:"rfp_id"`
	TemplateKey string `json:"template_key"`
	Title       string `json:"title"`
}

func (s *CloudServer) handleV1CotizadorChatCreate(w http.ResponseWriter, r *http.Request) {
	if s.quoteChat == nil {
		http.Error(w, `{"error":"quote-chat not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req v1CreateChatSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	uid := ""
	if claims != nil {
		uid = claims.UID
	}
	sess, err := s.quoteChat.CreateSession(r.Context(), dashboard.CreateChatSessionInput{
		LeadID:      strings.TrimSpace(req.LeadID),
		RFPID:       strings.TrimSpace(req.RFPID),
		TemplateKey: strings.TrimSpace(req.TemplateKey),
		Title:       strings.TrimSpace(req.Title),
		InitiatedBy: uid,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, sess)
}

func (s *CloudServer) handleV1CotizadorChatGet(w http.ResponseWriter, r *http.Request) {
	if s.quoteChat == nil {
		http.Error(w, `{"error":"quote-chat not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	sess, msgs, secs, err := s.quoteChat.GetSession(r.Context(), id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"session":  sess,
		"messages": msgs,
		"sections": secs,
	})
}

type v1ChatSendRequest struct {
	Message     string `json:"message"`
	Sensitivity string `json:"sensitivity"`
	TimeoutSec  int    `json:"timeout_sec"`
}

func (s *CloudServer) handleV1CotizadorChatSend(w http.ResponseWriter, r *http.Request) {
	if s.quoteChat == nil {
		http.Error(w, `{"error":"quote-chat not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req v1ChatSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	uid := ""
	if claims != nil {
		uid = claims.UID
	}
	user, assistant, err := s.quoteChat.SendUserMessage(r.Context(), dashboard.SendChatMessageInput{
		SessionID:   id,
		UserUID:     uid,
		Message:     req.Message,
		Sensitivity: req.Sensitivity,
		TimeoutSec:  req.TimeoutSec,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"user":      user,
		"assistant": assistant,
	})
}

func (s *CloudServer) handleV1CotizadorChatFinalize(w http.ResponseWriter, r *http.Request) {
	if s.quoteChat == nil {
		http.Error(w, `{"error":"quote-chat not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	claims, _ := claimsFromContext(r.Context())
	uid := ""
	if claims != nil {
		uid = claims.UID
	}
	quoteID, err := s.quoteChat.FinalizeSession(r.Context(), id, uid)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"session_id": id,
		"quote_id":   quoteID,
	})
}

type v1ChatEmailSendRequest struct {
	QuoteID  string `json:"quote_id"`
	To       string `json:"to"`
	CC       string `json:"cc"`
	Subject  string `json:"subject"`
	BodyHTML string `json:"body_html"`
}

func (s *CloudServer) handleV1CotizadorChatEmailSend(w http.ResponseWriter, r *http.Request) {
	if s.quoteChat == nil {
		http.Error(w, `{"error":"quote-chat not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 1*1024*1024)
	var req v1ChatEmailSendRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json"}`, http.StatusBadRequest)
		return
	}
	if req.To == "" || req.Subject == "" || strings.TrimSpace(req.BodyHTML) == "" {
		http.Error(w, `{"error":"to, subject, body_html required"}`, http.StatusBadRequest)
		return
	}
	if err := s.quoteChat.SendEmail(r.Context(), id, req.QuoteID, req.To, req.CC, req.Subject, req.BodyHTML); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"sent": true, "to": req.To})
}

// quoteChatRouteError is returned when the upstream chat route signals a
// transient failure that callers may retry. Reserved for future use.
var quoteChatRouteError = errors.New("cotizador chat: route failed")

var _ = quoteChatRouteError
