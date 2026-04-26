// Package ariamem implementa la capa de memoria/conocimiento ARIA Core
// equivalente al MCP legacy mcp__aria__* (que vivía en ~/.aria/aria.db SQLite).
//
// Reemplaza el legacy con:
//   - Persistencia en Postgres (aria_core_cloud)
//   - Multi-tenant (developer_uid + client_id)
//   - JWT user-bound auth
//   - FTS spanish + accent-insensitive
//   - Topic_key con upsert (revisión de memorias por tema)
//   - Reasoning trace estructurado (jsonb)
//   - Canon promotion + quality records
package ariamem

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

const (
	ScopePersonal        = "personal"
	ScopeProject         = "project"
	ScopeTeam            = "team"
	ScopeGlobal          = "global"
	ScopeClientKnowledge = "client_knowledge"
)

func ValidScope(s string) bool {
	switch s {
	case ScopePersonal, ScopeProject, ScopeTeam, ScopeGlobal, ScopeClientKnowledge:
		return true
	}
	return false
}

var (
	ErrNotFound       = errors.New("not found")
	ErrInvalidScope   = errors.New("invalid scope (must be personal|project|team|global|client_knowledge)")
	ErrSessionMissing = errors.New("session not found")
)

type Observation struct {
	ID               string
	SessionID        sql.NullString
	DeveloperUID     sql.NullString
	DeveloperRole    string
	ClientID         sql.NullString
	Project          sql.NullString
	Scope            string
	ObservationType  string
	Title            string
	Subtitle         sql.NullString
	Narrative        sql.NullString
	Facts            sql.NullString
	Concepts         sql.NullString
	FilesTouched     sql.NullString
	ReasoningTrace   sql.NullString // JSON
	GeneratedByModel sql.NullString
	RelevanceCount   int
	DiscoveryTokens  int
	QualityScore     float64
	DriftDetected    bool
	ValidFrom        time.Time
	ValidUntil       sql.NullTime
	SupersededBy     sql.NullString
	TopicKey         sql.NullString
	Source           string
	Canon            bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

type Session struct {
	ID             string
	DeveloperUID   sql.NullString
	DeveloperEmail sql.NullString
	DeveloperRole  string
	ClientID       sql.NullString
	MachineID      string
	Project        sql.NullString
	Directory      sql.NullString
	Goal           sql.NullString
	Status         string
	StartedAt      time.Time
	EndedAt        sql.NullTime
	CreatedAt      time.Time
}

type SessionSummary struct {
	ID             string
	SessionID      string
	Request        sql.NullString
	Investigated   sql.NullString
	Learned        sql.NullString
	Completed      sql.NullString
	NextSteps      sql.NullString
	FilesRead      sql.NullString
	FilesEdited    sql.NullString
	Notes          sql.NullString
	DriftScore     sql.NullFloat64
	QualityGrade   sql.NullString
	ToolCallsCount int
	CreatedAt      time.Time
}

type Skill struct {
	ID          string
	Name        string
	Description string
	Stack       []string
	Content     string
	Source      string
	Active      bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

type Recipe struct {
	ID              string
	TaskPattern     string
	Stack           []string
	StepsJSON       string
	SourceSessionID sql.NullString
	UsageCount      int
	CreatedAt       time.Time
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// DBRaw expone el *sql.DB para queries puntuales del adapter (e.g. ListProjects).
func (s *Store) DBRaw() *sql.DB { return s.db }

// === Observations ===

type SaveParams struct {
	SessionID       string
	DeveloperUID    string
	DeveloperRole   string
	ClientID        string
	Project         string
	Scope           string
	ObservationType string
	Title           string
	Subtitle        string
	Narrative       string
	Facts           string
	Concepts        string
	FilesTouched    string
	ReasoningTrace  string // JSON
	TopicKey        string
	Source          string
	GeneratedByModel string
}

// Save inserta o reemplaza por (project, topic_key) cuando topic_key viene seteado.
// Cuando topic_key viene vacío, siempre inserta nuevo.
// Equivalente al aria_save legacy.
func (s *Store) Save(ctx context.Context, p SaveParams) (*Observation, error) {
	scope := strings.TrimSpace(p.Scope)
	if scope == "" {
		scope = ScopePersonal
	}
	if !ValidScope(scope) {
		return nil, ErrInvalidScope
	}
	obsType := strings.TrimSpace(p.ObservationType)
	if obsType == "" {
		obsType = "general"
	}
	source := strings.TrimSpace(p.Source)
	if source == "" {
		source = "manual"
	}
	devRole := strings.TrimSpace(p.DeveloperRole)
	if devRole == "" {
		devRole = "dev"
	}
	if strings.TrimSpace(p.Title) == "" {
		return nil, fmt.Errorf("title is required")
	}

	// Reasoning trace debe ser JSON válido si viene.
	reasoningTrace := strings.TrimSpace(p.ReasoningTrace)
	if reasoningTrace != "" && !json.Valid([]byte(reasoningTrace)) {
		return nil, fmt.Errorf("reasoning_trace must be valid JSON")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	topicKey := strings.TrimSpace(p.TopicKey)
	project := strings.TrimSpace(p.Project)

	// Si hay topic_key, busca obs existente para el mismo (project, topic_key)
	// y la actualiza (revisión). Si no, inserta nueva.
	if topicKey != "" && project != "" {
		var existingID string
		err := tx.QueryRowContext(ctx, `
			SELECT id FROM aria_observations
			WHERE project = $1 AND topic_key = $2 AND valid_until IS NULL
			ORDER BY created_at DESC LIMIT 1
		`, project, topicKey).Scan(&existingID)
		if err == nil {
			// Update (mantiene id)
			if err := updateObs(ctx, tx, existingID, p, scope, obsType, source, devRole, reasoningTrace); err != nil {
				return nil, err
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return s.GetByID(ctx, existingID)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("query existing topic: %w", err)
		}
	}

	id, err := newObsID()
	if err != nil {
		return nil, err
	}
	if err := insertObs(ctx, tx, id, p, scope, obsType, source, devRole, reasoningTrace); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetByID(ctx, id)
}

func insertObs(ctx context.Context, tx *sql.Tx, id string, p SaveParams, scope, obsType, source, devRole, reasoningTrace string) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO aria_observations (
			id, session_id, developer_uid, developer_role, client_id, project, scope,
			observation_type, title, subtitle, narrative, facts, concepts, files_touched,
			reasoning_trace, generated_by_model, topic_key, source
		) VALUES (
			$1, NULLIF($2,''), NULLIF($3,'')::uuid, $4, NULLIF($5,'')::uuid, NULLIF($6,''), $7,
			$8, $9, NULLIF($10,''), NULLIF($11,''), NULLIF($12,''), NULLIF($13,''), NULLIF($14,''),
			NULLIF($15,'')::jsonb, NULLIF($16,''), NULLIF($17,''), $18
		)
	`, id, p.SessionID, p.DeveloperUID, devRole, p.ClientID, p.Project, scope,
		obsType, p.Title, p.Subtitle, p.Narrative, p.Facts, p.Concepts, p.FilesTouched,
		reasoningTrace, p.GeneratedByModel, p.TopicKey, source)
	return err
}

func updateObs(ctx context.Context, tx *sql.Tx, id string, p SaveParams, scope, obsType, source, devRole, reasoningTrace string) error {
	_, err := tx.ExecContext(ctx, `
		UPDATE aria_observations SET
			title = $1, subtitle = NULLIF($2,''), narrative = NULLIF($3,''),
			facts = NULLIF($4,''), concepts = NULLIF($5,''), files_touched = NULLIF($6,''),
			reasoning_trace = NULLIF($7,'')::jsonb, observation_type = $8, scope = $9,
			source = $10, generated_by_model = NULLIF($11,''),
			relevance_count = relevance_count + 1, updated_at = NOW()
		WHERE id = $12
	`, p.Title, p.Subtitle, p.Narrative, p.Facts, p.Concepts, p.FilesTouched,
		reasoningTrace, obsType, scope, source, p.GeneratedByModel, id)
	return err
}

func (s *Store) GetByID(ctx context.Context, id string) (*Observation, error) {
	row := s.db.QueryRowContext(ctx, observationSelect+` WHERE id = $1`, id)
	o, err := scanObs(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return o, nil
}

type SearchParams struct {
	Query           string
	Project         string
	Scope           string
	ObservationType string
	Limit           int
}

// Search FTS spanish + accent-insensitive.
func (s *Store) Search(ctx context.Context, p SearchParams) ([]*Observation, error) {
	if p.Limit <= 0 {
		p.Limit = 20
	}
	conds := []string{"valid_until IS NULL"}
	args := []any{}
	idx := 1
	if q := strings.TrimSpace(p.Query); q != "" {
		conds = append(conds,
			fmt.Sprintf("to_tsvector('spanish', unaccent(coalesce(title,'') || ' ' || coalesce(subtitle,'') || ' ' || coalesce(narrative,'') || ' ' || coalesce(facts,'') || ' ' || coalesce(concepts,''))) @@ plainto_tsquery('spanish', unaccent($%d))", idx))
		args = append(args, q)
		idx++
	}
	if proj := strings.TrimSpace(p.Project); proj != "" {
		conds = append(conds, fmt.Sprintf("project = $%d", idx))
		args = append(args, proj)
		idx++
	}
	if scope := strings.TrimSpace(p.Scope); scope != "" {
		conds = append(conds, fmt.Sprintf("scope = $%d", idx))
		args = append(args, scope)
		idx++
	}
	if t := strings.TrimSpace(p.ObservationType); t != "" {
		conds = append(conds, fmt.Sprintf("observation_type = $%d", idx))
		args = append(args, t)
		idx++
	}
	args = append(args, p.Limit)
	query := observationSelect + " WHERE " + strings.Join(conds, " AND ") + " ORDER BY created_at DESC LIMIT $" + fmt.Sprint(idx)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Observation
	for rows.Next() {
		o, err := scanObs(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// PromoteCanon marca una observation como canónica (verdad curada).
func (s *Store) PromoteCanon(ctx context.Context, id string, by string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE aria_observations SET canon = TRUE, updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return err
	}
	// Audit en quality_records con signal=canon_promoted
	_, _ = s.db.ExecContext(ctx, `
		INSERT INTO aria_quality_records (observation_id, recorded_by_uid, signal, score, notes)
		VALUES ($1, NULLIF($2,'')::uuid, 'canon_promoted', 1.0, 'promoted to canon')
	`, id, by)
	return nil
}

// RecordQuality agrega un signal de calidad sobre la observation.
func (s *Store) RecordQuality(ctx context.Context, id, signal string, score float64, notes, byUID string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_quality_records (observation_id, recorded_by_uid, signal, score, notes)
		VALUES ($1, NULLIF($2,'')::uuid, $3, $4, $5)
	`, id, byUID, signal, score, notes)
	return err
}

// === Sessions ===

type StartSessionParams struct {
	DeveloperUID   string
	DeveloperEmail string
	DeveloperRole  string
	ClientID       string
	MachineID      string
	Project        string
	Directory      string
	Goal           string
}

func (s *Store) StartSession(ctx context.Context, p StartSessionParams) (*Session, error) {
	id, err := newSessionID()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.MachineID) == "" {
		p.MachineID = "local"
	}
	if strings.TrimSpace(p.DeveloperRole) == "" {
		p.DeveloperRole = "dev"
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO aria_sessions (id, developer_uid, developer_email, developer_role, client_id,
			machine_id, project, directory, goal)
		VALUES ($1, NULLIF($2,'')::uuid, NULLIF($3,''), $4, NULLIF($5,'')::uuid, $6, NULLIF($7,''), NULLIF($8,''), NULLIF($9,''))
	`, id, p.DeveloperUID, p.DeveloperEmail, p.DeveloperRole, p.ClientID,
		p.MachineID, p.Project, p.Directory, p.Goal)
	if err != nil {
		return nil, err
	}
	return s.GetSession(ctx, id)
}

func (s *Store) GetSession(ctx context.Context, id string) (*Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, developer_uid::text, developer_email, developer_role, client_id::text,
		       machine_id, project, directory, goal, status, started_at, ended_at, created_at
		FROM aria_sessions WHERE id = $1
	`, id)
	var sess Session
	var devUID, clientID, devEmail, project, directory, goal sql.NullString
	if err := row.Scan(&sess.ID, &devUID, &devEmail, &sess.DeveloperRole, &clientID,
		&sess.MachineID, &project, &directory, &goal, &sess.Status, &sess.StartedAt, &sess.EndedAt, &sess.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrSessionMissing
		}
		return nil, err
	}
	sess.DeveloperUID = devUID
	sess.DeveloperEmail = devEmail
	sess.ClientID = clientID
	sess.Project = project
	sess.Directory = directory
	sess.Goal = goal
	return &sess, nil
}

type SaveSummaryParams struct {
	SessionID    string
	Request      string
	Investigated string
	Learned      string
	Completed    string
	NextSteps    string
	FilesRead    string
	FilesEdited  string
	Notes        string
	QualityGrade string
}

func (s *Store) SaveSummary(ctx context.Context, p SaveSummaryParams) error {
	id, err := newSummaryID()
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO aria_session_summaries (id, session_id, request, investigated, learned, completed,
			next_steps, files_read, files_edited, notes, quality_grade)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,''),
			NULLIF($7,''), NULLIF($8,''), NULLIF($9,''), NULLIF($10,''), NULLIF($11,''))
		ON CONFLICT (session_id) DO UPDATE SET
			request = EXCLUDED.request, investigated = EXCLUDED.investigated,
			learned = EXCLUDED.learned, completed = EXCLUDED.completed,
			next_steps = EXCLUDED.next_steps, files_read = EXCLUDED.files_read,
			files_edited = EXCLUDED.files_edited, notes = EXCLUDED.notes,
			quality_grade = EXCLUDED.quality_grade
	`, id, p.SessionID, p.Request, p.Investigated, p.Learned, p.Completed,
		p.NextSteps, p.FilesRead, p.FilesEdited, p.Notes, p.QualityGrade)
	if err != nil {
		return err
	}
	// Marca la sesión como completada
	_, _ = s.db.ExecContext(ctx, `UPDATE aria_sessions SET status = 'completed', ended_at = NOW() WHERE id = $1`, p.SessionID)
	return nil
}

// Timeline retorna observations en ventana temporal por proyecto.
func (s *Store) Timeline(ctx context.Context, project string, since, until *time.Time, limit int) ([]*Observation, error) {
	if limit <= 0 {
		limit = 50
	}
	conds := []string{"valid_until IS NULL"}
	args := []any{}
	idx := 1
	if p := strings.TrimSpace(project); p != "" {
		conds = append(conds, fmt.Sprintf("project = $%d", idx))
		args = append(args, p)
		idx++
	}
	if since != nil {
		conds = append(conds, fmt.Sprintf("created_at >= $%d", idx))
		args = append(args, since.UTC())
		idx++
	}
	if until != nil {
		conds = append(conds, fmt.Sprintf("created_at <= $%d", idx))
		args = append(args, until.UTC())
		idx++
	}
	args = append(args, limit)
	query := observationSelect + " WHERE " + strings.Join(conds, " AND ") + " ORDER BY created_at DESC LIMIT $" + fmt.Sprint(idx)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Observation
	for rows.Next() {
		o, err := scanObs(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// === Skills ===

type UpsertSkillParams struct {
	ID          string
	Name        string
	Description string
	Stack       []string
	Content     string
	Source      string
	Active      bool
}

func (s *Store) UpsertSkill(ctx context.Context, p UpsertSkillParams) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("skill id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_skills (id, name, description, stack, content, source, active)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			name = EXCLUDED.name, description = EXCLUDED.description,
			stack = EXCLUDED.stack, content = EXCLUDED.content,
			source = EXCLUDED.source, active = EXCLUDED.active, updated_at = NOW()
	`, p.ID, p.Name, p.Description, pq.Array(p.Stack), p.Content, p.Source, p.Active)
	return err
}

