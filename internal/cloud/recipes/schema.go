package recipes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lib/pq"
)

// RecipeStore is the persistence contract used by Runner.
type RecipeStore interface {
	GetRecipeByKey(ctx context.Context, key string) (*Recipe, error)
	ListRecipes(ctx context.Context, executableOnly bool) ([]*Recipe, error)
	UpsertRecipe(ctx context.Context, p UpsertRecipeInput) error
	IncrementUsage(ctx context.Context, recipeID string) error

	CreateExecution(ctx context.Context, e *Execution) error
	UpdateExecutionStatus(ctx context.Context, executionID, status string, completed int, failedIndex int, finishedAt *time.Time, totalDurationMs int) error
	InsertStepResult(ctx context.Context, executionID string, sr StepResult) error
	GetExecution(ctx context.Context, id string) (*Execution, error)
	ListExecutions(ctx context.Context, filter ExecFilter, limit int) ([]Execution, error)
	RecipeStats(ctx context.Context, recipeKey string, since time.Time) (RecipeStats, error)
}

// UpsertRecipeInput captures the writable fields when seeding/upserting recipes.
type UpsertRecipeInput struct {
	ID                      string
	Key                     string
	TaskPattern             string
	Stack                   []string
	Steps                   []Step
	Executable              bool
	ExpectedDurationSeconds int
}

// RecipeStats is aggregate telemetry for the dashboard catalog tab.
type RecipeStats struct {
	RecipeKey       string
	TotalRuns       int
	SuccessCount    int
	FailureCount    int
	SuccessRate     float64
	AvgDurationMs   int
	LastRunAt       *time.Time
	LastRunStatus   string
}

// pgStore is the Postgres implementation of RecipeStore.
type pgStore struct {
	db *sql.DB
}

// NewPgStore returns a RecipeStore backed by *sql.DB. Migrations live in
// cloudstore.go (see BEGIN RECIPE MIGRATIONS / END), so callers do not need
// to run anything here besides handing in an already-migrated DB.
func NewPgStore(db *sql.DB) RecipeStore {
	return &pgStore{db: db}
}

// recipe_key column on aria_recipes is added by the cloudstore migrations.
// We default to id when the legacy "id" column already holds a stable key
// (the ariamem code uses task-based ids today; we add a key column to make
// look-ups by stable name unambiguous).

func (s *pgStore) GetRecipeByKey(ctx context.Context, key string) (*Recipe, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("recipes: key is required")
	}
	const q = `
		SELECT id, COALESCE(recipe_key, id) AS key, task_pattern, stack, steps_json::text,
		       COALESCE(steps::text, '[]'),
		       COALESCE(executable, FALSE),
		       COALESCE(expected_duration_seconds, 0),
		       usage_count, created_at
		FROM aria_recipes
		WHERE recipe_key = $1 OR id = $1
		LIMIT 1`
	var r Recipe
	var stack pq.StringArray
	var legacyStepsJSON, stepsJSON string
	if err := s.db.QueryRowContext(ctx, q, key).Scan(
		&r.ID, &r.Key, &r.TaskPattern, &stack, &legacyStepsJSON, &stepsJSON,
		&r.Executable, &r.ExpectedDurationSeconds, &r.UsageCount, &r.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrRecipeNotFound
		}
		return nil, fmt.Errorf("recipes: get recipe: %w", err)
	}
	r.Stack = []string(stack)
	steps, err := ParseSteps([]byte(stepsJSON))
	if err != nil {
		return nil, err
	}
	if len(steps) == 0 && strings.TrimSpace(legacyStepsJSON) != "" {
		// Some legacy rows store a markdown blob in steps_json; we keep that
		// as the task pattern but expose no executable steps.
		steps = nil
	}
	r.Steps = steps
	return &r, nil
}

