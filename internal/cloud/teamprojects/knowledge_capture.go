package teamprojects

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// KnowledgeCapture es el resultado de CaptureKnowledge.
type KnowledgeCapture struct {
	ObservationsLinked int      `json:"observations_linked"`
	SessionsLinked     int      `json:"sessions_linked"`
	CommitsLinked      int      `json:"commits_linked"` // commits guardados como observation 'outcome'
	CapturedObsIDs     []string `json:"captured_observation_ids"`
	CapturedSessionIDs []string `json:"captured_session_ids"`
}

// GitHubCommitFetcher es la API mínima que CaptureKnowledge necesita del cliente GH.
// Implementación real: internal/cloud/github.Client.
type GitHubCommitFetcher interface {
	ListCommits(ctx context.Context, owner, repo string, since, until time.Time, author string) ([]GHCommit, error)
}

// GHCommit es la representación mínima de un commit (mirror del github package).
type GHCommit struct {
	SHA     string
	Message string
	URL     string
	Author  string
	Date    time.Time
}

// ObservationSaver permite a CaptureKnowledge persistir commits como observaciones.
// Implementación real: adapter contra ariamem.Store.SaveObservation o similar.
type ObservationSaver interface {
	SaveCommitAsObservation(ctx context.Context, in CommitObservationInput) (string, error)
}

// CommitObservationInput agrupa los datos para crear una observación de commit.
type CommitObservationInput struct {
	DeveloperUID string
	Project      string
	TaskID       string
	SHA          string
	Message      string
	URL          string
	CommitDate   time.Time
}

// GitHubUserResolver resuelve el GitHub username de un user_uid.
// Implementación: lee de cloud_users.preferences o de profile metadata.
type GitHubUserResolver interface {
	GitHubUsername(ctx context.Context, userUID string) (string, error)
}

// CaptureKnowledgeOptions controla CaptureKnowledge.
type CaptureKnowledgeOptions struct {
	GHFetcher        GitHubCommitFetcher
	ObsSaver         ObservationSaver
	GHResolver       GitHubUserResolver
	LinkedByUID      string
	ObservationLimit int // por default 50
	SessionLimit     int // por default 20
}

