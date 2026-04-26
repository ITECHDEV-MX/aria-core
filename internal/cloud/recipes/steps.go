package recipes

import "encoding/json"

// AllStepKinds returns the canonical list of supported step kinds.
// Used by docs / dashboard catalog filters.
func AllStepKinds() []StepKind {
	return []StepKind{StepShell, StepHTTP, StepAriaSave, StepVaultUse, StepPause}
}

// NewShellStep is a constructor for shell steps used by builtins / tests.
func NewShellStep(label, command, cwd string, timeoutSec int) Step {
	return Step{
		Kind:       StepShell,
		Label:      label,
		Command:    command,
		CWD:        cwd,
		TimeoutSec: timeoutSec,
	}
}

// NewHTTPStep builds an http step. body may be nil.
func NewHTTPStep(label, method, url string, expectedStatus int, body any) Step {
	step := Step{
		Kind:           StepHTTP,
		Label:          label,
		Method:         method,
		URL:            url,
		ExpectedStatus: expectedStatus,
	}
	if body != nil {
		raw, _ := json.Marshal(body)
		step.Body = raw
	}
	return step
}

// NewAriaSaveStep builds an aria_save step.
func NewAriaSaveStep(label, title, typ, scope, project, topicKey, contentTemplate string) Step {
	return Step{
		Kind:  StepAriaSave,
		Label: label,
		Save: &AriaSaveStep{
			Title:           title,
			Type:            typ,
			Scope:           scope,
			Project:         project,
			TopicKey:        topicKey,
			ContentTemplate: contentTemplate,
		},
	}
}

// NewVaultUseStep builds a vault_use step.
func NewVaultUseStep(label, command string, secretNames []string, timeoutSec int) Step {
	return Step{
		Kind:  StepVaultUse,
		Label: label,
		VaultUse: &VaultUseStep{
			Command:     command,
			SecretNames: secretNames,
			TimeoutSec:  timeoutSec,
		},
	}
}

// NewPauseStep builds a pause_for_human step.
func NewPauseStep(label, prompt string) Step {
	return Step{
		Kind:        StepPause,
		Label:       label,
		PausePrompt: prompt,
	}
}
