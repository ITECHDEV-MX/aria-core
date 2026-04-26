package teamprojects

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

const taskSelectCols = `
	id::text, project_id::text, title, COALESCE(description_md,''),
	status, priority, due_date, COALESCE(estimate_hours,0), COALESCE(spent_hours,0),
	COALESCE(github_issue_number,0),
	COALESCE(parent_task_id::text,''), position, labels,
	created_by_uid::text, created_at, updated_at,
	closed_at, COALESCE(closed_by_uid::text,'')`

func scanTask(r scanRow) (*Task, error) {
	var t Task
	var dueDate sql.NullTime
	var closedAt sql.NullTime
	var labels pq.StringArray
	if err := r.Scan(
		&t.ID, &t.ProjectID, &t.Title, &t.DescriptionMD,
		&t.Status, &t.Priority, &dueDate, &t.EstimateHours, &t.SpentHours,
		&t.GitHubIssueNumber,
		&t.ParentTaskID, &t.Position, &labels,
		&t.CreatedByUID, &t.CreatedAt, &t.UpdatedAt,
		&closedAt, &t.ClosedByUID,
	); err != nil {
		return nil, err
	}
	if dueDate.Valid {
		d := dueDate.Time
		t.DueDate = &d
	}
	if closedAt.Valid {
		d := closedAt.Time
		t.ClosedAt = &d
	}
	t.Labels = []string(labels)
	if t.Labels == nil {
		t.Labels = []string{}
	}
	return &t, nil
}

// CreateTask inserta una nueva task con position al final del status 'todo'.
func (s *PgStore) CreateTask(ctx context.Context, p CreateTaskParams) (*Task, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("teamprojects: store not initialized")
	}
	if !isUUID(p.ProjectID) {
		return nil, fmt.Errorf("%w: project_id must be uuid", ErrInvalidInput)
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return nil, fmt.Errorf("%w: title required", ErrInvalidInput)
	}
	if p.CreatedByUID == "" {
		return nil, fmt.Errorf("%w: created_by_uid required", ErrInvalidInput)
	}
	priority := strings.TrimSpace(p.Priority)
	if priority == "" {
		priority = "medium"
	}
	switch priority {
	case "low", "medium", "high", "urgent":
	default:
		return nil, fmt.Errorf("%w: priority %q inválido", ErrInvalidInput, priority)
	}
	id := uuid.NewString()
	labels := pq.StringArray(p.Labels)
	if labels == nil {
		labels = pq.StringArray{}
	}
	// Compute next position en columna 'todo'.
	var pos int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(position),0) FROM aria_tasks WHERE project_id=$1::uuid AND status='todo'`, p.ProjectID).Scan(&pos); err != nil {
		return nil, fmt.Errorf("teamprojects: compute task position: %w", err)
	}
	pos++

	const q = `
		INSERT INTO aria_tasks (
			id, project_id, title, description_md, status, priority,
			due_date, estimate_hours, github_issue_number,
			parent_task_id, position, labels, created_by_uid
		) VALUES (
			$1::uuid, $2::uuid, $3, NULLIF($4,''), 'todo', $5,
			$6, $7, NULLIF($8,0),
			NULLIF($9,'')::uuid, $10, $11, $12::uuid
		)
		RETURNING ` + taskSelectCols
	row := s.db.QueryRowContext(ctx, q,
		id, p.ProjectID, title, p.DescriptionMD, priority,
		p.DueDate, p.EstimateHours, p.GitHubIssueNumber,
		p.ParentTaskID, pos, labels, p.CreatedByUID,
	)
	t, err := scanTask(row)
	if err != nil {
		return nil, fmt.Errorf("teamprojects: create task: %w", err)
	}
	return t, nil
}

// GetTask retorna una task por ID.
func (s *PgStore) GetTask(ctx context.Context, taskID string) (*Task, error) {
	if !isUUID(taskID) {
		return nil, fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	row := s.db.QueryRowContext(ctx, `SELECT `+taskSelectCols+` FROM aria_tasks WHERE id=$1::uuid`, taskID)
	t, err := scanTask(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return t, nil
}

// ListTasksByProject lista tasks de un proyecto, opcionalmente filtradas por status.
func (s *PgStore) ListTasksByProject(ctx context.Context, projectID string, f TaskFilter) ([]*Task, error) {
	if !isUUID(projectID) {
		return nil, fmt.Errorf("%w: project_id must be uuid", ErrInvalidInput)
	}
	conds := []string{"project_id = $1::uuid"}
	args := []any{projectID}
	idx := 2
	if f.Status != "" && f.Status != "all" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, f.Status)
		idx++
	}
	if f.Priority != "" {
		conds = append(conds, fmt.Sprintf("priority = $%d", idx))
		args = append(args, f.Priority)
		idx++
	}
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	q := `SELECT ` + taskSelectCols + ` FROM aria_tasks WHERE ` + strings.Join(conds, " AND ") +
		fmt.Sprintf(` ORDER BY status ASC, position ASC, created_at ASC LIMIT %d`, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("teamprojects: list tasks: %w", err)
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ListTasksAssignedTo retorna tasks abiertas asignadas a un usuario.
func (s *PgStore) ListTasksAssignedTo(ctx context.Context, userUID string, f TaskFilter) ([]*Task, error) {
	if !isUUID(userUID) {
		return nil, fmt.Errorf("%w: user_uid must be uuid", ErrInvalidInput)
	}
	conds := []string{"id IN (SELECT task_id FROM aria_task_assignments WHERE user_uid = $1::uuid)"}
	args := []any{userUID}
	idx := 2
	if f.Status == "" {
		// default: open statuses only
		conds = append(conds, "status NOT IN ('done','cancelled')")
	} else if f.Status != "all" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, f.Status)
		idx++
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	q := `SELECT ` + taskSelectCols + ` FROM aria_tasks WHERE ` + strings.Join(conds, " AND ") +
		fmt.Sprintf(` ORDER BY due_date ASC NULLS LAST, priority DESC, created_at DESC LIMIT %d`, limit)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("teamprojects: list assigned tasks: %w", err)
	}
	defer rows.Close()
	out := []*Task{}
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// UpdateTaskStatus cambia el status (Kanban drag-drop). Si done|cancelled, NO marca closed_at
// (eso lo hace CloseTask para captura de knowledge).
func (s *PgStore) UpdateTaskStatus(ctx context.Context, taskID, status, byUID string) error {
	if !isUUID(taskID) {
		return fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	switch status {
	case "todo", "in_progress", "review", "done", "cancelled":
	default:
		return fmt.Errorf("%w: status %q inválido", ErrInvalidInput, status)
	}
	res, err := s.db.ExecContext(ctx, `UPDATE aria_tasks SET status=$2, updated_at=NOW() WHERE id=$1::uuid`, taskID, status)
	if err != nil {
		return fmt.Errorf("teamprojects: update status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateTaskPosition ajusta la posición en su columna actual.
func (s *PgStore) UpdateTaskPosition(ctx context.Context, taskID string, newPosition int) error {
	if !isUUID(taskID) {
		return fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	if newPosition < 0 {
		newPosition = 0
	}
	res, err := s.db.ExecContext(ctx, `UPDATE aria_tasks SET position=$2, updated_at=NOW() WHERE id=$1::uuid`, taskID, newPosition)
	if err != nil {
		return fmt.Errorf("teamprojects: update position: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AssignTask asigna un user a la task. Idempotente.
func (s *PgStore) AssignTask(ctx context.Context, taskID, userUID, byUID string) error {
	if !isUUID(taskID) || !isUUID(userUID) {
		return fmt.Errorf("%w: ids must be uuid", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_task_assignments (task_id, user_uid, assigned_by_uid)
		VALUES ($1::uuid, $2::uuid, $3::uuid)
		ON CONFLICT (task_id, user_uid) DO NOTHING`,
		taskID, userUID, nullableUUIDArg(byUID))
	if err != nil {
		return fmt.Errorf("teamprojects: assign: %w", err)
	}
	return nil
}

