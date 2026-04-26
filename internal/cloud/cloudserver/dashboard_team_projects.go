// Dashboard handlers for /dashboard/projects/team/* (wave 7).
// Renders raw HTML wrapped in dashboard.Layout via templ.Raw — same pattern
// used by dashboard_recipes.go to avoid creating a new .templ file.
package cloudserver

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/teamprojects"
	"github.com/a-h/templ"
)

// TeamProjectsService es el contrato runtime que cloudserver consume del
// adapter para evitar import directo de teamprojects.PgStore desde cloudserver
// sin ciclo (teamprojects ya es leaf). Usamos el store directamente.
type TeamProjectsService interface {
	CreateProject(ctx context.Context, params teamprojects.CreateProjectParams) (*teamprojects.Project, error)
	GetProject(ctx context.Context, id string) (*teamprojects.Project, error)
	GetProjectBySlug(ctx context.Context, slug string) (*teamprojects.Project, error)
	ListProjects(ctx context.Context, filter teamprojects.ListFilter) ([]*teamprojects.Project, error)
	UpdateProject(ctx context.Context, id string, updates teamprojects.UpdateProjectParams) error
	ArchiveProject(ctx context.Context, id string) error
	AddMember(ctx context.Context, projectID, userUID, role, byUID string) error
	RemoveMember(ctx context.Context, projectID, userUID string) error
	ListMembers(ctx context.Context, projectID string) ([]teamprojects.ProjectMember, error)
	IsMember(ctx context.Context, projectID, userUID string) (bool, error)

	CreateTask(ctx context.Context, params teamprojects.CreateTaskParams) (*teamprojects.Task, error)
	GetTask(ctx context.Context, taskID string) (*teamprojects.Task, error)
	ListTasksByProject(ctx context.Context, projectID string, filter teamprojects.TaskFilter) ([]*teamprojects.Task, error)
	ListTasksAssignedTo(ctx context.Context, userUID string, filter teamprojects.TaskFilter) ([]*teamprojects.Task, error)
	UpdateTaskStatus(ctx context.Context, taskID, status, byUID string) error
	UpdateTaskPosition(ctx context.Context, taskID string, newPosition int) error
	AssignTask(ctx context.Context, taskID, userUID, byUID string) error
	UnassignTask(ctx context.Context, taskID, userUID string) error
	CloseTask(ctx context.Context, taskID, byUID string) error
	ListTaskAssignees(ctx context.Context, taskID string) ([]string, error)
	LinkObservation(ctx context.Context, taskID, observationID, linkType, byUID string) error
	UnlinkObservation(ctx context.Context, taskID, observationID, linkType string) error
	ListLinkedObservations(ctx context.Context, taskID string) ([]teamprojects.TaskObservationLink, error)
	LinkSession(ctx context.Context, taskID, sessionID string) error
	ListLinkedSessions(ctx context.Context, taskID string) ([]teamprojects.TaskSessionLink, error)
	AddComment(ctx context.Context, taskID, authorUID, content string) (*teamprojects.TaskComment, error)
	ListComments(ctx context.Context, taskID string) ([]teamprojects.TaskComment, error)
	CaptureKnowledge(ctx context.Context, taskID string, opts teamprojects.CaptureKnowledgeOptions) (*teamprojects.KnowledgeCapture, error)
}

// TeamProjectsCreateAdapter wraps a higher-level "create with GitHub repo + warning if not configured" flow.
type TeamProjectsCreateAdapter interface {
	CreateProjectWithRepo(ctx context.Context, in CreateTeamProjectInput) (*teamprojects.Project, string, error)
}

// CreateTeamProjectInput agrupa el input del flujo "crear proyecto + intentar repo".
type CreateTeamProjectInput struct {
	Name         string
	Slug         string
	Description  string
	ClientID     string
	NoGitHub     bool
	CreatedByUID string
}

// WithTeamProjects inyecta el servicio runtime y el create-with-repo adapter.
func WithTeamProjects(svc TeamProjectsService, createAdapter TeamProjectsCreateAdapter) Option {
	return func(s *CloudServer) {
		s.teamProjects = svc
		s.teamProjectsCreate = createAdapter
	}
}

