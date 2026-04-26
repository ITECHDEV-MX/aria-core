// Package recipes implements the executable recipe runner for ARIA Core.
//
// A "recipe" is a sequence of structured Steps (shell, http, aria_save,
// vault_use, pause_for_human) tied to a stable key like "deploy-aria-core".
// The Runner executes the steps sequentially, captures stdout/stderr/exit,
// records timings into aria_recipe_executions / aria_recipe_step_results,
// and masks any vault secret values in the persisted output.
//
// Why: today recipes are markdown-only — devs read them and execute manually,
// which is slow and error-prone. Executable recipes turn 15-min deploys into
// 2-min deploys and produce telemetry that powers the ROI dashboard.
package recipes

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// StepKind enumerates the supported step types for executable recipes.
type StepKind string

const (
	StepShell    StepKind = "shell"           // ejecutar comando shell, capturar stdout/stderr/exit
	StepHTTP     StepKind = "http"            // GET/POST con url y body
	StepAriaSave StepKind = "aria_save"       // crear observation con outcome
	StepVaultUse StepKind = "vault_use"       // ejecutar cmd con secret inyectado (delega a vault)
	StepPause    StepKind = "pause_for_human" // espera input/aprobación del dev
)

// Step is the structured definition of one recipe step. Persisted as JSONB
// in aria_recipes.steps and decoded on each Execute().
type Step struct {
	Kind            StepKind          `json:"kind"`
	Label           string            `json:"label"`
	Command         string            `json:"command,omitempty"`
	CWD             string            `json:"cwd,omitempty"`
	TimeoutSec      int               `json:"timeout_sec,omitempty"`
	ContinueOnError bool              `json:"continue_on_error,omitempty"`
	URL             string            `json:"url,omitempty"`
	Method          string            `json:"method,omitempty"`
	Body            json.RawMessage   `json:"body,omitempty"`
	ExpectedStatus  int               `json:"expected_status,omitempty"`
	Vars            map[string]string `json:"vars,omitempty"`
	Save            *AriaSaveStep     `json:"save,omitempty"`
	VaultUse        *VaultUseStep     `json:"vault_use,omitempty"`
	PausePrompt     string            `json:"pause_prompt,omitempty"`
}

// AriaSaveStep captures the params for an aria_save step kind.
// ContentTemplate supports Go template substitution against the previous
// outcome map (e.g. {{ .PrevStdout }}, {{ .Vars.branch }}).
type AriaSaveStep struct {
	Title           string `json:"title"`
	Type            string `json:"type"`
	Scope           string `json:"scope"`
	Project         string `json:"project"`
	TopicKey        string `json:"topic_key"`
	ContentTemplate string `json:"content_template"`
}

// VaultUseStep captures the params for a vault_use step kind.
// SecretNames are looked up in the vault and exported as env vars before the
// command runs. The runner masks the values in stdout/stderr before persisting.
type VaultUseStep struct {
	SecretNames []string `json:"secret_names"`
	Command     string   `json:"command"`
	CWD         string   `json:"cwd,omitempty"`
	TimeoutSec  int      `json:"timeout_sec,omitempty"`
}

// Recipe is the executable recipe loaded from aria_recipes.
type Recipe struct {
	ID                      string
	Key                     string
	TaskPattern             string
	Stack                   []string
	Steps                   []Step
	Executable              bool
	ExpectedDurationSeconds int
	UsageCount              int
	CreatedAt               time.Time
}

// Status values for executions and step results.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusSuccess   = "success"
	StatusFailed    = "failed"
	StatusSkipped   = "skipped"
	StatusCancelled = "cancelled"
)

// Execution is the runtime view of one recipe invocation.
type Execution struct {
	ID                string       `json:"id"`
	RecipeID          string       `json:"recipe_id"`
	RecipeKey         string       `json:"recipe_key"`
	ExecutedByUID     string       `json:"executed_by_uid"`
	Project           string       `json:"project,omitempty"`
	Context           string       `json:"context,omitempty"`
	Status            string       `json:"status"`
	TotalSteps        int          `json:"total_steps"`
	CompletedSteps    int          `json:"completed_steps"`
	FailedStepIndex   int          `json:"failed_step_index"`
	Steps             []StepResult `json:"steps"`
	StartedAt         time.Time    `json:"started_at"`
	FinishedAt        *time.Time   `json:"finished_at,omitempty"`
	TotalDurationMs   int          `json:"total_duration_ms"`
}

// StepResult captures one step's outcome for telemetry + dashboard drilldown.
type StepResult struct {
	Index      int           `json:"index"`
	Kind       string        `json:"kind"`
	Label      string        `json:"label"`
	Status     string        `json:"status"`
	ExitCode   int           `json:"exit_code"`
	Stdout     string        `json:"stdout"`
	Stderr     string        `json:"stderr"`
	Duration   time.Duration `json:"duration_ms"`
	StartedAt  *time.Time    `json:"started_at,omitempty"`
	FinishedAt *time.Time    `json:"finished_at,omitempty"`
}

// ExecFilter parametrizes ListExecutions for the dashboard.
type ExecFilter struct {
	RecipeKey     string
	Status        string
	ExecutedByUID string
	Project       string
	Since         *time.Time
}

// ParseSteps decodes a JSONB-stored steps payload into []Step.
// Empty/null payload is treated as "no steps" (legacy markdown-only recipe).
func ParseSteps(raw []byte) ([]Step, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		return nil, nil
	}
	var steps []Step
	if err := json.Unmarshal([]byte(s), &steps); err != nil {
		return nil, fmt.Errorf("recipes: parse steps: %w", err)
	}
	for i := range steps {
		if strings.TrimSpace(string(steps[i].Kind)) == "" {
			return nil, fmt.Errorf("recipes: steps[%d].kind is required", i)
		}
		if !validKind(steps[i].Kind) {
			return nil, fmt.Errorf("recipes: steps[%d].kind %q is not supported", i, steps[i].Kind)
		}
	}
	return steps, nil
}

// MarshalSteps serializes []Step to JSONB-ready bytes (always returns valid JSON).
func MarshalSteps(steps []Step) ([]byte, error) {
	if steps == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(steps)
}

func validKind(k StepKind) bool {
	switch k {
	case StepShell, StepHTTP, StepAriaSave, StepVaultUse, StepPause:
		return true
	}
	return false
}

// Errors surfaced by the runner.
var (
	ErrRecipeNotFound      = errors.New("recipes: recipe not found")
	ErrRecipeNotExecutable = errors.New("recipes: recipe is not marked executable")
	ErrExecutionNotFound   = errors.New("recipes: execution not found")
	ErrCancelled           = errors.New("recipes: cancelled")
)
