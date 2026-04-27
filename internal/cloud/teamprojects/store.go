// Package teamprojects implementa la plataforma de gestión de proyectos del
// equipo iTechDev (wave 7). Reemplaza la fragmentación entre GitHub
// (código), Notion (PRDs), Slack (discusión) y "Mis Tareas" inexistente.
//
// Contrato:
//   - Project: contenedor opcional cliente, repo GitHub auto-creado.
//   - Member: usuario cloud_users + role (owner|lead|member|viewer).
//   - Task: Kanban con status/priority/asignados/labels/parent.
//   - Comment / Observation link / Session link: knowledge per task.
package teamprojects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// Errores públicos del store.
var (
	ErrNotFound     = errors.New("teamprojects: not found")
	ErrInvalidInput = errors.New("teamprojects: invalid input")
	ErrConflict     = errors.New("teamprojects: conflict")
)

// Project es la representación pública de un proyecto del equipo.
type Project struct {
	ID                  string
	Slug                string
	Name                string
	Description         string
	ClientID            string
	Status              string
	GitHubRepoURL       string
	GitHubRepoOwner     string
	GitHubRepoName      string
	GitHubRepoPrivate   bool
	GitHubDefaultBranch string
	CreatedByUID        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
	ArchivedAt          *time.Time
}

// CreateProjectParams es el input de CreateProject.
type CreateProjectParams struct {
	Slug                string // si vacío, se deriva del Name
	Name                string
	Description         string
	ClientID            string
	GitHubRepoURL       string
	GitHubRepoOwner     string
	GitHubRepoName      string
	GitHubRepoPrivate   bool
	GitHubDefaultBranch string
	CreatedByUID        string
}

// UpdateProjectParams agrupa updates parciales.
type UpdateProjectParams struct {
	Name            *string
	Description     *string
	ClientID        *string
	Status          *string
	GitHubRepoURL   *string
	GitHubRepoOwner *string
	GitHubRepoName  *string
}

// ListFilter filtra ListProjects.
type ListFilter struct {
	Status        string // active|paused|archived|all (default active)
	OnlyMemberOf  string // si != "", filtrar a proyectos donde user es member
	Limit         int
}

// ProjectMember representa una membresía.
type ProjectMember struct {
	ProjectID  string
	UserUID    string
	Role       string
	AddedByUID string
	AddedAt    time.Time
}

// Task es una tarea Kanban.
type Task struct {
	ID                string
	ProjectID         string
	Title             string
	DescriptionMD     string
	Status            string
	Priority          string
	DueDate           *time.Time
	EstimateHours     float64
	SpentHours        float64
	GitHubIssueNumber int
	ParentTaskID      string
	Position          int
	Labels            []string
	CreatedByUID      string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ClosedAt          *time.Time
	ClosedByUID       string
}

// CreateTaskParams es el input de CreateTask.
type CreateTaskParams struct {
	ProjectID         string
	Title             string
	DescriptionMD     string
	Priority          string
	DueDate           *time.Time
	EstimateHours     float64
	GitHubIssueNumber int
	ParentTaskID      string
	Labels            []string
	CreatedByUID      string
}

// TaskFilter filtra ListTasks*.
type TaskFilter struct {
	Status   string // todo|in_progress|review|done|cancelled|all
	Priority string
	Limit    int
}

// TaskObservationLink agrupa el link entre task y observación memorística.
type TaskObservationLink struct {
	TaskID        string
	ObservationID string
	LinkType      string
	LinkedByUID   string
	LinkedAt      time.Time
}

// TaskSessionLink representa una sesión Claude Code linkeada.
type TaskSessionLink struct {
	TaskID    string
	SessionID string
	LinkedAt  time.Time
}

// TaskComment representa un comentario.
type TaskComment struct {
	ID        string
	TaskID    string
	AuthorUID string
	ContentMD string
	CreatedAt time.Time
}

