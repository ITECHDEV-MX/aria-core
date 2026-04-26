package cloudserver

import (
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
	in := AriaMemSearchInput{
		Query:           strings.TrimSpace(r.URL.Query().Get("q")),
		Project:         strings.TrimSpace(r.URL.Query().Get("project")),
		Scope:           strings.TrimSpace(r.URL.Query().Get("scope")),
		ObservationType: strings.TrimSpace(r.URL.Query().Get("type")),
		Limit:           limit,
	}
	rs, err := s.ariaMem.Search(r.Context(), in)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"results": rs, "count": len(rs)})
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
	Project   string `json:"project,omitempty"`
	Directory string `json:"directory,omitempty"`
	Goal      string `json:"goal,omitempty"`
	ClientID  string `json:"client_id,omitempty"`
	MachineID string `json:"machine_id,omitempty"`
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
	jsonResponse(w, http.StatusCreated, sess)
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
	skills, err := s.ariaMem.ListSkills(r.Context(), stack)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"skills": skills, "count": len(skills)})
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
