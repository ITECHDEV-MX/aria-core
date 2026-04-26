package cloudserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/recipes"
)

// RecipeRunnerService is the cloudserver-facing contract for the recipe runner.
// Implemented by *recipes.Runner via a thin adapter (see cmd/aria-core/cloud.go).
type RecipeRunnerService interface {
	Execute(ctx context.Context, p recipes.ExecuteParams) (*recipes.Execution, error)
	Cancel(ctx context.Context, executionID string) error
	GetExecution(ctx context.Context, id string) (*recipes.Execution, error)
	ListExecutions(ctx context.Context, filter recipes.ExecFilter, limit int) ([]recipes.Execution, error)
	ListRecipes(ctx context.Context, executableOnly bool) ([]*recipes.Recipe, error)
	GetRecipe(ctx context.Context, key string) (*recipes.Recipe, error)
	Stats(ctx context.Context, recipeKey string, since time.Time) (recipes.RecipeStats, error)
}

// WithRecipeRunner inyecta el recipe runner en el cloudserver.
func WithRecipeRunner(rr RecipeRunnerService) Option {
	return func(s *CloudServer) {
		s.recipes = rr
	}
}

// === Handlers ===

type v1RecipeRunRequest struct {
	RecipeKey string            `json:"recipe_key"`
	Vars      map[string]string `json:"vars,omitempty"`
	Project   string            `json:"project,omitempty"`
	DryRun    bool              `json:"dry_run,omitempty"`
}

func (s *CloudServer) handleV1RecipeRun(w http.ResponseWriter, r *http.Request) {
	if s.recipes == nil {
		http.Error(w, `{"error":"recipe runner not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64*1024)
	var req v1RecipeRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.RecipeKey) == "" {
		http.Error(w, `{"error":"recipe_key is required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	if claims != nil {
		byUID = claims.UID
	}
	exec, err := s.recipes.Execute(r.Context(), recipes.ExecuteParams{
		RecipeKey:     req.RecipeKey,
		Vars:          req.Vars,
		Project:       req.Project,
		ExecutedByUID: byUID,
		DryRun:        req.DryRun,
	})
	if err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, recipes.ErrRecipeNotFound) {
			status = http.StatusNotFound
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, status)
		return
	}
	jsonResponse(w, http.StatusOK, summarizeExecution(exec))
}

func (s *CloudServer) handleV1RecipeExecutionGet(w http.ResponseWriter, r *http.Request) {
	if s.recipes == nil {
		http.Error(w, `{"error":"recipe runner not configured"}`, http.StatusServiceUnavailable)
		return
	}
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, `{"error":"id required"}`, http.StatusBadRequest)
		return
	}
	exec, err := s.recipes.GetExecution(r.Context(), id)
	if err != nil {
		if errors.Is(err, recipes.ErrExecutionNotFound) {
			http.Error(w, `{"error":"execution not found"}`, http.StatusNotFound)
			return
		}
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, exec)
}

func (s *CloudServer) handleV1RecipeList(w http.ResponseWriter, r *http.Request) {
	if s.recipes == nil {
		http.Error(w, `{"error":"recipe runner not configured"}`, http.StatusServiceUnavailable)
		return
	}
	executableOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("executable")), "true")
	rs, err := s.recipes.ListRecipes(r.Context(), executableOnly)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	out := make([]map[string]any, 0, len(rs))
	for _, rec := range rs {
		out = append(out, map[string]any{
			"id":                        rec.ID,
			"recipe_key":                rec.Key,
			"task_pattern":              rec.TaskPattern,
			"stack":                     rec.Stack,
			"executable":                rec.Executable,
			"expected_duration_seconds": rec.ExpectedDurationSeconds,
			"step_count":                len(rec.Steps),
			"usage_count":               rec.UsageCount,
		})
	}
	jsonResponse(w, http.StatusOK, map[string]any{"recipes": out, "count": len(out)})
}

func (s *CloudServer) handleV1RecipeExecutionList(w http.ResponseWriter, r *http.Request) {
	if s.recipes == nil {
		http.Error(w, `{"error":"recipe runner not configured"}`, http.StatusServiceUnavailable)
		return
	}
	filter := recipes.ExecFilter{
		RecipeKey:     strings.TrimSpace(r.URL.Query().Get("recipe_key")),
		Status:        strings.TrimSpace(r.URL.Query().Get("status")),
		ExecutedByUID: strings.TrimSpace(r.URL.Query().Get("executed_by_uid")),
		Project:       strings.TrimSpace(r.URL.Query().Get("project")),
	}
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			t := time.Now().UTC().Add(-time.Duration(d) * 24 * time.Hour)
			filter.Since = &t
		}
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	out, err := s.recipes.ListExecutions(r.Context(), filter, limit)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"executions": out, "count": len(out)})
}

// summarizeExecution shapes the runner output for the MCP/v1 contract.
func summarizeExecution(e *recipes.Execution) map[string]any {
	stepSummary := make([]map[string]any, 0, len(e.Steps))
	for _, s := range e.Steps {
		stepSummary = append(stepSummary, map[string]any{
			"index":       s.Index,
			"kind":        s.Kind,
			"label":       s.Label,
			"status":      s.Status,
			"exit_code":   s.ExitCode,
			"duration_ms": int(s.Duration / time.Millisecond),
		})
	}
	out := map[string]any{
		"execution_id":     e.ID,
		"status":           e.Status,
		"recipe_id":        e.RecipeID,
		"recipe_key":       e.RecipeKey,
		"completed_steps":  e.CompletedSteps,
		"total_steps":      e.TotalSteps,
		"duration_seconds": float64(e.TotalDurationMs) / 1000.0,
		"step_summary":     stepSummary,
		"started_at":       e.StartedAt.UTC().Format(time.RFC3339),
	}
	if e.FinishedAt != nil {
		out["finished_at"] = e.FinishedAt.UTC().Format(time.RFC3339)
	}
	if e.Status == recipes.StatusFailed && e.FailedStepIndex >= 0 && e.FailedStepIndex < len(e.Steps) {
		f := e.Steps[e.FailedStepIndex]
		out["failed_at"] = map[string]any{
			"index":  f.Index,
			"label":  f.Label,
			"stderr": f.Stderr,
		}
	} else {
		out["failed_at"] = nil
	}
	return out
}