func (s *pgStore) ListRecipes(ctx context.Context, executableOnly bool) ([]*Recipe, error) {
	q := `
		SELECT id, COALESCE(recipe_key, id) AS key, task_pattern, stack,
		       COALESCE(steps::text, '[]'),
		       COALESCE(executable, FALSE),
		       COALESCE(expected_duration_seconds, 0),
		       usage_count, created_at
		FROM aria_recipes`
	if executableOnly {
		q += ` WHERE COALESCE(executable, FALSE) = TRUE`
	}
	q += ` ORDER BY usage_count DESC, created_at DESC LIMIT 200`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("recipes: list recipes: %w", err)
	}
	defer rows.Close()

	out := []*Recipe{}
	for rows.Next() {
		var r Recipe
		var stack pq.StringArray
		var stepsJSON string
		if err := rows.Scan(&r.ID, &r.Key, &r.TaskPattern, &stack, &stepsJSON,
			&r.Executable, &r.ExpectedDurationSeconds, &r.UsageCount, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("recipes: scan recipe: %w", err)
		}
		r.Stack = []string(stack)
		if steps, err := ParseSteps([]byte(stepsJSON)); err == nil {
			r.Steps = steps
		}
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *pgStore) UpsertRecipe(ctx context.Context, p UpsertRecipeInput) error {
	if strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("recipes: recipe id is required")
	}
	stepsBytes, err := MarshalSteps(p.Steps)
	if err != nil {
		return fmt.Errorf("recipes: marshal steps: %w", err)
	}
	key := strings.TrimSpace(p.Key)
	if key == "" {
		key = p.ID
	}
	const q = `
		INSERT INTO aria_recipes (id, recipe_key, task_pattern, stack, steps, steps_json, executable, expected_duration_seconds)
		VALUES ($1, $2, $3, $4, $5::jsonb, '[]'::jsonb, $6, $7)
		ON CONFLICT (id) DO UPDATE SET
			recipe_key = EXCLUDED.recipe_key,
			task_pattern = EXCLUDED.task_pattern,
			stack = EXCLUDED.stack,
			steps = EXCLUDED.steps,
			executable = EXCLUDED.executable,
			expected_duration_seconds = EXCLUDED.expected_duration_seconds`
	expected := p.ExpectedDurationSeconds
	if expected < 0 {
		expected = 0
	}
	if _, err := s.db.ExecContext(ctx, q, p.ID, key, p.TaskPattern, pq.Array(p.Stack), string(stepsBytes), p.Executable, expected); err != nil {
		return fmt.Errorf("recipes: upsert recipe: %w", err)
	}
	return nil
}

