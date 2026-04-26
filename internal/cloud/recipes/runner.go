package recipes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// VaultBridge is the runner-facing contract used to resolve+reveal vault
// secrets for vault_use steps. Implemented by the cloud vault adapter.
type VaultBridge interface {
	// ResolveAndReveal returns name->plaintext for the given secret names,
	// using the principal (uid) for ACL checks. The bridge is responsible
	// for logging the audit entries; the runner just consumes the values.
	ResolveAndReveal(ctx context.Context, names []string, byUID, reason string) (map[string]string, error)
}

// AriaMemBridge is the contract used by aria_save steps to persist the
// rendered observation back into ARIA's memory. The runner does not carry
// any state about scope/sensitivity policy; it just hands off the params.
type AriaMemBridge interface {
	Save(ctx context.Context, p AriaSaveBridgeInput) error
}

// AriaSaveBridgeInput is the payload for an aria_save step.
type AriaSaveBridgeInput struct {
	Title        string
	Type         string
	Scope        string
	Project      string
	TopicKey     string
	Content      string
	DeveloperUID string
	Source       string
}

// ShellExecutor abstracts os/exec so unit tests can inject deterministic fakes.
// Implementations must mask any provided secrets in the returned stdout/stderr.
type ShellExecutor interface {
	Run(ctx context.Context, command, cwd string, env []string, secrets map[string]string, timeout time.Duration) (stdout, stderr string, exitCode int, err error)
}

// HTTPExecutor abstracts the HTTP client used by step kind=http. Returning
// just (status, body, err) keeps the interface narrow.
type HTTPExecutor interface {
	Do(ctx context.Context, method, url string, body []byte, timeout time.Duration) (status int, respBody string, err error)
}

// PauseHandler decides what to do when a pause_for_human step is hit.
// Default impl simply records "paused" and continues — the dashboard will
// surface the prompt and let the operator click Resume; an interactive CLI
// prompt is plugged in via WithPauseHandler.
type PauseHandler interface {
	HandlePause(ctx context.Context, prompt string) (proceed bool, note string, err error)
}

// Option configures a Runner.
type Option func(*Runner)

// WithVault attaches the vault bridge for vault_use steps.
func WithVault(v VaultBridge) Option { return func(r *Runner) { r.vault = v } }

// WithAriaMem attaches the aria-memory bridge for aria_save steps.
func WithAriaMem(a AriaMemBridge) Option { return func(r *Runner) { r.ariaMem = a } }

// WithShellExecutor overrides the default os/exec-backed shell executor.
func WithShellExecutor(s ShellExecutor) Option { return func(r *Runner) { r.shellExec = s } }

// WithHTTPExecutor overrides the default net/http-backed executor.
func WithHTTPExecutor(h HTTPExecutor) Option { return func(r *Runner) { r.httpExec = h } }

// WithPauseHandler overrides the default no-op pause handler.
func WithPauseHandler(p PauseHandler) Option { return func(r *Runner) { r.pauseHandler = p } }

// WithDefaultProject sets the project string recorded on each execution row
// when the caller does not override it.
func WithDefaultProject(p string) Option {
	return func(r *Runner) { r.defaultProject = strings.TrimSpace(p) }
}

// Runner orchestrates recipe execution + telemetry.
type Runner struct {
	store          RecipeStore
	vault          VaultBridge
	ariaMem        AriaMemBridge
	shellExec      ShellExecutor
	httpExec       HTTPExecutor
	pauseHandler   PauseHandler
	defaultProject string

	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc
}

