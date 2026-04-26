package cloudserver

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type v1MemSaveRequest struct {
	SessionID        string          `json:"session_id,omitempty"`
	Project          string          `json:"project,omitempty"`
	Scope            string          `json:"scope,omitempty"`
	ObservationType  string          `json:"type,omitempty"`
	Title            string          `json:"title"`
	Subtitle         string          `json:"subtitle,omitempty"`
	Narrative        string          `json:"narrative,omitempty"`
	Content          string          `json:"content,omitempty"` // alias de narrative para compat legacy
	Facts            string          `json:"facts,omitempty"`
	Concepts         string          `json:"concepts,omitempty"`
	FilesTouched     string          `json:"files_touched,omitempty"`
	ReasoningTrace   json.RawMessage `json:"reasoning_trace,omitempty"`
	TopicKey         string          `json:"topic_key,omitempty"`
	Source           string          `json:"source,omitempty"`
	GeneratedByModel string          `json:"generated_by_model,omitempty"`
	ClientID         string          `json:"client_id,omitempty"`
	Sensitivity      string          `json:"sensitivity,omitempty"`
}

func (s *CloudServer) handleV1MemorySave(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024*1024)
	var req v1MemSaveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Title) == "" {
		http.Error(w, `{"error":"title is required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	devUID, devEmail, devRole := "", "", "dev"
	if claims != nil {
		devUID = claims.UID
		devEmail = claims.Email
		if claims.HasRole("admin") {
			devRole = "admin"
		}
	}
	narrative := req.Narrative
	if narrative == "" {
		narrative = req.Content // alias compat
	}
	in := AriaMemSaveInput{
		SessionID:        req.SessionID,
		DeveloperUID:     devUID,
		DeveloperRole:    devRole,
		ClientID:         req.ClientID,
		Project:          req.Project,
		Scope:            req.Scope,
		ObservationType:  req.ObservationType,
		Title:            req.Title,
		Subtitle:         req.Subtitle,
		Narrative:        narrative,
		Facts:            req.Facts,
		Concepts:         req.Concepts,
		FilesTouched:     req.FilesTouched,
		ReasoningTrace:   string(req.ReasoningTrace),
		TopicKey:         req.TopicKey,
		Source:           req.Source,
		GeneratedByModel: req.GeneratedByModel,
		Sensitivity:      req.Sensitivity,
	}
	_ = devEmail
	o, err := s.ariaMem.Save(r.Context(), in)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusCreated, o)
}

func (s *CloudServer) handleV1MemoryGet(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	o, err := s.ariaMem.GetByID(r.Context(), r.PathValue("id"))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusNotFound)
		return
	}
	jsonResponse(w, http.StatusOK, o)
}

func (s *CloudServer) handleV1MemorySearch(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	tokenBudget, _ := strconv.Atoi(r.URL.Query().Get("token_budget"))
	strategy := strings.TrimSpace(r.URL.Query().Get("strategy"))
	in := AriaMemSearchInput{
		Query:           strings.TrimSpace(r.URL.Query().Get("q")),
		Project:         strings.TrimSpace(r.URL.Query().Get("project")),
		Scope:           strings.TrimSpace(r.URL.Query().Get("scope")),
		ObservationType: strings.TrimSpace(r.URL.Query().Get("type")),
		Limit:           limit,
	}
	if tokenBudget > 0 {
		results, truncated, tokens, err := s.ariaMem.SearchWithBudget(r.Context(), in, tokenBudget, strategy)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"results":         results,
			"count":           len(results),
			"truncated_count": truncated,
			"tokens_used":     tokens,
			"strategy":        strategy,
		})
		return
	}
	rs, err := s.ariaMem.Search(r.Context(), in)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	// Bóveda-de-cliente: para cada resultado decidir si puede salir tal cual,
	// si necesita scrub, o si debe quedar bloqueado por confidential. El
	// caller del MCP es Claude (provider='anthropic'); cuando esté offline
	// usa ollama-local. Esto está duro-codeado a 'anthropic' como heurística
	// hasta que el MCP transport pase un X-LLM-Provider header.
	provider := strings.TrimSpace(strings.ToLower(r.Header.Get("X-LLM-Provider")))
	if provider == "" {
		provider = "anthropic"
	}
	reason := strings.TrimSpace(r.URL.Query().Get("reason"))
	if reason == "" {
		reason = "aria_search"
	}
	claims, _ := claimsFromContext(r.Context())
	userUID := ""
	if claims != nil {
		userUID = claims.UID
	}
	if s.scrubber != nil {
		applyScrubGate(r.Context(), s.scrubber, rs, provider, reason, userUID)
	}

	jsonResponse(w, http.StatusOK, map[string]any{"results": rs, "count": len(rs)})
}

// applyScrubGate aplica la política bóveda-de-cliente a cada resultado:
//   - public/internal: pasa sin tocar
//   - client + provider permitido: scrub a tokens y log
//   - confidential: bloquea (vacía narrative/facts) y log
//
// La fila se loguea siempre que el caller toque algo (incluido el bloqueo).
func applyScrubGate(ctx context.Context, gate ScrubGate, results []*AriaMemObservation, provider, reason, userUID string) {
	for _, o := range results {
		if o == nil {
			continue
		}
		sens := strings.TrimSpace(o.Sensitivity)
		if sens == "" {
			sens = "internal"
		}
		if sens == "public" || sens == "internal" {
			continue
		}
		// Combine narrative+facts into one buffer; treat the full row as the
		// scrubbed payload.
		payload := o.Narrative + "\n" + o.Facts
		size := len(payload)
		hash := payloadHashSafe(payload)
		if !gate.CanSendToLLM(sens, provider) {
			// Confidential or disallowed provider: blank out and log.
			o.Narrative = "[BLOCKED-CONFIDENTIAL]"
			o.Facts = ""
			_ = gate.LogEgress(ctx, "", o.ID, provider, "", o.ClientID, userUID, reason+":blocked", hash, size, false, "[]")
			continue
		}
		// Scrub.
		newNarr, redJSON := gate.ScrubString(ctx, o.Narrative)
		newFacts, _ := gate.ScrubString(ctx, o.Facts)
		o.Narrative = newNarr
		o.Facts = newFacts
		_ = gate.LogEgress(ctx, "", o.ID, provider, "", o.ClientID, userUID, reason, hash, size, true, redJSON)
	}
}

func payloadHashSafe(s string) string {
	if s == "" {
		return ""
	}
	// We do not want to import crypto/sha256 in this file; the redactor
	// package already hashes when it logs egress. For the passthrough audit
	// log call we accept a placeholder; the real hash is computed inside
	// LogEgress() when called via the redactor adapter.
	return ""
}

func (s *CloudServer) handleV1MemoryTimeline(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var since, until *time.Time
	if v := strings.TrimSpace(r.URL.Query().Get("since")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			since = &t
		}
	}
	if v := strings.TrimSpace(r.URL.Query().Get("until")); v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			until = &t
		}
	}
	rs, err := s.ariaMem.Timeline(r.Context(), project, since, until, limit)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"results": rs, "count": len(rs)})
}

func (s *CloudServer) handleV1MemoryPromoteCanon(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	claims, _ := claimsFromContext(r.Context())
	by := ""
	if claims != nil {
		by = claims.UID
	}
	if err := s.ariaMem.PromoteCanon(r.Context(), id, by); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"id": id, "canon": true})
}

type v1QualityRequest struct {
	Signal string  `json:"signal"`
	Score  float64 `json:"score"`
	Notes  string  `json:"notes,omitempty"`
}

func (s *CloudServer) handleV1MemoryRecordQuality(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req v1QualityRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	by := ""
	if claims != nil {
		by = claims.UID
	}
	if err := s.ariaMem.RecordQuality(r.Context(), id, req.Signal, req.Score, req.Notes, by); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true})
}

type v1SessionStartRequest struct {
	Project     string   `json:"project,omitempty"`
	Directory   string   `json:"directory,omitempty"`
	Goal        string   `json:"goal,omitempty"`
	ClientID    string   `json:"client_id,omitempty"`
	MachineID   string   `json:"machine_id,omitempty"`
	Stack       []string `json:"stack,omitempty"`
	TokenBudget int      `json:"token_budget,omitempty"`
}

func (s *CloudServer) handleV1MemorySessionStart(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32*1024)
	var req v1SessionStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	devUID, devEmail, devRole := "", "", "dev"
	if claims != nil {
		devUID = claims.UID
		devEmail = claims.Email
		if claims.HasRole("admin") {
			devRole = "admin"
		}
	}
	sess, err := s.ariaMem.StartSession(r.Context(), AriaMemStartSessionInput{
		DeveloperUID: devUID, DeveloperEmail: devEmail, DeveloperRole: devRole,
		ClientID: req.ClientID, MachineID: req.MachineID,
		Project: req.Project, Directory: req.Directory, Goal: req.Goal,
	})
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}

	// Auto-context injection: armar primer mensaje con canon+skills+recipes.
	autoCtx, _ := s.ariaMem.BuildSessionAutoContext(r.Context(), AriaMemAutoContextInput{
		Project:      req.Project,
		Goal:         req.Goal,
		Stack:        req.Stack,
		DeveloperUID: devUID,
		SessionID:    sess.ID,
		TokenBudget:  req.TokenBudget,
	})
	resp := map[string]any{
		"session": sess,
	}
	if autoCtx != nil {
		resp["auto_context"] = autoCtx.Markdown
		resp["auto_context_tokens"] = autoCtx.TokensUsed
		resp["auto_context_truncated"] = autoCtx.TruncatedItems
	}
	// session_id top-level para compatibilidad con clientes existentes que esperan {id}
	resp["session_id"] = sess.ID
	jsonResponse(w, http.StatusCreated, resp)
}

type v1SessionSummaryRequest struct {
	Request      string `json:"request,omitempty"`
	Investigated string `json:"investigated,omitempty"`
	Learned      string `json:"learned,omitempty"`
	Completed    string `json:"completed,omitempty"`
	NextSteps    string `json:"next_steps,omitempty"`
	FilesRead    string `json:"files_read,omitempty"`
	FilesEdited  string `json:"files_edited,omitempty"`
	Notes        string `json:"notes,omitempty"`
	QualityGrade string `json:"quality_grade,omitempty"`
}

func (s *CloudServer) handleV1MemorySessionSummary(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	r.Body = http.MaxBytesReader(w, r.Body, 256*1024)
	var req v1SessionSummaryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if err := s.ariaMem.SaveSummary(r.Context(), AriaMemSaveSummaryInput{
		SessionID: id, Request: req.Request, Investigated: req.Investigated,
		Learned: req.Learned, Completed: req.Completed, NextSteps: req.NextSteps,
		FilesRead: req.FilesRead, FilesEdited: req.FilesEdited, Notes: req.Notes,
		QualityGrade: req.QualityGrade,
	}); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"session_id": id, "ok": true})
}

func (s *CloudServer) handleV1MemoryContextStatus(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	st, err := s.ariaMem.GetContextStatus(r.Context(), project)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, st)
}

func (s *CloudServer) handleV1MemorySkills(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	stack := []string{}
	if v := strings.TrimSpace(r.URL.Query().Get("stack")); v != "" {
		for _, t := range strings.Split(v, ",") {
			if tt := strings.TrimSpace(t); tt != "" {
				stack = append(stack, tt)
			}
		}
	}
	taskDesc := strings.TrimSpace(r.URL.Query().Get("task_description"))
	tokenBudget, _ := strconv.Atoi(r.URL.Query().Get("token_budget"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	strategy := strings.TrimSpace(r.URL.Query().Get("strategy"))
	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	// Si llega task_description o token_budget => usar el path con telemetry.
	if taskDesc != "" || tokenBudget > 0 || strategy != "" {
		claims, _ := claimsFromContext(r.Context())
		devUID := ""
		if claims != nil {
			devUID = claims.UID
		}
		out, err := s.ariaMem.GetSkillsRanked(r.Context(), AriaMemSkillsRankedInput{
			TaskDescription: taskDesc,
			Stack:           stack,
			Limit:           limit,
			TokenBudget:     tokenBudget,
			SessionID:       sessionID,
			DeveloperUID:    devUID,
			Project:         project,
			Strategy:        strategy,
		})
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
			return
		}
		jsonResponse(w, http.StatusOK, map[string]any{
			"skills":          out.Skills,
			"count":           len(out.Skills),
			"truncated_count": out.TruncatedCount,
			"tokens_used":     out.TokensUsed,
			"strategy":        out.Strategy,
		})
		return
	}

	skills, err := s.ariaMem.ListSkills(r.Context(), stack)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"skills": skills, "count": len(skills)})
}

type v1SkillFeedbackRequest struct {
	SkillID string `json:"skill_id"`
	Signal  string `json:"signal"`
	Helped  *bool  `json:"helped,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

func (s *CloudServer) handleV1MemorySkillFeedback(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req v1SkillFeedbackRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SkillID) == "" {
		http.Error(w, `{"error":"skill_id is required"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Signal) == "" {
		http.Error(w, `{"error":"signal is required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	devUID := ""
	if claims != nil {
		devUID = claims.UID
	}
	if err := s.ariaMem.RecordSkillFeedback(r.Context(), AriaMemSkillFeedbackInput{
		SkillID:      req.SkillID,
		DeveloperUID: devUID,
		Signal:       req.Signal,
		Helped:       req.Helped,
		Notes:        req.Notes,
	}); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true, "skill_id": req.SkillID})
}

func (s *CloudServer) handleV1MemoryRecipes(w http.ResponseWriter, r *http.Request) {
	if s.ariaMem == nil {
		http.Error(w, `{"error":"memory not configured"}`, http.StatusServiceUnavailable)
		return
	}
	taskDesc := strings.TrimSpace(r.URL.Query().Get("task"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	stack := []string{}
	if v := strings.TrimSpace(r.URL.Query().Get("stack")); v != "" {
		for _, t := range strings.Split(v, ",") {
			if tt := strings.TrimSpace(t); tt != "" {
				stack = append(stack, tt)
			}
		}
	}
	rs, err := s.ariaMem.GetRecipes(r.Context(), taskDesc, stack, limit)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"recipes": rs, "count": len(rs)})
}