func (s *pgStore) IncrementUsage(ctx context.Context, recipeID string) error {
	if strings.TrimSpace(recipeID) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE aria_recipes SET usage_count = usage_count + 1 WHERE id = $1`, recipeID)
	if err != nil {
		return fmt.Errorf("recipes: increment usage: %w", err)
	}
	return nil
}

func (s *pgStore) CreateExecution(ctx context.Context, e *Execution) error {
	if strings.TrimSpace(e.ID) == "" {
		e.ID = uuid.NewString()
	}
	if e.StartedAt.IsZero() {
		e.StartedAt = time.Now().UTC()
	}
	if e.Status == "" {
		e.Status = StatusRunning
	}
	const q = `
		INSERT INTO aria_recipe_executions
		  (id, recipe_id, recipe_key, executed_by_uid, project, context, status, total_steps, completed_steps, failed_step_index, started_at)
		VALUES ($1::uuid, $2, $3, NULLIF($4,'')::uuid, NULLIF($5,''), $6::jsonb, $7, $8, $9, $10, $11)`
	contextJSON := strings.TrimSpace(e.Context)
	if contextJSON == "" {
		contextJSON = "{}"
	}
	failedIdx := e.FailedStepIndex
	if failedIdx == 0 && e.Status != StatusFailed {
		failedIdx = -1
	}
	if _, err := s.db.ExecContext(ctx, q,
		e.ID, e.RecipeID, e.RecipeKey, e.ExecutedByUID, e.Project, contextJSON,
		e.Status, e.TotalSteps, e.CompletedSteps, failedIdx, e.StartedAt,
	); err != nil {
		return fmt.Errorf("recipes: create execution: %w", err)
	}
	return nil
}

func (s *pgStore) UpdateExecutionStatus(ctx context.Context, executionID, status string, completed int, failedIndex int, finishedAt *time.Time, totalDurationMs int) error {
	const q = `
		UPDATE aria_recipe_executions
		SET status = $2,
		    completed_steps = $3,
		    failed_step_index = $4,
		    finished_at = $5,
		    total_duration_ms = $6
		WHERE id = $1::uuid`
	if _, err := s.db.ExecContext(ctx, q, executionID, status, completed, failedIndex, finishedAt, totalDurationMs); err != nil {
		return fmt.Errorf("recipes: update execution status: %w", err)
	}
	return nil
}

func (s *pgStore) InsertStepResult(ctx context.Context, executionID string, sr StepResult) error {
	const q = `
		INSERT INTO aria_recipe_step_results
		  (execution_id, step_index, step_kind, step_label, status, exit_code, stdout, stderr, duration_ms, started_at, finished_at)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (execution_id, step_index) DO UPDATE SET
		  step_kind = EXCLUDED.step_kind,
		  step_label = EXCLUDED.step_label,
		  status = EXCLUDED.status,
		  exit_code = EXCLUDED.exit_code,
		  stdout = EXCLUDED.stdout,
		  stderr = EXCLUDED.stderr,
		  duration_ms = EXCLUDED.duration_ms,
		  started_at = EXCLUDED.started_at,
		  finished_at = EXCLUDED.finished_at`
	durMs := int(sr.Duration / time.Millisecond)
	if _, err := s.db.ExecContext(ctx, q,
		executionID, sr.Index, sr.Kind, sr.Label, sr.Status, sr.ExitCode,
		sr.Stdout, sr.Stderr, durMs, sr.StartedAt, sr.FinishedAt,
	); err != nil {
		return fmt.Errorf("recipes: insert step result: %w", err)
	}
	return nil
}

func (s *pgStore) GetExecution(ctx context.Context, id string) (*Execution, error) {
	const head = `
		SELECT id::text, recipe_id, recipe_key, executed_by_uid::text, COALESCE(project,''),
		       COALESCE(context::text, '{}'),
		       status, total_steps, completed_steps, COALESCE(failed_step_index, -1),
		       started_at, finished_at, COALESCE(total_duration_ms, 0)
		FROM aria_recipe_executions
		WHERE id = $1::uuid`
	var e Execution
	var finishedAt sql.NullTime
	var executedBy sql.NullString
	if err := s.db.QueryRowContext(ctx, head, id).Scan(
		&e.ID, &e.RecipeID, &e.RecipeKey, &executedBy, &e.Project, &e.Context,
		&e.Status, &e.TotalSteps, &e.CompletedSteps, &e.FailedStepIndex,
		&e.StartedAt, &finishedAt, &e.TotalDurationMs,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrExecutionNotFound
		}
		return nil, fmt.Errorf("recipes: get execution: %w", err)
	}
	if executedBy.Valid {
		e.ExecutedByUID = executedBy.String
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		e.FinishedAt = &t
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT step_index, step_kind, COALESCE(step_label,''), status, COALESCE(exit_code,0),
		       COALESCE(stdout,''), COALESCE(stderr,''), COALESCE(duration_ms,0),
		       started_at, finished_at
		FROM aria_recipe_step_results
		WHERE execution_id = $1::uuid
		ORDER BY step_index ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("recipes: list step results: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sr StepResult
		var durMs int
		var sStarted, sFinished sql.NullTime
		if err := rows.Scan(&sr.Index, &sr.Kind, &sr.Label, &sr.Status, &sr.ExitCode,
			&sr.Stdout, &sr.Stderr, &durMs, &sStarted, &sFinished); err != nil {
			return nil, fmt.Errorf("recipes: scan step result: %w", err)
		}
		sr.Duration = time.Duration(durMs) * time.Millisecond
		if sStarted.Valid {
			t := sStarted.Time
			sr.StartedAt = &t
		}
		if sFinished.Valid {
			t := sFinished.Time
			sr.FinishedAt = &t
		}
		e.Steps = append(e.Steps, sr)
	}
	return &e, rows.Err()
}

func (s *pgStore) ListExecutions(ctx context.Context, filter ExecFilter, limit int) ([]Execution, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	conds := []string{"1=1"}
	args := []any{}
	idx := 1
	if v := strings.TrimSpace(filter.RecipeKey); v != "" {
		conds = append(conds, fmt.Sprintf("recipe_key = $%d", idx))
		args = append(args, v)
		idx++
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		conds = append(conds, fmt.Sprintf("status = $%d", idx))
		args = append(args, v)
		idx++
	}
	if v := strings.TrimSpace(filter.ExecutedByUID); v != "" {
		conds = append(conds, fmt.Sprintf("executed_by_uid = $%d::uuid", idx))
		args = append(args, v)
		idx++
	}
	if v := strings.TrimSpace(filter.Project); v != "" {
		conds = append(conds, fmt.Sprintf("project = $%d", idx))
		args = append(args, v)
		idx++
	}
	if filter.Since != nil {
		conds = append(conds, fmt.Sprintf("started_at >= $%d", idx))
		args = append(args, *filter.Since)
		idx++
	}
	args = append(args, limit)
	q := fmt.Sprintf(`
		SELECT id::text, recipe_id, recipe_key, COALESCE(executed_by_uid::text,''),
		       COALESCE(project,''), status, total_steps, completed_steps,
		       COALESCE(failed_step_index,-1), started_at, finished_at,
		       COALESCE(total_duration_ms,0)
		FROM aria_recipe_executions
		WHERE %s
		ORDER BY started_at DESC
		LIMIT $%d`, strings.Join(conds, " AND "), idx)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("recipes: list executions: %w", err)
	}
	defer rows.Close()

	out := []Execution{}
	for rows.Next() {
		var e Execution
		var finishedAt sql.NullTime
		if err := rows.Scan(&e.ID, &e.RecipeID, &e.RecipeKey, &e.ExecutedByUID,
			&e.Project, &e.Status, &e.TotalSteps, &e.CompletedSteps,
			&e.FailedStepIndex, &e.StartedAt, &finishedAt, &e.TotalDurationMs); err != nil {
			return nil, fmt.Errorf("recipes: scan execution: %w", err)
		}
		if finishedAt.Valid {
			t := finishedAt.Time
			e.FinishedAt = &t
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *pgStore) RecipeStats(ctx context.Context, recipeKey string, since time.Time) (RecipeStats, error) {
	st := RecipeStats{RecipeKey: recipeKey}
	const q = `
		SELECT COUNT(*),
		       COUNT(*) FILTER (WHERE status = 'success'),
		       COUNT(*) FILTER (WHERE status = 'failed'),
		       COALESCE(AVG(NULLIF(total_duration_ms,0))::int, 0),
		       MAX(started_at)
		FROM aria_recipe_executions
		WHERE recipe_key = $1 AND started_at >= $2`
	var lastRun sql.NullTime
	if err := s.db.QueryRowContext(ctx, q, recipeKey, since).Scan(
		&st.TotalRuns, &st.SuccessCount, &st.FailureCount,
		&st.AvgDurationMs, &lastRun,
	); err != nil {
		return st, fmt.Errorf("recipes: stats: %w", err)
	}
	if st.TotalRuns > 0 {
		st.SuccessRate = float64(st.SuccessCount) / float64(st.TotalRuns)
	}
	if lastRun.Valid {
		t := lastRun.Time
		st.LastRunAt = &t
		_ = s.db.QueryRowContext(ctx, `
			SELECT status FROM aria_recipe_executions
			WHERE recipe_key = $1 AND started_at = $2
			LIMIT 1`, recipeKey, t).Scan(&st.LastRunStatus)
	}
	return st, nil
}

// Compile-time assertions.
var _ json.Marshaler = (*nullExecution)(nil)

type nullExecution struct{}

func (nullExecution) MarshalJSON() ([]byte, error) { return []byte("null"), nil }