// GetSkillByID retorna un skill o ErrNotFound.
func (s *Store) GetSkillByID(ctx context.Context, id string) (*Skill, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, description, stack, content, source, active, created_at, updated_at
		FROM aria_skills WHERE id = $1
	`, id)
	var sk Skill
	var stack pq.StringArray
	if err := row.Scan(&sk.ID, &sk.Name, &sk.Description, &stack, &sk.Content, &sk.Source, &sk.Active, &sk.CreatedAt, &sk.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	sk.Stack = []string(stack)
	return &sk, nil
}

// ListAllSkills incluye los inactivos (para admin UI).
func (s *Store) ListAllSkills(ctx context.Context) ([]*Skill, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, description, stack, content, source, active, created_at, updated_at
		FROM aria_skills ORDER BY active DESC, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Skill
	for rows.Next() {
		var sk Skill
		var stack pq.StringArray
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Description, &stack, &sk.Content, &sk.Source, &sk.Active, &sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, err
		}
		sk.Stack = []string(stack)
		out = append(out, &sk)
	}
	return out, rows.Err()
}

// SetSkillActive toggle de status.
func (s *Store) SetSkillActive(ctx context.Context, id string, active bool) error {
	res, err := s.db.ExecContext(ctx, `UPDATE aria_skills SET active = $1, updated_at = NOW() WHERE id = $2`, active, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSkill borra un skill por id.
func (s *Store) DeleteSkill(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM aria_skills WHERE id = $1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// SearchSkills filtra los skills por query FTS español accent-insensitive sobre
// name+description+content, opcionalmente por stack y por activo. Si query está
// vacío retorna la lista completa filtrada por stack/active.
func (s *Store) SearchSkills(ctx context.Context, query, stackFilter string, activeOnly bool) ([]*Skill, error) {
	conds := []string{}
	args := []any{}
	idx := 1
	if q := strings.TrimSpace(query); q != "" {
		conds = append(conds,
			fmt.Sprintf("to_tsvector('spanish', unaccent(coalesce(name,'') || ' ' || coalesce(description,'') || ' ' || coalesce(content,''))) @@ plainto_tsquery('spanish', unaccent($%d))", idx))
		args = append(args, q)
		idx++
	}
	if st := strings.TrimSpace(stackFilter); st != "" {
		conds = append(conds, fmt.Sprintf("$%d = ANY(stack)", idx))
		args = append(args, st)
		idx++
	}
	if activeOnly {
		conds = append(conds, "active = TRUE")
	}
	where := ""
	if len(conds) > 0 {
		where = " WHERE " + strings.Join(conds, " AND ")
	}
	q := `SELECT id, name, description, stack, content, source, active, created_at, updated_at
		FROM aria_skills` + where + ` ORDER BY active DESC, name`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Skill
	for rows.Next() {
		var sk Skill
		var stack pq.StringArray
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Description, &stack, &sk.Content, &sk.Source, &sk.Active, &sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, err
		}
		sk.Stack = []string(stack)
		out = append(out, &sk)
	}
	return out, rows.Err()
}

// ListUniqueStacks devuelve la lista distinct de stacks (unnest del array TEXT[])
// presentes en la tabla aria_skills. Útil para popular dropdown de filtros.
func (s *Store) ListUniqueStacks(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT unnest(stack) AS st
		FROM aria_skills
		WHERE stack IS NOT NULL
		ORDER BY st
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var st string
		if err := rows.Scan(&st); err != nil {
			return nil, err
		}
		st = strings.TrimSpace(st)
		if st != "" {
			out = append(out, st)
		}
	}
	return out, rows.Err()
}

func (s *Store) ListSkills(ctx context.Context, stackFilter []string) ([]*Skill, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if len(stackFilter) > 0 {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, name, description, stack, content, source, active, created_at, updated_at
			FROM aria_skills WHERE active = TRUE AND stack && $1::text[] ORDER BY name
		`, pq.Array(stackFilter))
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT id, name, description, stack, content, source, active, created_at, updated_at
			FROM aria_skills WHERE active = TRUE ORDER BY name
		`)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Skill
	for rows.Next() {
		var sk Skill
		var stack pq.StringArray
		if err := rows.Scan(&sk.ID, &sk.Name, &sk.Description, &stack, &sk.Content, &sk.Source, &sk.Active, &sk.CreatedAt, &sk.UpdatedAt); err != nil {
			return nil, err
		}
		sk.Stack = []string(stack)
		out = append(out, &sk)
	}
	return out, rows.Err()
}

// === Recipes ===

type UpsertRecipeParams struct {
	ID              string
	TaskPattern     string
	Stack           []string
	StepsJSON       string
	SourceSessionID string
}

func (s *Store) UpsertRecipe(ctx context.Context, p UpsertRecipeParams) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("recipe id is required")
	}
	stepsJSON := p.StepsJSON
	if strings.TrimSpace(stepsJSON) == "" {
		stepsJSON = "[]"
	}
	if !json.Valid([]byte(stepsJSON)) {
		return fmt.Errorf("steps_json must be valid JSON")
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_recipes (id, task_pattern, stack, steps_json, source_session_id)
		VALUES ($1, $2, $3, $4::jsonb, NULLIF($5,''))
		ON CONFLICT (id) DO UPDATE SET
			task_pattern = EXCLUDED.task_pattern, stack = EXCLUDED.stack,
			steps_json = EXCLUDED.steps_json, source_session_id = EXCLUDED.source_session_id
	`, p.ID, p.TaskPattern, pq.Array(p.Stack), stepsJSON, p.SourceSessionID)
	return err
}

