package recipes

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeStore is an in-memory RecipeStore implementation for unit tests.
type fakeStore struct {
	mu          sync.Mutex
	recipes     map[string]*Recipe
	executions  map[string]*Execution
	steps       map[string][]StepResult
	usage       map[string]int
	failNextSet bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		recipes:    make(map[string]*Recipe),
		executions: make(map[string]*Execution),
		steps:      make(map[string][]StepResult),
		usage:      make(map[string]int),
	}
}

func (f *fakeStore) GetRecipeByKey(ctx context.Context, key string) (*Recipe, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r, ok := f.recipes[key]; ok {
		return r, nil
	}
	return nil, ErrRecipeNotFound
}

func (f *fakeStore) ListRecipes(ctx context.Context, executableOnly bool) ([]*Recipe, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []*Recipe{}
	for _, r := range f.recipes {
		if executableOnly && !r.Executable {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeStore) UpsertRecipe(ctx context.Context, p UpsertRecipeInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.recipes[p.Key] = &Recipe{
		ID:                      p.ID,
		Key:                     p.Key,
		TaskPattern:             p.TaskPattern,
		Stack:                   p.Stack,
		Steps:                   p.Steps,
		Executable:              p.Executable,
		ExpectedDurationSeconds: p.ExpectedDurationSeconds,
	}
	return nil
}

func (f *fakeStore) IncrementUsage(ctx context.Context, recipeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.usage[recipeID]++
	return nil
}

func (f *fakeStore) CreateExecution(ctx context.Context, e *Execution) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e.ID == "" {
		e.ID = "exec-" + e.RecipeKey
	}
	cp := *e
	f.executions[e.ID] = &cp
	return nil
}

func (f *fakeStore) UpdateExecutionStatus(ctx context.Context, executionID, status string, completed int, failedIndex int, finishedAt *time.Time, totalDurationMs int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.executions[executionID]
	if !ok {
		return ErrExecutionNotFound
	}
	e.Status = status
	e.CompletedSteps = completed
	e.FailedStepIndex = failedIndex
	e.FinishedAt = finishedAt
	e.TotalDurationMs = totalDurationMs
	return nil
}

func (f *fakeStore) InsertStepResult(ctx context.Context, executionID string, sr StepResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps[executionID] = append(f.steps[executionID], sr)
	return nil
}

func (f *fakeStore) GetExecution(ctx context.Context, id string) (*Execution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.executions[id]
	if !ok {
		return nil, ErrExecutionNotFound
	}
	cp := *e
	cp.Steps = append([]StepResult(nil), f.steps[id]...)
	return &cp, nil
}

func (f *fakeStore) ListExecutions(ctx context.Context, filter ExecFilter, limit int) ([]Execution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []Execution{}
	for _, e := range f.executions {
		if filter.RecipeKey != "" && e.RecipeKey != filter.RecipeKey {
			continue
		}
		out = append(out, *e)
	}
	return out, nil
}

func (f *fakeStore) RecipeStats(ctx context.Context, recipeKey string, since time.Time) (RecipeStats, error) {
	st := RecipeStats{RecipeKey: recipeKey}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, e := range f.executions {
		if e.RecipeKey != recipeKey {
			continue
		}
		st.TotalRuns++
		if e.Status == StatusSuccess {
			st.SuccessCount++
		} else if e.Status == StatusFailed {
			st.FailureCount++
		}
	}
	if st.TotalRuns > 0 {
		st.SuccessRate = float64(st.SuccessCount) / float64(st.TotalRuns)
	}
	return st, nil
}

// fakeShell records every Run() invocation and returns scripted outputs.
type fakeShell struct {
	mu      sync.Mutex
	runs    []string
	scripts map[string]struct {
		stdout, stderr string
		exit           int
	}
}

func newFakeShell() *fakeShell {
	return &fakeShell{
		scripts: map[string]struct {
			stdout, stderr string
			exit           int
		}{},
	}
}

func (f *fakeShell) script(cmd, stdout, stderr string, exit int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts[cmd] = struct {
		stdout, stderr string
		exit           int
	}{stdout, stderr, exit}
}

func (f *fakeShell) Run(ctx context.Context, command, cwd string, env []string, secrets map[string]string, timeout time.Duration) (string, string, int, error) {
	f.mu.Lock()
	f.runs = append(f.runs, command)
	scripted, ok := f.scripts[command]
	f.mu.Unlock()
	if !ok {
		return "", "", 0, nil
	}
	stdout := MaskValues(scripted.stdout, secrets)
	stderr := MaskValues(scripted.stderr, secrets)
	return stdout, stderr, scripted.exit, nil
}

// fakeHTTP returns scripted responses keyed by URL.
type fakeHTTP struct {
	scripts map[string]struct {
		status int
		body   string
		err    error
	}
}

func newFakeHTTP() *fakeHTTP {
	return &fakeHTTP{scripts: map[string]struct {
		status int
		body   string
		err    error
	}{}}
}

func (f *fakeHTTP) script(url string, status int, body string) {
	f.scripts[url] = struct {
		status int
		body   string
		err    error
	}{status, body, nil}
}

func (f *fakeHTTP) Do(ctx context.Context, method, url string, body []byte, timeout time.Duration) (int, string, error) {
	s, ok := f.scripts[url]
	if !ok {
		return 0, "", errors.New("no script for " + url)
	}
	return s.status, s.body, s.err
}

// fakeAriaMem records save calls.
type fakeAriaMem struct {
	saves []AriaSaveBridgeInput
}

func (f *fakeAriaMem) Save(ctx context.Context, p AriaSaveBridgeInput) error {
	f.saves = append(f.saves, p)
	return nil
}

// fakeVault returns canned secret values.
type fakeVault struct {
	values map[string]string
}

func (f *fakeVault) ResolveAndReveal(ctx context.Context, names []string, byUID, reason string) (map[string]string, error) {
	out := make(map[string]string, len(names))
	for _, n := range names {
		v, ok := f.values[n]
		if !ok {
			return nil, errors.New("secret not found: " + n)
		}
		out[n] = v
	}
	return out, nil
}

func TestRunner_ShellSuccess(t *testing.T) {
	st := newFakeStore()
	sh := newFakeShell()
	sh.script("echo hi", "hi\n", "", 0)
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID:    "r1", Key: "shell-ok", Executable: true,
		Steps: []Step{NewShellStep("say", "echo hi", "", 0)},
	})
	r := NewRunner(st, WithShellExecutor(sh))
	exec, err := r.Execute(context.Background(), ExecuteParams{RecipeKey: "shell-ok"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if exec.Status != StatusSuccess {
		t.Fatalf("expected success, got %s", exec.Status)
	}
	if exec.CompletedSteps != 1 {
		t.Fatalf("expected 1 completed, got %d", exec.CompletedSteps)
	}
	if exec.Steps[0].Stdout != "hi\n" {
		t.Fatalf("stdout mismatch: %q", exec.Steps[0].Stdout)
	}
}

func TestRunner_FailMidStepStops(t *testing.T) {
	st := newFakeStore()
	sh := newFakeShell()
	sh.script("ok1", "", "", 0)
	sh.script("boom", "", "kaboom", 1)
	sh.script("ok2", "", "", 0)
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r2", Key: "midfail", Executable: true,
		Steps: []Step{
			NewShellStep("a", "ok1", "", 0),
			NewShellStep("b", "boom", "", 0),
			NewShellStep("c", "ok2", "", 0),
		},
	})
	r := NewRunner(st, WithShellExecutor(sh))
	exec, err := r.Execute(context.Background(), ExecuteParams{RecipeKey: "midfail"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if exec.Status != StatusFailed {
		t.Fatalf("expected failed, got %s", exec.Status)
	}
	if exec.FailedStepIndex != 1 {
		t.Fatalf("expected failed_index=1, got %d", exec.FailedStepIndex)
	}
	if len(sh.runs) != 2 {
		t.Fatalf("expected 2 shell runs (no run after fail), got %d", len(sh.runs))
	}
}

func TestRunner_ContinueOnError(t *testing.T) {
	st := newFakeStore()
	sh := newFakeShell()
	sh.script("boom", "", "kaboom", 1)
	sh.script("ok", "ok", "", 0)
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r3", Key: "cont", Executable: true,
		Steps: []Step{
			{Kind: StepShell, Label: "boom", Command: "boom", ContinueOnError: true},
			NewShellStep("ok", "ok", "", 0),
		},
	})
	r := NewRunner(st, WithShellExecutor(sh))
	exec, _ := r.Execute(context.Background(), ExecuteParams{RecipeKey: "cont"})
	if exec.Status != StatusSuccess {
		t.Fatalf("expected success with continue_on_error, got %s", exec.Status)
	}
	if exec.CompletedSteps != 2 {
		t.Fatalf("expected 2 completed, got %d", exec.CompletedSteps)
	}
}