// ProjectStore es la API pública del paquete.
type ProjectStore interface {
	CreateProject(ctx context.Context, params CreateProjectParams) (*Project, error)
	GetProject(ctx context.Context, id string) (*Project, error)
	GetProjectBySlug(ctx context.Context, slug string) (*Project, error)
	ListProjects(ctx context.Context, filter ListFilter) ([]*Project, error)
	UpdateProject(ctx context.Context, id string, updates UpdateProjectParams) error
	ArchiveProject(ctx context.Context, id string) error

	AddMember(ctx context.Context, projectID, userUID, role, byUID string) error
	RemoveMember(ctx context.Context, projectID, userUID string) error
	ListMembers(ctx context.Context, projectID string) ([]ProjectMember, error)
	IsMember(ctx context.Context, projectID, userUID string) (bool, error)

	CreateTask(ctx context.Context, params CreateTaskParams) (*Task, error)
	GetTask(ctx context.Context, taskID string) (*Task, error)
	ListTasksByProject(ctx context.Context, projectID string, filter TaskFilter) ([]*Task, error)
	ListTasksAssignedTo(ctx context.Context, userUID string, filter TaskFilter) ([]*Task, error)
	UpdateTaskStatus(ctx context.Context, taskID, status, byUID string) error
	UpdateTaskPosition(ctx context.Context, taskID string, newPosition int) error
	AssignTask(ctx context.Context, taskID, userUID, byUID string) error
	UnassignTask(ctx context.Context, taskID, userUID string) error
	CloseTask(ctx context.Context, taskID, byUID string) error
	ListTaskAssignees(ctx context.Context, taskID string) ([]string, error)

	LinkObservation(ctx context.Context, taskID, observationID, linkType, byUID string) error
	UnlinkObservation(ctx context.Context, taskID, observationID, linkType string) error
	ListLinkedObservations(ctx context.Context, taskID string) ([]TaskObservationLink, error)

	LinkSession(ctx context.Context, taskID, sessionID string) error
	ListLinkedSessions(ctx context.Context, taskID string) ([]TaskSessionLink, error)

	AddComment(ctx context.Context, taskID, authorUID, content string) (*TaskComment, error)
	ListComments(ctx context.Context, taskID string) ([]TaskComment, error)
}

// PgStore es la implementación Postgres.
type PgStore struct {
	db *sql.DB
}

// NewPgStore construye el store contra el sql.DB dado.
func NewPgStore(db *sql.DB) *PgStore { return &PgStore{db: db} }

// DB expone el handler para diagnóstico.
func (s *PgStore) DB() *sql.DB { return s.db }

// ─── Project CRUD ────────────────────────────────────────────────────────

var slugRe = regexp.MustCompile(`[^a-z0-9-]+`)

// SlugFromName deriva un slug lowercase con dashes a partir del name.
// Reglas: lowercase, espacios→dash, drop caracteres no [a-z0-9-], collapse dashes.
func SlugFromName(name string) string {
	s := strings.ToLower(strings.TrimSpace(name))
	s = strings.ReplaceAll(s, " ", "-")
	s = slugRe.ReplaceAllString(s, "")
	s = regexp.MustCompile(`-+`).ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "project-" + uuid.NewString()[:8]
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// CreateProject inserta un nuevo proyecto del equipo.
func (s *PgStore) CreateProject(ctx context.Context, p CreateProjectParams) (*Project, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("teamprojects: store not initialized")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, fmt.Errorf("%w: name required", ErrInvalidInput)
	}
	if p.CreatedByUID == "" {
		return nil, fmt.Errorf("%w: created_by_uid required", ErrInvalidInput)
	}
	slug := SlugFromName(p.Slug)
	if slug == "" || strings.HasPrefix(slug, "project-") {
		slug = SlugFromName(name)
	}
	branch := strings.TrimSpace(p.GitHubDefaultBranch)
	if branch == "" {
		branch = "main"
	}

	id := uuid.NewString()
	const q = `
		INSERT INTO aria_team_projects (
			id, slug, name, description, client_id, status,
			github_repo_url, github_repo_owner, github_repo_name,
			github_repo_private, github_default_branch, created_by_uid
		) VALUES (
			$1::uuid, $2, $3, NULLIF($4,''), $5, 'active',
			NULLIF($6,''), NULLIF($7,''), NULLIF($8,''),
			$9, $10, $11::uuid
		)
		RETURNING id::text, slug, name, COALESCE(description,''),
		          COALESCE(client_id::text,''), status,
		          COALESCE(github_repo_url,''), COALESCE(github_repo_owner,''), COALESCE(github_repo_name,''),
		          github_repo_private, github_default_branch,
		          created_by_uid::text, created_at, updated_at, archived_at`

	row := s.db.QueryRowContext(ctx, q,
		id, slug, name, p.Description, nullableUUIDArg(p.ClientID),
		p.GitHubRepoURL, p.GitHubRepoOwner, p.GitHubRepoName,
		p.GitHubRepoPrivate, branch, p.CreatedByUID,
	)
	pr, err := scanProject(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: slug %q ya existe", ErrConflict, slug)
		}
		return nil, fmt.Errorf("teamprojects: create project: %w", err)
	}
	// Auto-add creator as owner.
	if err := s.AddMember(ctx, pr.ID, p.CreatedByUID, "owner", p.CreatedByUID); err != nil {
		// rollback project?
		return nil, fmt.Errorf("teamprojects: add creator as owner: %w", err)
	}
	return pr, nil
}