// mountTeamProjectsDashboard agrega las rutas del módulo wave 7.
func (s *CloudServer) mountTeamProjectsDashboard() {
	if s.teamProjects == nil {
		return
	}
	guard := func(roles []string, next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := s.authorizeDashboardRequest(r); err != nil {
				http.Redirect(w, r, "/dashboard/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
				return
			}
			if len(roles) > 0 {
				userRoles := s.dashboardRolesFromRequest(r)
				ok := false
				for _, want := range roles {
					for _, have := range userRoles {
						if strings.EqualFold(want, have) {
							ok = true
							break
						}
					}
					if ok {
						break
					}
				}
				if !ok {
					http.Error(w, "forbidden", http.StatusForbidden)
					return
				}
			}
			next(w, r)
		}
	}
	allowedRoles := []string{"admin", "dev", "project_admin"}

	s.mux.HandleFunc("GET /dashboard/projects/team", guard(allowedRoles, s.handleTeamProjectsList))
	s.mux.HandleFunc("GET /dashboard/projects/team/new", guard(allowedRoles, s.handleTeamProjectsNew))
	s.mux.HandleFunc("POST /dashboard/projects/team", guard(allowedRoles, s.handleTeamProjectsCreate))
	s.mux.HandleFunc("GET /dashboard/projects/team/{id}", guard(allowedRoles, s.handleTeamProjectDetail))
	s.mux.HandleFunc("GET /dashboard/projects/team/{id}/members", guard(allowedRoles, s.handleTeamProjectMembers))
	s.mux.HandleFunc("POST /dashboard/projects/team/{id}/members", guard(allowedRoles, s.handleTeamProjectAddMember))
	s.mux.HandleFunc("POST /dashboard/projects/team/{id}/members/{uid}/remove", guard(allowedRoles, s.handleTeamProjectRemoveMember))
	s.mux.HandleFunc("GET /dashboard/projects/team/{id}/tasks", guard(allowedRoles, s.handleTeamProjectKanban))
	s.mux.HandleFunc("POST /dashboard/projects/team/{id}/tasks", guard(allowedRoles, s.handleTeamProjectCreateTask))
	s.mux.HandleFunc("GET /dashboard/projects/team/{id}/tasks/{taskID}", guard(allowedRoles, s.handleTeamTaskDetail))
	s.mux.HandleFunc("POST /dashboard/tasks/{id}/status", guard(allowedRoles, s.handleTeamTaskStatusChange))
	s.mux.HandleFunc("POST /dashboard/tasks/{id}/assign", guard(allowedRoles, s.handleTeamTaskAssign))
	s.mux.HandleFunc("POST /dashboard/tasks/{id}/close", guard(allowedRoles, s.handleTeamTaskClose))
	s.mux.HandleFunc("POST /dashboard/tasks/{id}/comments", guard(allowedRoles, s.handleTeamTaskComment))
	s.mux.HandleFunc("GET /dashboard/projects/team/{id}/prds", guard(allowedRoles, s.handleTeamProjectPRDs))

	// Cockpit personal: tab "Mis tareas" — lista tasks abiertas asignadas al user.
	s.mux.HandleFunc("GET /dashboard/me/tasks", guard(nil, s.handleMyTasks))
}