// NewRunner constructs a Runner with sensible defaults. Pass options to wire
// vault, aria_save, alternative shell/http executors, etc.
func NewRunner(s RecipeStore, opts ...Option) *Runner {
	r := &Runner{
		store:    s,
		shellExec: defaultShellExecutor{},
		httpExec:  defaultHTTPExecutor{},
		pauseHandler: defaultPauseHandler{},
		cancels:  make(map[string]context.CancelFunc),
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ExecuteParams parametrizes Execute.
type ExecuteParams struct {
	RecipeKey     string
	Vars          map[string]string
	ExecutedByUID string
	Project       string
	DryRun        bool
}

// Execute runs the recipe identified by key sequentially. Each step's outcome
// is persisted as it completes; the returned Execution is the final view.
//
// Behavior:
//   - vars are exposed to step templates via {{ .Vars.* }}.
//   - vault_use steps fetch secrets via VaultBridge and mask them in stdout.
//   - aria_save steps render ContentTemplate against {{ .Prev.* }} and call AriaMemBridge.
//   - Failure: the runner records FailedStepIndex and stops (unless ContinueOnError).
//   - DryRun: returns a structured plan without invoking executors.
func (r *Runner) Execute(ctx context.Context, p ExecuteParams) (*Execution, error) {
	recipe, err := r.store.GetRecipeByKey(ctx, p.RecipeKey)
	if err != nil {
		return nil, err
	}
	if !recipe.Executable {
		return nil, ErrRecipeNotExecutable
	}
	if len(recipe.Steps) == 0 {
		return nil, fmt.Errorf("recipes: recipe %q has no steps", p.RecipeKey)
	}

	project := strings.TrimSpace(p.Project)
	if project == "" {
		project = r.defaultProject
	}

	contextJSON, _ := json.Marshal(map[string]any{
		"vars":   p.Vars,
		"dryRun": p.DryRun,
	})

	exec := &Execution{
		RecipeID:        recipe.ID,
		RecipeKey:       recipe.Key,
		ExecutedByUID:   p.ExecutedByUID,
		Project:         project,
		Context:         string(contextJSON),
		Status:          StatusRunning,
		TotalSteps:      len(recipe.Steps),
		FailedStepIndex: -1,
		StartedAt:       time.Now().UTC(),
	}

	if p.DryRun {
		// Build a synthetic plan; do not persist.
		exec.Status = StatusSuccess
		exec.Steps = make([]StepResult, len(recipe.Steps))
		for i, s := range recipe.Steps {
			exec.Steps[i] = StepResult{
				Index:  i,
				Kind:   string(s.Kind),
				Label:  s.Label,
				Status: StatusSkipped,
			}
		}
		now := time.Now().UTC()
		exec.FinishedAt = &now
		return exec, nil
	}

	if err := r.store.CreateExecution(ctx, exec); err != nil {
		return nil, err
	}

	// Track cancel hook for Cancel().
	runCtx, cancel := context.WithCancel(ctx)
	r.cancelMu.Lock()
	r.cancels[exec.ID] = cancel
	r.cancelMu.Unlock()
	defer func() {
		r.cancelMu.Lock()
		delete(r.cancels, exec.ID)
		r.cancelMu.Unlock()
		cancel()
	}()

	results := make([]StepResult, 0, len(recipe.Steps))
	startGlobal := time.Now()

	completed := 0
	failedIndex := -1
	finalStatus := StatusSuccess

	for i, step := range recipe.Steps {
		if runCtx.Err() != nil {
			finalStatus = StatusCancelled
			break
		}
		var prev *StepResult
		if len(results) > 0 {
			p := results[len(results)-1]
			prev = &p
		}
		tCtx := TemplateContext{Vars: p.Vars, Prev: prev, AllSteps: append([]StepResult(nil), results...)}

		sr := r.runStep(runCtx, exec.ID, p.ExecutedByUID, i, step, tCtx)
		results = append(results, sr)
		// Persist step result before deciding next-step strategy.
		_ = r.store.InsertStepResult(runCtx, exec.ID, sr)

		if sr.Status == StatusSuccess {
			completed++
			continue
		}
		if step.ContinueOnError {
			completed++
			continue
		}
		failedIndex = i
		finalStatus = StatusFailed
		break
	}

	finished := time.Now().UTC()
	totalMs := int(time.Since(startGlobal) / time.Millisecond)

	if finalStatus == StatusSuccess {
		_ = r.store.IncrementUsage(ctx, recipe.ID)
	}

	_ = r.store.UpdateExecutionStatus(ctx, exec.ID, finalStatus, completed, failedIndex, &finished, totalMs)

	exec.Status = finalStatus
	exec.CompletedSteps = completed
	exec.FailedStepIndex = failedIndex
	exec.FinishedAt = &finished
	exec.TotalDurationMs = totalMs
	exec.Steps = results
	return exec, nil
}

// Cancel signals an in-flight execution to abort. No-op if execution is not running.
func (r *Runner) Cancel(ctx context.Context, executionID string) error {
	r.cancelMu.Lock()
	cancel, ok := r.cancels[executionID]
	r.cancelMu.Unlock()
	if !ok {
		return ErrExecutionNotFound
	}
	cancel()
	now := time.Now().UTC()
	return r.store.UpdateExecutionStatus(ctx, executionID, StatusCancelled, 0, -1, &now, 0)
}

// GetExecution proxies to the store.
func (r *Runner) GetExecution(ctx context.Context, id string) (*Execution, error) {
	return r.store.GetExecution(ctx, id)
}

// ListExecutions proxies to the store.
func (r *Runner) ListExecutions(ctx context.Context, filter ExecFilter, limit int) ([]Execution, error) {
	return r.store.ListExecutions(ctx, filter, limit)
}

// ListRecipes returns the catalog (executable-only or all).
func (r *Runner) ListRecipes(ctx context.Context, executableOnly bool) ([]*Recipe, error) {
	return r.store.ListRecipes(ctx, executableOnly)
}

// GetRecipe loads a recipe by stable key.
func (r *Runner) GetRecipe(ctx context.Context, key string) (*Recipe, error) {
	return r.store.GetRecipeByKey(ctx, key)
}

// Stats returns rolling success-rate + avg duration since the given window.
func (r *Runner) Stats(ctx context.Context, recipeKey string, since time.Time) (RecipeStats, error) {
	return r.store.RecipeStats(ctx, recipeKey, since)
}

// SeedBuiltins inserts the 3 builtin recipes (deploy / backup / onboard).
// Idempotent — uses ON CONFLICT(id) DO UPDATE in the store.
func (r *Runner) SeedBuiltins(ctx context.Context) error {
	for _, rec := range BuiltinRecipes() {
		if err := r.store.UpsertRecipe(ctx, rec); err != nil {
			return fmt.Errorf("recipes: seed %q: %w", rec.Key, err)
		}
	}
	return nil
}

// runStep dispatches by kind and produces a StepResult. Errors from executors
// are NOT panic-fatal — they bubble into the StepResult so the run can decide
// whether to continue (ContinueOnError) or fail-stop.
func (r *Runner) runStep(ctx context.Context, executionID, byUID string, idx int, step Step, tCtx TemplateContext) StepResult {
	now := time.Now().UTC()
	sr := StepResult{
		Index:     idx,
		Kind:      string(step.Kind),
		Label:     step.Label,
		Status:    StatusRunning,
		StartedAt: &now,
	}
	start := time.Now()

	switch step.Kind {
	case StepShell:
		execShell(ctx, r.shellExec, &sr, step, tCtx)
	case StepHTTP:
		execHTTP(ctx, r.httpExec, &sr, step, tCtx)
	case StepAriaSave:
		execAriaSave(ctx, r.ariaMem, &sr, step, tCtx, byUID)
	case StepVaultUse:
		execVaultUse(ctx, r.shellExec, r.vault, &sr, step, tCtx, byUID)
	case StepPause:
		execPause(ctx, r.pauseHandler, &sr, step, tCtx)
	default:
		sr.Status = StatusFailed
		sr.Stderr = fmt.Sprintf("unsupported step kind: %s", step.Kind)
	}

	finish := time.Now().UTC()
	sr.FinishedAt = &finish
	sr.Duration = time.Since(start)
	return sr
}