// UnassignTask quita la asignación.
func (s *PgStore) UnassignTask(ctx context.Context, taskID, userUID string) error {
	if !isUUID(taskID) || !isUUID(userUID) {
		return fmt.Errorf("%w: ids must be uuid", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM aria_task_assignments WHERE task_id=$1::uuid AND user_uid=$2::uuid`, taskID, userUID)
	if err != nil {
		return fmt.Errorf("teamprojects: unassign: %w", err)
	}
	return nil
}

// CloseTask marca la task como done + registra closed_at/closed_by_uid.
// Knowledge capture se hace por separado en CaptureKnowledge.
func (s *PgStore) CloseTask(ctx context.Context, taskID, byUID string) error {
	if !isUUID(taskID) {
		return fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	if !isUUID(byUID) {
		return fmt.Errorf("%w: by_uid must be uuid", ErrInvalidInput)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE aria_tasks SET status='done', closed_at=NOW(), closed_by_uid=$2::uuid, updated_at=NOW()
		WHERE id=$1::uuid`,
		taskID, byUID)
	if err != nil {
		return fmt.Errorf("teamprojects: close task: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListTaskAssignees retorna los UIDs asignados a una task.
func (s *PgStore) ListTaskAssignees(ctx context.Context, taskID string) ([]string, error) {
	if !isUUID(taskID) {
		return nil, fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT user_uid::text FROM aria_task_assignments WHERE task_id=$1::uuid`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// ─── Observation / session links ──────────────────────────────────────

// LinkObservation registra una obs como knowledge de la task.
func (s *PgStore) LinkObservation(ctx context.Context, taskID, observationID, linkType, byUID string) error {
	if !isUUID(taskID) {
		return fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	observationID = strings.TrimSpace(observationID)
	if observationID == "" {
		return fmt.Errorf("%w: observation_id required", ErrInvalidInput)
	}
	if linkType == "" {
		linkType = "work"
	}
	switch linkType {
	case "work", "prompt", "outcome", "reference":
	default:
		return fmt.Errorf("%w: link_type %q inválido", ErrInvalidInput, linkType)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_task_observations (task_id, observation_id, link_type, linked_by_uid)
		VALUES ($1::uuid, $2, $3, $4::uuid)
		ON CONFLICT (task_id, observation_id, link_type) DO NOTHING`,
		taskID, observationID, linkType, nullableUUIDArg(byUID))
	if err != nil {
		return fmt.Errorf("teamprojects: link observation: %w", err)
	}
	return nil
}

// UnlinkObservation quita el link específico (task, obs, link_type).
func (s *PgStore) UnlinkObservation(ctx context.Context, taskID, observationID, linkType string) error {
	if !isUUID(taskID) {
		return fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	if linkType == "" {
		linkType = "work"
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM aria_task_observations WHERE task_id=$1::uuid AND observation_id=$2 AND link_type=$3`, taskID, observationID, linkType)
	return err
}

// ListLinkedObservations lista observaciones vinculadas.
func (s *PgStore) ListLinkedObservations(ctx context.Context, taskID string) ([]TaskObservationLink, error) {
	if !isUUID(taskID) {
		return nil, fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id::text, observation_id, link_type, COALESCE(linked_by_uid::text,''), linked_at
		FROM aria_task_observations WHERE task_id=$1::uuid ORDER BY linked_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskObservationLink{}
	for rows.Next() {
		var l TaskObservationLink
		if err := rows.Scan(&l.TaskID, &l.ObservationID, &l.LinkType, &l.LinkedByUID, &l.LinkedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LinkSession registra una sesión Claude Code como knowledge de la task.
func (s *PgStore) LinkSession(ctx context.Context, taskID, sessionID string) error {
	if !isUUID(taskID) {
		return fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("%w: session_id required", ErrInvalidInput)
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO aria_task_sessions (task_id, session_id) VALUES ($1::uuid, $2)
		ON CONFLICT (task_id, session_id) DO NOTHING`, taskID, sessionID)
	return err
}

// ListLinkedSessions lista sesiones vinculadas.
func (s *PgStore) ListLinkedSessions(ctx context.Context, taskID string) ([]TaskSessionLink, error) {
	if !isUUID(taskID) {
		return nil, fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT task_id::text, session_id, linked_at FROM aria_task_sessions WHERE task_id=$1::uuid ORDER BY linked_at DESC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskSessionLink{}
	for rows.Next() {
		var l TaskSessionLink
		if err := rows.Scan(&l.TaskID, &l.SessionID, &l.LinkedAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ─── Comments ───────────────────────────────────────────────────────────

// AddComment agrega un comentario a la task.
func (s *PgStore) AddComment(ctx context.Context, taskID, authorUID, content string) (*TaskComment, error) {
	if !isUUID(taskID) || !isUUID(authorUID) {
		return nil, fmt.Errorf("%w: ids must be uuid", ErrInvalidInput)
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("%w: content required", ErrInvalidInput)
	}
	id := uuid.NewString()
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO aria_task_comments (id, task_id, author_uid, content_md)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4)
		RETURNING id::text, task_id::text, author_uid::text, content_md, created_at`,
		id, taskID, authorUID, content)
	var c TaskComment
	if err := row.Scan(&c.ID, &c.TaskID, &c.AuthorUID, &c.ContentMD, &c.CreatedAt); err != nil {
		return nil, fmt.Errorf("teamprojects: add comment: %w", err)
	}
	return &c, nil
}

// ListComments lista comentarios de una task ordenados ASC.
func (s *PgStore) ListComments(ctx context.Context, taskID string) ([]TaskComment, error) {
	if !isUUID(taskID) {
		return nil, fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, task_id::text, author_uid::text, content_md, created_at
		FROM aria_task_comments WHERE task_id=$1::uuid ORDER BY created_at ASC`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TaskComment{}
	for rows.Next() {
		var c TaskComment
		if err := rows.Scan(&c.ID, &c.TaskID, &c.AuthorUID, &c.ContentMD, &c.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// LinkObservationsByQuery es un helper que busca observaciones por developer + project + rango y las linkea con linkType.
// Los IDs se pasan como []string; este helper sólo los inserta. La query real vive en knowledge_capture.go.
func (s *PgStore) LinkObservationsByIDs(ctx context.Context, taskID string, observationIDs []string, linkType, byUID string) (int, error) {
	count := 0
	for _, id := range observationIDs {
		if err := s.LinkObservation(ctx, taskID, id, linkType, byUID); err == nil {
			count++
		}
	}
	return count, nil
}

// LinkSessionsByIDs linkea múltiples sesiones a la task.
func (s *PgStore) LinkSessionsByIDs(ctx context.Context, taskID string, sessionIDs []string) (int, error) {
	count := 0
	for _, id := range sessionIDs {
		if err := s.LinkSession(ctx, taskID, id); err == nil {
			count++
		}
	}
	return count, nil
}

// since/until helpers — protege fechas contra zero-times y no envía al SQL.
func boundedRange(from, to time.Time) (time.Time, time.Time) {
	if from.IsZero() {
		from = time.Now().Add(-90 * 24 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now().Add(24 * time.Hour)
	}
	return from, to
}