func TestRunner_VarSubstitution(t *testing.T) {
	st := newFakeStore()
	sh := newFakeShell()
	sh.script("git pull origin main", "Already up to date", "", 0)
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r4", Key: "vars", Executable: true,
		Steps: []Step{NewShellStep("pull", "git pull origin {{ .Vars.branch }}", "", 0)},
	})
	r := NewRunner(st, WithShellExecutor(sh))
	exec, _ := r.Execute(context.Background(), ExecuteParams{
		RecipeKey: "vars",
		Vars:      map[string]string{"branch": "main"},
	})
	if exec.Status != StatusSuccess {
		t.Fatalf("expected success, got %s", exec.Status)
	}
	if sh.runs[0] != "git pull origin main" {
		t.Fatalf("var substitution failed: %q", sh.runs[0])
	}
}

func TestRunner_AriaSaveUsesPrevStdout(t *testing.T) {
	st := newFakeStore()
	sh := newFakeShell()
	sh.script("echo done", "deploy ok", "", 0)
	mem := &fakeAriaMem{}
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r5", Key: "save", Executable: true,
		Steps: []Step{
			NewShellStep("smoke", "echo done", "", 0),
			NewAriaSaveStep("record", "Deploy", "deploy", "project", "aria-core", "deploy-last",
				"smoke output: {{ .Prev.Stdout }}"),
		},
	})
	r := NewRunner(st, WithShellExecutor(sh), WithAriaMem(mem))
	exec, _ := r.Execute(context.Background(), ExecuteParams{RecipeKey: "save", ExecutedByUID: "u1"})
	if exec.Status != StatusSuccess {
		t.Fatalf("expected success, got %s: %#v", exec.Status, exec.Steps)
	}
	if len(mem.saves) != 1 {
		t.Fatalf("expected 1 save, got %d", len(mem.saves))
	}
	if !strings.Contains(mem.saves[0].Content, "deploy ok") {
		t.Fatalf("template did not render Prev.Stdout: %q", mem.saves[0].Content)
	}
}

