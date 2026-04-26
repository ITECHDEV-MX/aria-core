// V1 REST endpoints for team-projects (wave 7).
// JWT user-bound. Cualquier user autenticado puede listar/crear/leer; auth
// específica por proyecto se aplica via membership en handlers más sensibles.
package cloudserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/teamprojects"
)

// uidFromCtx extrae el UID del JWT inyectado por withJWTAuth.
func uidFromCtx(ctx context.Context) string {
	c, ok := claimsFromContext(ctx)
	if !ok || c == nil {
		return ""
	}
	return c.UID
}

func (s *CloudServer) mountV1TeamProjects() {
	if s.teamProjects == nil {
		return
	}
	// Projects.
	s.mux.HandleFunc("GET /v1/team-projects", s.withJWTAuth(s.handleV1TeamProjectList))
	s.mux.HandleFunc("POST /v1/team-projects", s.withJWTAuth(s.handleV1TeamProjectCreate))
	s.mux.HandleFunc("GET /v1/team-projects/{id}", s.withJWTAuth(s.handleV1TeamProjectGet))
	s.mux.HandleFunc("POST /v1/team-projects/{id}/members", s.withJWTAuth(s.handleV1TeamProjectAddMember))
	s.mux.HandleFunc("GET /v1/team-projects/{id}/members", s.withJWTAuth(s.handleV1TeamProjectListMembers))
	// Tasks.
	s.mux.HandleFunc("GET /v1/team-projects/{id}/tasks", s.withJWTAuth(s.handleV1TaskListByProject))
	s.mux.HandleFunc("POST /v1/team-projects/{id}/tasks", s.withJWTAuth(s.handleV1TaskCreate))
	s.mux.HandleFunc("GET /v1/tasks/{taskID}", s.withJWTAuth(s.handleV1TaskGet))
	s.mux.HandleFunc("POST /v1/tasks/{taskID}/status", s.withJWTAuth(s.handleV1TaskUpdateStatus))
	s.mux.HandleFunc("POST /v1/tasks/{taskID}/assign", s.withJWTAuth(s.handleV1TaskAssign))
	s.mux.HandleFunc("POST /v1/tasks/{taskID}/close", s.withJWTAuth(s.handleV1TaskClose))
	s.mux.HandleFunc("POST /v1/tasks/{taskID}/link-observation", s.withJWTAuth(s.handleV1TaskLinkObservation))
	s.mux.HandleFunc("GET /v1/tasks/assigned-to-me", s.withJWTAuth(s.handleV1TaskListAssignedToMe))
}

func (s *CloudServer) handleV1TeamProjectList(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := atoiOr(r.URL.Query().Get("limit"), 50)
	out, err := s.teamProjects.ListProjects(r.Context(), teamprojects.ListFilter{Status: status, Limit: limit})
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"projects": out})
}