const projectSelectCols = `
	id::text, slug, name, COALESCE(description,''),
	COALESCE(client_id::text,''), status,
	COALESCE(github_repo_url,''), COALESCE(github_repo_owner,''), COALESCE(github_repo_name,''),
	github_repo_private, github_default_branch,
	created_by_uid::text, created_at, updated_at, archived_at`

// GetProject retorna un proyecto por ID.
func (s *PgStore) GetProject(ctx context.Context, id string) (*Project, error) {
	if !isUUID(id) {
		return nil, fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+projectSelectCols+` FROM aria_team_projects WHERE id = $1::uuid`, id)
	pr, err := scanProject(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return pr, nil
}

// GetProjectBySlug retorna un proyecto por slug.
func (s *PgStore) GetProjectBySlug(ctx context.Context, slug string) (*Project, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, fmt.Errorf("%w: slug required", ErrInvalidInput)
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+projectSelectCols+` FROM aria_team_projects WHERE slug = $1`, slug)
	pr, err := scanProject(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return pr, nil
}

// ListProjects lista proyectos con filtros simples.
func (s *PgStore) ListProjects(ctx context.Context, f ListFilter) ([]*Project, error) {
	conds := []string{}
	args := []any{}
	idx := 1
	status := strings.TrimSpace(f.Status)
	if status == "" {
		status = "active"
	}
	if status != "all" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, status)
		idx++
	}
	if f.OnlyMemberOf != "" {
		conds = append(conds, fmt.Sprintf("id IN (SELECT project_id FROM aria_team_project_members WHERE user_uid = $%d::uuid)", idx))
		args = append(args, f.OnlyMemberOf)
		idx++
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + projectSelectCols + ` FROM aria_team_projects`
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}
	q += fmt.Sprintf(" ORDER BY created_at DESC LIMIT %d", limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("teamprojects: list projects: %w", err)
	}
	defer rows.Close()
	out := []*Project{}
	for rows.Next() {
		pr, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

// UpdateProject aplica updates parciales.
func (s *PgStore) UpdateProject(ctx context.Context, id string, u UpdateProjectParams) error {
	if !isUUID(id) {
		return fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	sets := []string{"updated_at = NOW()"}
	args := []any{id}
	idx := 2
	if u.Name != nil {
		sets = append(sets, fmt.Sprintf("name = $%d", idx))
		args = append(args, strings.TrimSpace(*u.Name))
		idx++
	}
	if u.Description != nil {
		sets = append(sets, fmt.Sprintf("description = NULLIF($%d,'')", idx))
		args = append(args, *u.Description)
		idx++
	}
	if u.ClientID != nil {
		sets = append(sets, fmt.Sprintf("client_id = $%d", idx))
		args = append(args, nullableUUIDArg(*u.ClientID))
		idx++
	}
	if u.Status != nil {
		sets = append(sets, fmt.Sprintf("status = $%d", idx))
		args = append(args, *u.Status)
		idx++
	}
	if u.GitHubRepoURL != nil {
		sets = append(sets, fmt.Sprintf("github_repo_url = NULLIF($%d,'')", idx))
		args = append(args, strings.TrimSpace(*u.GitHubRepoURL))
		idx++
	}
	if u.GitHubRepoOwner != nil {
		sets = append(sets, fmt.Sprintf("github_repo_owner = NULLIF($%d,'')", idx))
		args = append(args, strings.TrimSpace(*u.GitHubRepoOwner))
		idx++
	}
	if u.GitHubRepoName != nil {
		sets = append(sets, fmt.Sprintf("github_repo_name = NULLIF($%d,'')", idx))
		args = append(args, strings.TrimSpace(*u.GitHubRepoName))
		idx++
	}
	q := "UPDATE aria_team_projects SET " + strings.Join(sets, ", ") + " WHERE id = $1::uuid"
	res, err := s.db.ExecContext(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("teamprojects: update project: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ArchiveProject marca proyecto como archived.
func (s *PgStore) ArchiveProject(ctx context.Context, id string) error {
	if !isUUID(id) {
		return fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE aria_team_projects SET status='archived', archived_at=NOW(), updated_at=NOW()
		WHERE id = $1::uuid`, id)
	if err != nil {
		return fmt.Errorf("teamprojects: archive: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Members ─────────────────────────────────────────────────────────────

// AddMember agrega un usuario al proyecto. Idempotente: si ya existe, hace UPDATE del role.
func (s *PgStore) AddMember(ctx context.Context, projectID, userUID, role, byUID string) error {
	if !isUUID(projectID) || !isUUID(userUID) {
		return fmt.Errorf("%w: project_id y user_uid deben ser uuid", ErrInvalidInput)
	}
	role = strings.TrimSpace(role)
	if role == "" {
		role = "member"
	}
	switch role {
	case "owner", "lead", "member", "viewer":
	default:
		return fmt.Errorf("%w: role %q inválido", ErrInvalidInput, role)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_team_project_members (project_id, user_uid, role, added_by_uid)
		VALUES ($1::uuid, $2::uuid, $3, $4::uuid)
		ON CONFLICT (project_id, user_uid) DO UPDATE SET role = EXCLUDED.role`,
		projectID, userUID, role, nullableUUIDArg(byUID))
	if err != nil {
		return fmt.Errorf("teamprojects: add member: %w", err)
	}
	return nil
}

// RemoveMember elimina la membresía.
func (s *PgStore) RemoveMember(ctx context.Context, projectID, userUID string) error {
	if !isUUID(projectID) || !isUUID(userUID) {
		return fmt.Errorf("%w: project_id y user_uid deben ser uuid", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM aria_team_project_members WHERE project_id=$1::uuid AND user_uid=$2::uuid`, projectID, userUID)
	if err != nil {
		return fmt.Errorf("teamprojects: remove member: %w", err)
	}
	return nil
}

// ListMembers retorna los miembros del proyecto.
func (s *PgStore) ListMembers(ctx context.Context, projectID string) ([]ProjectMember, error) {
	if !isUUID(projectID) {
		return nil, fmt.Errorf("%w: project_id must be uuid", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT project_id::text, user_uid::text, role, COALESCE(added_by_uid::text,''), added_at
		FROM aria_team_project_members WHERE project_id = $1::uuid ORDER BY added_at ASC`, projectID)
	if err != nil {
		return nil, fmt.Errorf("teamprojects: list members: %w", err)
	}
	defer rows.Close()
	out := []ProjectMember{}
	for rows.Next() {
		var m ProjectMember
		if err := rows.Scan(&m.ProjectID, &m.UserUID, &m.Role, &m.AddedByUID, &m.AddedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// IsMember reporta si user es miembro del proyecto.
func (s *PgStore) IsMember(ctx context.Context, projectID, userUID string) (bool, error) {
	if !isUUID(projectID) || !isUUID(userUID) {
		return false, fmt.Errorf("%w: ids must be uuid", ErrInvalidInput)
	}
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM aria_team_project_members WHERE project_id=$1::uuid AND user_uid=$2::uuid`, projectID, userUID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// ─── Helpers ─────────────────────────────────────────────────────────────

// scanRow es la interfaz común para QueryRow y Rows.
type scanRow interface {
	Scan(dest ...any) error
}

func scanProject(r scanRow) (*Project, error) {
	var p Project
	var archivedAt sql.NullTime
	err := r.Scan(
		&p.ID, &p.Slug, &p.Name, &p.Description,
		&p.ClientID, &p.Status,
		&p.GitHubRepoURL, &p.GitHubRepoOwner, &p.GitHubRepoName,
		&p.GitHubRepoPrivate, &p.GitHubDefaultBranch,
		&p.CreatedByUID, &p.CreatedAt, &p.UpdatedAt, &archivedAt,
	)
	if err != nil {
		return nil, err
	}
	if archivedAt.Valid {
		t := archivedAt.Time
		p.ArchivedAt = &t
	}
	return &p, nil
}

func nullableUUIDArg(v string) any {
	v = strings.TrimSpace(v)
	if v == "" || !isUUID(v) {
		return sql.NullString{}
	}
	return v
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUID(s string) bool { return uuidRe.MatchString(strings.TrimSpace(s)) }

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	var pqErr *pq.Error
	if errors.As(err, &pqErr) {
		return pqErr.Code == "23505"
	}
	return strings.Contains(err.Error(), "duplicate key")
}