func TestRunner_VaultUseMasksSecrets(t *testing.T) {
	st := newFakeStore()
	sh := newFakeShell()
	sh.script("echo $TOKEN", "leak: hunter2-very-secret", "", 0)
	v := &fakeVault{values: map[string]string{"TOKEN": "hunter2-very-secret"}}
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r6", Key: "vault", Executable: true,
		Steps: []Step{NewVaultUseStep("use", "echo $TOKEN", []string{"TOKEN"}, 0)},
	})
	r := NewRunner(st, WithShellExecutor(sh), WithVault(v))
	exec, _ := r.Execute(context.Background(), ExecuteParams{RecipeKey: "vault"})
	if exec.Status != StatusSuccess {
		t.Fatalf("expected success, got %s: %#v", exec.Status, exec.Steps)
	}
	if strings.Contains(exec.Steps[0].Stdout, "hunter2-very-secret") {
		t.Fatalf("secret leaked into stdout: %q", exec.Steps[0].Stdout)
	}
	if !strings.Contains(exec.Steps[0].Stdout, "<<MASKED:TOKEN>>") {
		t.Fatalf("expected masked marker, got %q", exec.Steps[0].Stdout)
	}
}

func TestRunner_DryRunDoesNotInvoke(t *testing.T) {
	st := newFakeStore()
	sh := newFakeShell()
	sh.script("rm -rf /tmp/foo", "should-not-run", "", 0)
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r7", Key: "dry", Executable: true,
		Steps: []Step{NewShellStep("rm", "rm -rf /tmp/foo", "", 0)},
	})
	r := NewRunner(st, WithShellExecutor(sh))
	exec, err := r.Execute(context.Background(), ExecuteParams{RecipeKey: "dry", DryRun: true})
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if len(sh.runs) != 0 {
		t.Fatalf("dry run invoked shell: %v", sh.runs)
	}
	if exec.Status != StatusSuccess {
		t.Fatalf("dry run should be success, got %s", exec.Status)
	}
	if exec.Steps[0].Status != StatusSkipped {
		t.Fatalf("dry-run step should be skipped, got %s", exec.Steps[0].Status)
	}
}