// CaptureKnowledge corre el flow al cerrar una task: query obs + sessions del rango,
// linkea cada uno como knowledge. Si GHFetcher + ObsSaver están presentes y el
// proyecto tiene github_repo_*, también captura commits del rango como obs 'outcome'.
//
// Idempotente: usar CONFLICT DO NOTHING en links.
func (s *PgStore) CaptureKnowledge(ctx context.Context, taskID string, opts CaptureKnowledgeOptions) (*KnowledgeCapture, error) {
	if !isUUID(taskID) {
		return nil, fmt.Errorf("%w: task_id must be uuid", ErrInvalidInput)
	}
	task, err := s.GetTask(ctx, taskID)
	if err != nil {
		return nil, err
	}
	project, err := s.GetProject(ctx, task.ProjectID)
	if err != nil {
		return nil, err
	}
	assignees, err := s.ListTaskAssignees(ctx, taskID)
	if err != nil {
		return nil, err
	}
	// Si la task no tiene assignees, usamos al creador como fallback.
	if len(assignees) == 0 {
		assignees = []string{task.CreatedByUID}
	}

	// Rango temporal: created_at de la task → ahora (o closed_at si existe).
	from := task.CreatedAt
	to := time.Now().UTC()
	if task.ClosedAt != nil {
		to = *task.ClosedAt
	}

	out := &KnowledgeCapture{
		CapturedObsIDs:     []string{},
		CapturedSessionIDs: []string{},
	}

	obsLimit := opts.ObservationLimit
	if obsLimit <= 0 {
		obsLimit = 50
	}
	sessLimit := opts.SessionLimit
	if sessLimit <= 0 {
		sessLimit = 20
	}

	// 1) Observaciones del rango (project = slug AND developer_uid IN assignees AND created_at BETWEEN).
	obsIDs, err := s.queryObservationsForCapture(ctx, project.Slug, assignees, from, to, obsLimit)
	if err != nil {
		return nil, err
	}
	for _, oid := range obsIDs {
		if err := s.LinkObservation(ctx, taskID, oid, "work", opts.LinkedByUID); err == nil {
			out.ObservationsLinked++
			out.CapturedObsIDs = append(out.CapturedObsIDs, oid)
		}
	}

	// 2) Sesiones Claude Code del rango.
	sessIDs, err := s.querySessionsForCapture(ctx, project.Slug, assignees, from, to, sessLimit)
	if err != nil {
		return nil, err
	}
	for _, sid := range sessIDs {
		if err := s.LinkSession(ctx, taskID, sid); err == nil {
			out.SessionsLinked++
			out.CapturedSessionIDs = append(out.CapturedSessionIDs, sid)
		}
	}

	// 3) Commits GitHub (si proyecto tiene repo configurado y opts presentes).
	if opts.GHFetcher != nil && opts.ObsSaver != nil && project.GitHubRepoOwner != "" && project.GitHubRepoName != "" {
		// Para cada assignee, resolver gh username y queryar commits.
		seen := map[string]struct{}{}
		for _, uid := range assignees {
			ghUser := ""
			if opts.GHResolver != nil {
				if u, err := opts.GHResolver.GitHubUsername(ctx, uid); err == nil {
					ghUser = u
				}
			}
			commits, err := opts.GHFetcher.ListCommits(ctx, project.GitHubRepoOwner, project.GitHubRepoName, from, to, ghUser)
			if err != nil {
				// non-fatal: continuamos con otros assignees
				continue
			}
			for _, c := range commits {
				if _, ok := seen[c.SHA]; ok {
					continue
				}
				seen[c.SHA] = struct{}{}
				obsID, err := opts.ObsSaver.SaveCommitAsObservation(ctx, CommitObservationInput{
					DeveloperUID: uid,
					Project:      project.Slug,
					TaskID:       taskID,
					SHA:          c.SHA,
					Message:      c.Message,
					URL:          c.URL,
					CommitDate:   c.Date,
				})
				if err != nil || obsID == "" {
					continue
				}
				if err := s.LinkObservation(ctx, taskID, obsID, "outcome", opts.LinkedByUID); err == nil {
					out.CommitsLinked++
				}
			}
		}
	}
	return out, nil
}

// queryObservationsForCapture devuelve IDs de observaciones del rango temporal,
// filtradas por project (slug) y developer_uid in (assignees).
func (s *PgStore) queryObservationsForCapture(ctx context.Context, projectSlug string, assignees []string, from, to time.Time, limit int) ([]string, error) {
	projectSlug = strings.TrimSpace(projectSlug)
	if projectSlug == "" || len(assignees) == 0 {
		return nil, nil
	}
	// pq array param: usamos = ANY($N)
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM aria_observations
		WHERE project = $1
		  AND developer_uid::text = ANY($2::text[])
		  AND created_at BETWEEN $3 AND $4
		ORDER BY created_at ASC
		LIMIT $5`,
		projectSlug, pqArray(assignees), from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("teamprojects: query observations for capture: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *PgStore) querySessionsForCapture(ctx context.Context, projectSlug string, assignees []string, from, to time.Time, limit int) ([]string, error) {
	projectSlug = strings.TrimSpace(projectSlug)
	if projectSlug == "" || len(assignees) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id FROM aria_sessions
		WHERE project = $1
		  AND developer_uid::text = ANY($2::text[])
		  AND started_at BETWEEN $3 AND $4
		ORDER BY started_at ASC
		LIMIT $5`,
		projectSlug, pqArray(assignees), from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, fmt.Errorf("teamprojects: query sessions for capture: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// pqArray returns the value as a sql.Array friendly for PG TEXT[].
func pqArray(items []string) any {
	// pq.Array no se importa para evitar otra dep; usamos formato literal
	// {item1,item2}.
	if len(items) == 0 {
		return "{}"
	}
	parts := make([]string, len(items))
	for i, s := range items {
		s = strings.ReplaceAll(s, `"`, `\"`)
		parts[i] = `"` + s + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// AssertHasDB es un helper interno para tests.
func (s *PgStore) assertDB() error {
	if s == nil || s.db == nil {
		return fmt.Errorf("teamprojects: store not initialized")
	}
	return nil
}

// touch helps avoid the unused import gripe in test isolation.
var _ = sql.ErrNoRows