func (s *CloudServer) handleV1TeamProjectCreate(w http.ResponseWriter, r *http.Request) {
	uid := uidFromCtx(r.Context())
	if uid == "" {
		jsonResponse(w, http.StatusUnauthorized, map[string]any{"error": "missing uid"})
		return
	}
	var body struct {
		Name        string `json:"name"`
		Slug        string `json:"slug"`
		Description string `json:"description"`
		ClientID    string `json:"client_id"`
		NoGitHub    bool   `json:"no_github"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if strings.TrimSpace(body.Name) == "" {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": "name required"})
		return
	}
	var pr *teamprojects.Project
	var warning string
	var err error
	if s.teamProjectsCreate != nil {
		pr, warning, err = s.teamProjectsCreate.CreateProjectWithRepo(r.Context(), CreateTeamProjectInput{
			Name: body.Name, Slug: body.Slug, Description: body.Description,
			ClientID: body.ClientID, NoGitHub: body.NoGitHub, CreatedByUID: uid,
		})
	} else {
		pr, err = s.teamProjects.CreateProject(r.Context(), teamprojects.CreateProjectParams{
			Slug: body.Slug, Name: body.Name, Description: body.Description,
			ClientID: body.ClientID, CreatedByUID: uid,
		})
	}
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"project": pr, "warning": warning})
}

func (s *CloudServer) handleV1TeamProjectGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pr, err := s.teamProjects.GetProject(r.Context(), id)
	if err != nil {
		if errors.Is(err, teamprojects.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		jsonResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"project": pr})
}

func (s *CloudServer) handleV1TeamProjectAddMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := uidFromCtx(r.Context())
	var body struct {
		UserUID string `json:"user_uid"`
		Role    string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.teamProjects.AddMember(r.Context(), id, body.UserUID, body.Role, uid); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *CloudServer) handleV1TeamProjectListMembers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	out, err := s.teamProjects.ListMembers(r.Context(), id)
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"members": out})
}

func (s *CloudServer) handleV1TaskListByProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	status := r.URL.Query().Get("status")
	limit := atoiOr(r.URL.Query().Get("limit"), 100)
	out, err := s.teamProjects.ListTasksByProject(r.Context(), id, teamprojects.TaskFilter{Status: status, Limit: limit})
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"tasks": out})
}

func (s *CloudServer) handleV1TaskListAssignedToMe(w http.ResponseWriter, r *http.Request) {
	uid := uidFromCtx(r.Context())
	if uid == "" {
		jsonResponse(w, http.StatusUnauthorized, map[string]any{"error": "missing uid"})
		return
	}
	status := r.URL.Query().Get("status")
	out, err := s.teamProjects.ListTasksAssignedTo(r.Context(), uid, teamprojects.TaskFilter{Status: status, Limit: 200})
	if err != nil {
		jsonResponse(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"tasks": out})
}

func (s *CloudServer) handleV1TaskCreate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := uidFromCtx(r.Context())
	var body struct {
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Priority    string   `json:"priority"`
		Assignees   []string `json:"assignees"`
		Labels      []string `json:"labels"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	t, err := s.teamProjects.CreateTask(r.Context(), teamprojects.CreateTaskParams{
		ProjectID: id, Title: body.Title, DescriptionMD: body.Description,
		Priority: body.Priority, Labels: body.Labels, CreatedByUID: uid,
	})
	if err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	for _, a := range body.Assignees {
		_ = s.teamProjects.AssignTask(r.Context(), t.ID, a, uid)
	}
	jsonResponse(w, http.StatusOK, map[string]any{"task": t})
}

func (s *CloudServer) handleV1TaskGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("taskID")
	t, err := s.teamProjects.GetTask(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	assignees, _ := s.teamProjects.ListTaskAssignees(r.Context(), id)
	jsonResponse(w, http.StatusOK, map[string]any{"task": t, "assignees": assignees})
}

func (s *CloudServer) handleV1TaskUpdateStatus(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("taskID")
	uid := uidFromCtx(r.Context())
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.teamProjects.UpdateTaskStatus(r.Context(), id, body.Status, uid); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *CloudServer) handleV1TaskAssign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("taskID")
	by := uidFromCtx(r.Context())
	var body struct {
		UserUID string `json:"user_uid"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.teamProjects.AssignTask(r.Context(), id, body.UserUID, by); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *CloudServer) handleV1TaskClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("taskID")
	uid := uidFromCtx(r.Context())
	if err := s.teamProjects.CloseTask(r.Context(), id, uid); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	cap, capErr := s.teamProjects.CaptureKnowledge(r.Context(), id, teamprojects.CaptureKnowledgeOptions{LinkedByUID: uid})
	resp := map[string]any{"ok": true}
	if capErr == nil && cap != nil {
		resp["captured"] = cap
	}
	jsonResponse(w, http.StatusOK, resp)
}

func (s *CloudServer) handleV1TaskLinkObservation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("taskID")
	by := uidFromCtx(r.Context())
	var body struct {
		ObservationID string `json:"observation_id"`
		LinkType      string `json:"link_type"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	if err := s.teamProjects.LinkObservation(r.Context(), id, body.ObservationID, body.LinkType, by); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"ok": true})
}

// atoiOr parsea int desde string; si falla, retorna fallback.
func atoiOr(v string, fallback int) int {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