func TestRunner_HTTPExpectedStatus(t *testing.T) {
	st := newFakeStore()
	h := newFakeHTTP()
	h.script("https://x/health", 200, "ok")
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r8", Key: "http", Executable: true,
		Steps: []Step{NewHTTPStep("ping", "GET", "https://x/health", 200, nil)},
	})
	r := NewRunner(st, WithHTTPExecutor(h))
	exec, _ := r.Execute(context.Background(), ExecuteParams{RecipeKey: "http"})
	if exec.Status != StatusSuccess {
		t.Fatalf("expected success, got %s", exec.Status)
	}
}

func TestRunner_NonExecutableRejected(t *testing.T) {
	st := newFakeStore()
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r9", Key: "manual", Executable: false,
		Steps: []Step{NewShellStep("a", "true", "", 0)},
	})
	r := NewRunner(st)
	_, err := r.Execute(context.Background(), ExecuteParams{RecipeKey: "manual"})
	if !errors.Is(err, ErrRecipeNotExecutable) {
		t.Fatalf("expected ErrRecipeNotExecutable, got %v", err)
	}
}

func TestParseSteps_RejectUnknownKind(t *testing.T) {
	_, err := ParseSteps([]byte(`[{"kind":"weird","label":"x"}]`))
	if err == nil {
		t.Fatalf("expected error for unknown kind")
	}
}

func TestRenderString_VarsAndPrev(t *testing.T) {
	out := RenderString("hello {{ .Vars.name }} prev={{ .Prev.Stdout }}", TemplateContext{
		Vars: map[string]string{"name": "world"},
		Prev: &StepResult{Stdout: "abc"},
	})
	if out != "hello world prev=abc" {
		t.Fatalf("got %q", out)
	}
}

func TestMaskValues_SkipShortSecrets(t *testing.T) {
	in := "key=abc longer=hunter2-very"
	out := MaskValues(in, map[string]string{"SHORT": "abc", "LONG": "hunter2-very"})
	if strings.Contains(out, "hunter2-very") {
		t.Fatalf("LONG was not masked: %q", out)
	}
	if !strings.Contains(out, "abc") {
		t.Fatalf("SHORT (under 6 chars) should not be masked: %q", out)
	}
}

func TestExecutorPause_ProceedFalseAborts(t *testing.T) {
	st := newFakeStore()
	_ = st.UpsertRecipe(context.Background(), UpsertRecipeInput{
		ID: "r10", Key: "pause", Executable: true,
		Steps: []Step{NewPauseStep("approve", "approve?")},
	})
	r := NewRunner(st, WithPauseHandler(stubPause{proceed: false}))
	exec, _ := r.Execute(context.Background(), ExecuteParams{RecipeKey: "pause"})
	if exec.Status != StatusFailed {
		t.Fatalf("expected failed when pause aborted, got %s", exec.Status)
	}
}

type stubPause struct{ proceed bool }

func (s stubPause) HandlePause(ctx context.Context, prompt string) (bool, string, error) {
	return s.proceed, "", nil
}

func TestSeedBuiltinsIsIdempotent(t *testing.T) {
	st := newFakeStore()
	r := NewRunner(st)
	if err := r.SeedBuiltins(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := r.SeedBuiltins(context.Background()); err != nil {
		t.Fatalf("seed twice: %v", err)
	}
	rs, _ := r.ListRecipes(context.Background(), true)
	if len(rs) < 3 {
		t.Fatalf("expected >=3 builtins, got %d", len(rs))
	}
}