func (s *Store) GetRecipes(ctx context.Context, taskDescription string, stack []string, limit int) ([]*Recipe, error) {
	if limit <= 0 {
		limit = 5
	}
	conds := []string{"1=1"}
	args := []any{}
	idx := 1
	if td := strings.TrimSpace(taskDescription); td != "" {
		conds = append(conds, fmt.Sprintf("to_tsvector('spanish', unaccent(task_pattern)) @@ plainto_tsquery('spanish', unaccent($%d))", idx))
		args = append(args, td)
		idx++
	}
	if len(stack) > 0 {
		conds = append(conds, fmt.Sprintf("stack && $%d::text[]", idx))
		args = append(args, pq.Array(stack))
		idx++
	}
	args = append(args, limit)
	query := fmt.Sprintf(`
		SELECT id, task_pattern, stack, steps_json::text, source_session_id, usage_count, created_at
		FROM aria_recipes WHERE %s ORDER BY usage_count DESC, created_at DESC LIMIT $%d
	`, strings.Join(conds, " AND "), idx)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Recipe
	for rows.Next() {
		var r Recipe
		var stack pq.StringArray
		if err := rows.Scan(&r.ID, &r.TaskPattern, &stack, &r.StepsJSON, &r.SourceSessionID, &r.UsageCount, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Stack = []string(stack)
		out = append(out, &r)
	}
	return out, rows.Err()
}

// === Context status ===

type ContextStatus struct {
	Project          string
	ActiveSessionID  string
	TotalObservations int
	Last7DaysCount   int
	SkillsLoaded     []string
	LastSyncAt       sql.NullTime
}

func (s *Store) GetContextStatus(ctx context.Context, project string) (*ContextStatus, error) {
	st := &ContextStatus{Project: project}

	// Active session (sin ended_at) más reciente
	_ = s.db.QueryRowContext(ctx, `
		SELECT id FROM aria_sessions
		WHERE project = $1 AND ended_at IS NULL
		ORDER BY started_at DESC LIMIT 1
	`, project).Scan(&st.ActiveSessionID)

	// Counts
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM aria_observations WHERE project = $1 AND valid_until IS NULL`, project).Scan(&st.TotalObservations)
	_ = s.db.QueryRowContext(ctx, `SELECT count(*) FROM aria_observations WHERE project = $1 AND valid_until IS NULL AND created_at >= NOW() - INTERVAL '7 days'`, project).Scan(&st.Last7DaysCount)

	// Skills cargados (activos)
	rows, err := s.db.QueryContext(ctx, `SELECT name FROM aria_skills WHERE active = TRUE ORDER BY name`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var n string
			if err := rows.Scan(&n); err == nil {
				st.SkillsLoaded = append(st.SkillsLoaded, n)
			}
		}
	}
	return st, nil
}

// === Helpers ===

const observationSelect = `
	SELECT id, session_id, developer_uid::text, developer_role, client_id::text, project, scope,
	       observation_type, title, subtitle, narrative, facts, concepts, files_touched,
	       reasoning_trace::text, generated_by_model, relevance_count, discovery_tokens,
	       quality_score, drift_detected, valid_from, valid_until, superseded_by,
	       topic_key, source, canon, created_at, updated_at
	FROM aria_observations
`

type scanner interface {
	Scan(dest ...any) error
}

func scanObs(s scanner) (*Observation, error) {
	var o Observation
	var devUID, clientID sql.NullString
	if err := s.Scan(&o.ID, &o.SessionID, &devUID, &o.DeveloperRole, &clientID, &o.Project, &o.Scope,
		&o.ObservationType, &o.Title, &o.Subtitle, &o.Narrative, &o.Facts, &o.Concepts, &o.FilesTouched,
		&o.ReasoningTrace, &o.GeneratedByModel, &o.RelevanceCount, &o.DiscoveryTokens,
		&o.QualityScore, &o.DriftDetected, &o.ValidFrom, &o.ValidUntil, &o.SupersededBy,
		&o.TopicKey, &o.Source, &o.Canon, &o.CreatedAt, &o.UpdatedAt); err != nil {
		return nil, err
	}
	o.DeveloperUID = devUID
	o.ClientID = clientID
	return &o, nil
}

func newObsID() (string, error) {
	return newID("obs_")
}

func newSessionID() (string, error) {
	return newID("ses_")
}

func newSummaryID() (string, error) {
	return newID("sum_")
}

func newID(prefix string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(b), nil
}