func (s *CloudServer) handleMyTasks(w http.ResponseWriter, r *http.Request) {
	uid := s.uidFromRequest(r)
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	tasks, err := s.teamProjects.ListTasksAssignedTo(r.Context(), uid, teamprojects.TaskFilter{Limit: 200})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Group by project ID.
	byProject := map[string][]*teamprojects.Task{}
	for _, t := range tasks {
		byProject[t.ProjectID] = append(byProject[t.ProjectID], t)
	}
	var b strings.Builder
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(`<p class="section-kicker">MIS TAREAS</p>`)
	b.WriteString(`<h2>Mis tareas asignadas</h2>`)
	b.WriteString(`<p>Tareas activas (todo · in_progress · review) en proyectos donde sos miembro.</p>`)
	if len(tasks) == 0 {
		b.WriteString(`<p class="muted">Sin tareas asignadas. ¡Buen trabajo!</p>`)
	} else {
		for projectID, list := range byProject {
			pr, _ := s.teamProjects.GetProject(r.Context(), projectID)
			projName := projectID
			projSlug := ""
			if pr != nil {
				projName = pr.Name
				projSlug = pr.Slug
			}
			b.WriteString(fmt.Sprintf(`<h3><a href="/dashboard/projects/team/%s">%s</a> <small class="muted">%s</small></h3>`,
				html.EscapeString(projectID), html.EscapeString(projName), html.EscapeString(projSlug)))
			b.WriteString(`<ul>`)
			for _, t := range list {
				urgency := ""
				if t.DueDate != nil && time.Until(*t.DueDate) < 72*time.Hour {
					urgency = ` <span style="color:#f44">⏰ urgente</span>`
				}
				b.WriteString(fmt.Sprintf(`<li><a href="/dashboard/projects/team/%s/tasks/%s">%s</a> · %s · %s%s</li>`,
					html.EscapeString(projectID), html.EscapeString(t.ID), html.EscapeString(t.Title),
					html.EscapeString(t.Status), html.EscapeString(t.Priority), urgency))
			}
			b.WriteString(`</ul>`)
		}
	}
	b.WriteString(`<p><a href="/dashboard/me">← Volver al cockpit</a></p>`)
	b.WriteString(`</section>`)
	renderTeamProjectsLayout(w, r, "Mis tareas", displayName, roles, b.String())
}

// ─── Handlers ───────────────────────────────────────────────────────────

func (s *CloudServer) handleTeamProjectsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	uid := s.uidFromRequest(r)

	statusFilter := strings.TrimSpace(r.URL.Query().Get("status"))
	if statusFilter == "" {
		statusFilter = "active"
	}
	filter := teamprojects.ListFilter{Status: statusFilter}
	// Non-admin: solo proyectos donde es miembro.
	if !isAdminContext(roles) {
		filter.OnlyMemberOf = uid
	}
	projects, err := s.teamProjects.ListProjects(ctx, filter)
	if err != nil {
		http.Error(w, "list error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	var b strings.Builder
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(`<p class="section-kicker">PROYECTOS DEL EQUIPO</p>`)
	b.WriteString(`<h2>Proyectos del equipo iTechDev</h2>`)
	b.WriteString(`<p>Plataforma de gestión: PRDs · Tasks Kanban · GitHub repos · Knowledge per task</p>`)
	b.WriteString(`<div style="margin-bottom:1rem;display:flex;gap:1rem;align-items:center">`)
	b.WriteString(fmt.Sprintf(`<a href="/dashboard/projects/team/new" class="shell-button">+ Nuevo proyecto</a>`))
	b.WriteString(`<form method="get" style="margin:0">`)
	b.WriteString(`<select name="status" onchange="this.form.submit()">`)
	for _, st := range []string{"active", "paused", "archived", "all"} {
		sel := ""
		if st == statusFilter {
			sel = " selected"
		}
		b.WriteString(fmt.Sprintf(`<option value="%s"%s>%s</option>`, st, sel, st))
	}
	b.WriteString(`</select></form></div>`)

	if len(projects) == 0 {
		b.WriteString(`<p class="muted">Sin proyectos para mostrar.</p>`)
	} else {
		b.WriteString(`<table class="data-table"><thead><tr><th>Slug</th><th>Nombre</th><th>Status</th><th>GitHub</th><th>Creado</th><th></th></tr></thead><tbody>`)
		for _, pr := range projects {
			gh := "—"
			if pr.GitHubRepoURL != "" {
				gh = fmt.Sprintf(`<a href="%s" target="_blank" rel="noopener">%s/%s</a>`, html.EscapeString(pr.GitHubRepoURL), html.EscapeString(pr.GitHubRepoOwner), html.EscapeString(pr.GitHubRepoName))
			}
			b.WriteString(fmt.Sprintf(
				`<tr><td><code>%s</code></td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td><a href="/dashboard/projects/team/%s">Abrir</a></td></tr>`,
				html.EscapeString(pr.Slug), html.EscapeString(pr.Name), statusBadge(pr.Status), gh,
				pr.CreatedAt.Format("2006-01-02"), html.EscapeString(pr.ID),
			))
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</section>`)
	renderTeamProjectsLayout(w, r, "Proyectos del equipo", displayName, roles, b.String())
}

func (s *CloudServer) handleTeamProjectsNew(w http.ResponseWriter, r *http.Request) {
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	var b strings.Builder
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(`<p class="section-kicker">PROYECTOS DEL EQUIPO</p>`)
	b.WriteString(`<h2>Nuevo proyecto interno</h2>`)
	b.WriteString(`<form method="post" action="/dashboard/projects/team" class="frame-form" style="max-width:560px">`)
	b.WriteString(`<label>Nombre <input name="name" required/></label>`)
	b.WriteString(`<label>Slug (opcional, auto-derivado del nombre) <input name="slug" placeholder="auto"/></label>`)
	b.WriteString(`<label>Descripción <textarea name="description" rows="4"></textarea></label>`)
	b.WriteString(`<label>Cliente (UUID, opcional) <input name="client_id"/></label>`)
	b.WriteString(`<label><input type="checkbox" name="no_github" value="1"/> No crear repo GitHub</label>`)
	b.WriteString(`<button type="submit" class="shell-button">Crear proyecto</button>`)
	b.WriteString(`</form></section>`)
	renderTeamProjectsLayout(w, r, "Nuevo proyecto del equipo", displayName, roles, b.String())
}

func (s *CloudServer) handleTeamProjectsCreate(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	uid := s.uidFromRequest(r)
	in := CreateTeamProjectInput{
		Name:         strings.TrimSpace(r.FormValue("name")),
		Slug:         strings.TrimSpace(r.FormValue("slug")),
		Description:  strings.TrimSpace(r.FormValue("description")),
		ClientID:     strings.TrimSpace(r.FormValue("client_id")),
		NoGitHub:     r.FormValue("no_github") == "1",
		CreatedByUID: uid,
	}
	if in.Name == "" {
		http.Error(w, "nombre requerido", http.StatusBadRequest)
		return
	}
	var pr *teamprojects.Project
	var warning string
	var err error
	if s.teamProjectsCreate != nil {
		pr, warning, err = s.teamProjectsCreate.CreateProjectWithRepo(r.Context(), in)
	} else {
		// Sin adapter de GitHub: solo crear el proyecto plano.
		pr, err = s.teamProjects.CreateProject(r.Context(), teamprojects.CreateProjectParams{
			Slug:         in.Slug,
			Name:         in.Name,
			Description:  in.Description,
			ClientID:     in.ClientID,
			CreatedByUID: in.CreatedByUID,
		})
	}
	if err != nil {
		http.Error(w, "create error: "+err.Error(), http.StatusInternalServerError)
		return
	}
	target := "/dashboard/projects/team/" + pr.ID
	if warning != "" {
		target += "?warning=" + url_QueryEscape(warning)
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func (s *CloudServer) handleTeamProjectDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pr, err := s.teamProjects.GetProject(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	members, _ := s.teamProjects.ListMembers(r.Context(), pr.ID)
	tasks, _ := s.teamProjects.ListTasksByProject(r.Context(), pr.ID, teamprojects.TaskFilter{Limit: 500})

	openCount := 0
	doneCount := 0
	for _, t := range tasks {
		if t.Status == "done" {
			doneCount++
		} else if t.Status != "cancelled" {
			openCount++
		}
	}

	var b strings.Builder
	if w := r.URL.Query().Get("warning"); w != "" {
		b.WriteString(fmt.Sprintf(`<div class="alert alert-warning">%s</div>`, html.EscapeString(w)))
	}
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(fmt.Sprintf(`<p class="section-kicker">PROYECTO · %s</p>`, html.EscapeString(pr.Slug)))
	b.WriteString(fmt.Sprintf(`<h2>%s</h2>`, html.EscapeString(pr.Name)))
	if pr.Description != "" {
		b.WriteString(fmt.Sprintf(`<p>%s</p>`, html.EscapeString(pr.Description)))
	}
	b.WriteString(`<div class="metric-row" style="display:flex;gap:1.5rem;flex-wrap:wrap;margin:1rem 0">`)
	b.WriteString(fmt.Sprintf(`<div><strong>Status</strong><br>%s</div>`, statusBadge(pr.Status)))
	b.WriteString(fmt.Sprintf(`<div><strong>Tasks open</strong><br>%d</div>`, openCount))
	b.WriteString(fmt.Sprintf(`<div><strong>Tasks done</strong><br>%d</div>`, doneCount))
	b.WriteString(fmt.Sprintf(`<div><strong>Miembros</strong><br>%d</div>`, len(members)))
	if pr.GitHubRepoURL != "" {
		b.WriteString(fmt.Sprintf(`<div><strong>GitHub</strong><br><a href="%s" target="_blank" rel="noopener">%s/%s</a></div>`,
			html.EscapeString(pr.GitHubRepoURL), html.EscapeString(pr.GitHubRepoOwner), html.EscapeString(pr.GitHubRepoName)))
	} else {
		b.WriteString(`<div><strong>GitHub</strong><br><span class="muted">no configurado</span></div>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`<nav class="tab-nav" style="display:flex;gap:1rem;margin-bottom:1rem">`)
	b.WriteString(fmt.Sprintf(`<a href="/dashboard/projects/team/%s/tasks" class="shell-button">Kanban</a>`, pr.ID))
	b.WriteString(fmt.Sprintf(`<a href="/dashboard/projects/team/%s/members" class="shell-button">Miembros</a>`, pr.ID))
	b.WriteString(fmt.Sprintf(`<a href="/dashboard/projects/team/%s/prds" class="shell-button">PRDs</a>`, pr.ID))
	b.WriteString(`</nav>`)
	b.WriteString(`</section>`)

	renderTeamProjectsLayout(w, r, pr.Name, displayName, roles, b.String())
}

func (s *CloudServer) handleTeamProjectMembers(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pr, err := s.teamProjects.GetProject(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	members, _ := s.teamProjects.ListMembers(r.Context(), pr.ID)
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	var b strings.Builder
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(fmt.Sprintf(`<p class="section-kicker">PROYECTO · %s · MIEMBROS</p>`, html.EscapeString(pr.Slug)))
	b.WriteString(fmt.Sprintf(`<h2>%s — Miembros</h2>`, html.EscapeString(pr.Name)))
	b.WriteString(`<form method="post" action="/dashboard/projects/team/` + html.EscapeString(pr.ID) + `/members" class="frame-form">`)
	b.WriteString(`<label>UID del usuario <input name="user_uid" required/></label>`)
	b.WriteString(`<label>Role <select name="role">`)
	for _, role := range []string{"member", "lead", "owner", "viewer"} {
		b.WriteString(fmt.Sprintf(`<option value="%s">%s</option>`, role, role))
	}
	b.WriteString(`</select></label>`)
	b.WriteString(`<button class="shell-button">Agregar</button></form>`)
	b.WriteString(`<table class="data-table"><thead><tr><th>UID</th><th>Role</th><th>Agregado</th><th></th></tr></thead><tbody>`)
	for _, m := range members {
		b.WriteString(fmt.Sprintf(
			`<tr><td><code>%s</code></td><td>%s</td><td>%s</td><td><form method="post" action="/dashboard/projects/team/%s/members/%s/remove" style="margin:0"><button class="shell-button" type="submit">×</button></form></td></tr>`,
			html.EscapeString(truncateUID(m.UserUID)), html.EscapeString(m.Role), m.AddedAt.Format("2006-01-02"),
			html.EscapeString(pr.ID), html.EscapeString(m.UserUID),
		))
	}
	b.WriteString(`</tbody></table></section>`)
	renderTeamProjectsLayout(w, r, "Miembros", displayName, roles, b.String())
}

func (s *CloudServer) handleTeamProjectAddMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	uid := r.FormValue("user_uid")
	role := r.FormValue("role")
	byUID := s.uidFromRequest(r)
	if err := s.teamProjects.AddMember(r.Context(), id, uid, role, byUID); err != nil {
		http.Error(w, "add member: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/projects/team/"+id+"/members", http.StatusSeeOther)
}

func (s *CloudServer) handleTeamProjectRemoveMember(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := r.PathValue("uid")
	if err := s.teamProjects.RemoveMember(r.Context(), id, uid); err != nil {
		http.Error(w, "remove: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/projects/team/"+id+"/members", http.StatusSeeOther)
}

func (s *CloudServer) handleTeamProjectKanban(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pr, err := s.teamProjects.GetProject(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	tasks, _ := s.teamProjects.ListTasksByProject(r.Context(), pr.ID, teamprojects.TaskFilter{Limit: 500})
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)

	cols := map[string][]*teamprojects.Task{
		"todo":        {},
		"in_progress": {},
		"review":      {},
		"done":        {},
	}
	for _, t := range tasks {
		if t.Status == "cancelled" {
			continue
		}
		cols[t.Status] = append(cols[t.Status], t)
	}
	var b strings.Builder
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(fmt.Sprintf(`<p class="section-kicker">KANBAN · %s</p>`, html.EscapeString(pr.Slug)))
	b.WriteString(fmt.Sprintf(`<h2>%s — Kanban</h2>`, html.EscapeString(pr.Name)))
	// Form crear task
	b.WriteString(`<form method="post" action="/dashboard/projects/team/` + html.EscapeString(pr.ID) + `/tasks" class="frame-form" style="margin-bottom:1rem">`)
	b.WriteString(`<label>Título <input name="title" required style="min-width:300px"/></label>`)
	b.WriteString(`<label>Priority <select name="priority"><option>medium</option><option>low</option><option>high</option><option>urgent</option></select></label>`)
	b.WriteString(`<button class="shell-button">+ Task</button></form>`)

	b.WriteString(`<div class="kanban-board" style="display:grid;grid-template-columns:repeat(4,1fr);gap:1rem;align-items:flex-start">`)
	for _, col := range []struct{ id, label string }{
		{"todo", "Todo"}, {"in_progress", "In progress"}, {"review", "Review"}, {"done", "Done"},
	} {
		b.WriteString(fmt.Sprintf(`<div class="kanban-col" data-status="%s" style="background:rgba(255,255,255,0.04);padding:0.75rem;border-radius:8px"><h3>%s · %d</h3>`,
			col.id, col.label, len(cols[col.id])))
		for _, t := range cols[col.id] {
			urgency := ""
			if t.DueDate != nil && time.Until(*t.DueDate) < 72*time.Hour && t.Status != "done" {
				urgency = ` <span style="color:#f00" title="due en menos de 72h">⏰</span>`
			}
			b.WriteString(fmt.Sprintf(
				`<div class="kanban-card" data-task-id="%s" style="background:rgba(255,255,255,0.06);padding:0.5rem;margin-bottom:0.5rem;border-radius:4px"><a href="/dashboard/projects/team/%s/tasks/%s">%s</a> <small>(%s)</small>%s</div>`,
				html.EscapeString(t.ID), html.EscapeString(pr.ID), html.EscapeString(t.ID), html.EscapeString(t.Title), html.EscapeString(t.Priority), urgency))
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	b.WriteString(`</section>`)
	renderTeamProjectsLayout(w, r, pr.Name+" · Kanban", displayName, roles, b.String())
}

func (s *CloudServer) handleTeamProjectCreateTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	uid := s.uidFromRequest(r)
	_, err := s.teamProjects.CreateTask(r.Context(), teamprojects.CreateTaskParams{
		ProjectID:     id,
		Title:         r.FormValue("title"),
		DescriptionMD: r.FormValue("description"),
		Priority:      r.FormValue("priority"),
		CreatedByUID:  uid,
	})
	if err != nil {
		http.Error(w, "create task: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/projects/team/"+id+"/tasks", http.StatusSeeOther)
}

func (s *CloudServer) handleTeamTaskDetail(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	taskID := r.PathValue("taskID")
	pr, err := s.teamProjects.GetProject(r.Context(), id)
	if err != nil {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	t, err := s.teamProjects.GetTask(r.Context(), taskID)
	if err != nil {
		http.Error(w, "task not found", http.StatusNotFound)
		return
	}
	assignees, _ := s.teamProjects.ListTaskAssignees(r.Context(), taskID)
	comments, _ := s.teamProjects.ListComments(r.Context(), taskID)
	obs, _ := s.teamProjects.ListLinkedObservations(r.Context(), taskID)
	sess, _ := s.teamProjects.ListLinkedSessions(r.Context(), taskID)

	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	var b strings.Builder
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(fmt.Sprintf(`<p class="section-kicker"><a href="/dashboard/projects/team/%s">%s</a> · TASK</p>`, html.EscapeString(pr.ID), html.EscapeString(pr.Slug)))
	b.WriteString(fmt.Sprintf(`<h2>%s</h2>`, html.EscapeString(t.Title)))
	b.WriteString(fmt.Sprintf(`<p><strong>Status</strong>: %s · <strong>Priority</strong>: %s</p>`, statusBadge(t.Status), html.EscapeString(t.Priority)))
	if t.DescriptionMD != "" {
		b.WriteString(`<pre style="white-space:pre-wrap">`)
		b.WriteString(html.EscapeString(t.DescriptionMD))
		b.WriteString(`</pre>`)
	}

	// Status quick form
	b.WriteString(`<form method="post" action="/dashboard/tasks/` + html.EscapeString(t.ID) + `/status" class="frame-form" style="display:inline-block">`)
	b.WriteString(`<label>Cambiar status <select name="status">`)
	for _, st := range []string{"todo", "in_progress", "review", "done", "cancelled"} {
		sel := ""
		if st == t.Status {
			sel = " selected"
		}
		b.WriteString(fmt.Sprintf(`<option value="%s"%s>%s</option>`, st, sel, st))
	}
	b.WriteString(`</select></label><button class="shell-button">Guardar</button></form>`)

	// Close + capture knowledge
	if t.Status != "done" {
		b.WriteString(`<form method="post" action="/dashboard/tasks/` + html.EscapeString(t.ID) + `/close" class="frame-form" style="display:inline-block">`)
		b.WriteString(`<button class="shell-button" type="submit">✓ Cerrar + capturar knowledge</button></form>`)
	}

	// Assign form
	b.WriteString(`<h3>Asignados</h3><ul>`)
	for _, a := range assignees {
		b.WriteString(fmt.Sprintf(`<li><code>%s</code></li>`, html.EscapeString(truncateUID(a))))
	}
	b.WriteString(`</ul>`)
	b.WriteString(`<form method="post" action="/dashboard/tasks/` + html.EscapeString(t.ID) + `/assign" class="frame-form">`)
	b.WriteString(`<label>UID <input name="user_uid"/></label><button class="shell-button">Asignar</button></form>`)

	// Linked obs/sessions
	if len(obs) > 0 {
		b.WriteString(`<h3>Observaciones linked</h3><ul>`)
		for _, o := range obs {
			b.WriteString(fmt.Sprintf(`<li><code>%s</code> (%s)</li>`, html.EscapeString(o.ObservationID), html.EscapeString(o.LinkType)))
		}
		b.WriteString(`</ul>`)
	}
	if len(sess) > 0 {
		b.WriteString(`<h3>Sesiones Claude Code linked</h3><ul>`)
		for _, ss := range sess {
			b.WriteString(fmt.Sprintf(`<li><code>%s</code></li>`, html.EscapeString(ss.SessionID)))
		}
		b.WriteString(`</ul>`)
	}

	// Comments
	b.WriteString(`<h3>Comentarios</h3><div>`)
	for _, c := range comments {
		b.WriteString(fmt.Sprintf(`<div style="border-left:2px solid #555;padding:0.25rem 0.5rem;margin-bottom:0.5rem"><small><code>%s</code> · %s</small><div>%s</div></div>`,
			html.EscapeString(truncateUID(c.AuthorUID)), c.CreatedAt.Format("2006-01-02 15:04"), html.EscapeString(c.ContentMD)))
	}
	b.WriteString(`</div>`)
	b.WriteString(`<form method="post" action="/dashboard/tasks/` + html.EscapeString(t.ID) + `/comments" class="frame-form">`)
	b.WriteString(`<label>Comentario <textarea name="content" rows="3" required></textarea></label>`)
	b.WriteString(`<button class="shell-button">Comentar</button></form>`)

	b.WriteString(`</section>`)
	renderTeamProjectsLayout(w, r, t.Title, displayName, roles, b.String())
}

func (s *CloudServer) handleTeamTaskStatusChange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	status := r.FormValue("status")
	if err := s.teamProjects.UpdateTaskStatus(r.Context(), id, status, s.uidFromRequest(r)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t, _ := s.teamProjects.GetTask(r.Context(), id)
	if t != nil {
		http.Redirect(w, r, fmt.Sprintf("/dashboard/projects/team/%s/tasks/%s", t.ProjectID, t.ID), http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *CloudServer) handleTeamTaskAssign(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	uid := strings.TrimSpace(r.FormValue("user_uid"))
	by := s.uidFromRequest(r)
	if err := s.teamProjects.AssignTask(r.Context(), id, uid, by); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t, _ := s.teamProjects.GetTask(r.Context(), id)
	if t != nil {
		http.Redirect(w, r, fmt.Sprintf("/dashboard/projects/team/%s/tasks/%s", t.ProjectID, t.ID), http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *CloudServer) handleTeamTaskClose(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	uid := s.uidFromRequest(r)
	if err := s.teamProjects.CloseTask(r.Context(), id, uid); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Capture knowledge (best-effort, sin GH integration por default — adapter override).
	cap, err := s.teamProjects.CaptureKnowledge(r.Context(), id, teamprojects.CaptureKnowledgeOptions{LinkedByUID: uid})
	if err == nil && cap != nil {
		// Volcar como query param informativo.
		t, _ := s.teamProjects.GetTask(r.Context(), id)
		if t != nil {
			pl, _ := json.Marshal(map[string]any{
				"obs":      cap.ObservationsLinked,
				"sessions": cap.SessionsLinked,
				"commits":  cap.CommitsLinked,
			})
			http.Redirect(w, r, fmt.Sprintf("/dashboard/projects/team/%s/tasks/%s?captured=%s", t.ProjectID, t.ID, url_QueryEscape(string(pl))), http.StatusSeeOther)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *CloudServer) handleTeamTaskComment(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	uid := s.uidFromRequest(r)
	if _, err := s.teamProjects.AddComment(r.Context(), id, uid, r.FormValue("content")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	t, _ := s.teamProjects.GetTask(r.Context(), id)
	if t != nil {
		http.Redirect(w, r, fmt.Sprintf("/dashboard/projects/team/%s/tasks/%s", t.ProjectID, t.ID), http.StatusSeeOther)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *CloudServer) handleTeamProjectPRDs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	pr, err := s.teamProjects.GetProject(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	var b strings.Builder
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(fmt.Sprintf(`<p class="section-kicker">PROYECTO · %s · PRDs</p>`, html.EscapeString(pr.Slug)))
	b.WriteString(fmt.Sprintf(`<h2>%s — PRDs</h2>`, html.EscapeString(pr.Name)))
	b.WriteString(`<p>Documentos PRD asociados al proyecto. Los PRDs reusan el módulo Pages (template <code>prd-v1</code>) con filter por project=<code>` + html.EscapeString(pr.Slug) + `</code>.</p>`)
	b.WriteString(`<a class="shell-button" href="/dashboard/pages?project=` + url_QueryEscape(pr.Slug) + `&template=prd-v1">Abrir PRDs en Pages →</a>`)
	b.WriteString(`</section>`)
	renderTeamProjectsLayout(w, r, pr.Name+" · PRDs", displayName, roles, b.String())
}

// ─── helpers ───────────────────────────────────────────────────────────

func renderTeamProjectsLayout(w http.ResponseWriter, r *http.Request, title, displayName string, roles []string, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHTMXLikeRequest(r) {
		_, _ = w.Write([]byte(body))
		return
	}
	component := dashboard.Layout(title, displayName, "team-projects", roles, templ.Raw(body))
	if err := component.Render(r.Context(), w); err != nil {
		_, _ = w.Write([]byte(body))
	}
}

func (s *CloudServer) uidFromRequest(r *http.Request) string {
	claims, err := s.dashboardClaimsFromRequest(r)
	if err != nil || claims == nil {
		return ""
	}
	return claims.UID
}

// url_QueryEscape: alias to avoid importing net/url just for this.
func url_QueryEscape(s string) string {
	out := strings.Builder{}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			out.WriteByte(c)
		default:
			out.WriteString(fmt.Sprintf("%%%02X", c))
		}
	}
	return out.String()
}

// touch unused stdlib
var _ = strconv.Itoa
