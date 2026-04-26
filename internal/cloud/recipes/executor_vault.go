package recipes

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// execVaultUse runs a command with vault secrets injected as env vars.
// The runner masks every secret value in stdout/stderr before persisting,
// matching the contract documented in aria_vault_mcp.go.
func execVaultUse(ctx context.Context, sh ShellExecutor, vault VaultBridge, sr *StepResult, step Step, tCtx TemplateContext, byUID string) {
	if step.VaultUse == nil {
		sr.Status = StatusFailed
		sr.Stderr = "vault_use step: vault_use params are required"
		return
	}
	if vault == nil {
		sr.Status = StatusFailed
		sr.Stderr = "vault_use step: no vault bridge configured"
		return
	}
	cmd := RenderString(step.VaultUse.Command, tCtx)
	if strings.TrimSpace(cmd) == "" {
		sr.Status = StatusFailed
		sr.Stderr = "vault_use step: command is required"
		return
	}
	names := append([]string(nil), step.VaultUse.SecretNames...)
	if len(names) == 0 {
		sr.Status = StatusFailed
		sr.Stderr = "vault_use step: secret_names is required"
		return
	}
	reason := fmt.Sprintf("recipe_step:%d:%s", sr.Index, sr.Label)
	secrets, err := vault.ResolveAndReveal(ctx, names, byUID, reason)
	if err != nil {
		sr.Status = StatusFailed
		sr.Stderr = err.Error()
		return
	}
	envOverlay := make([]string, 0, len(secrets))
	for k, v := range secrets {
		envOverlay = append(envOverlay, k+"="+v)
	}
	timeout := time.Duration(step.VaultUse.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	stdout, stderr, exitCode, err := sh.Run(ctx, cmd, RenderString(step.VaultUse.CWD, tCtx), envOverlay, secrets, timeout)
	// Defense-in-depth: mask again at the runner level in case the executor
	// did not honor the contract.
	sr.Stdout = MaskValues(stdout, secrets)
	sr.Stderr = MaskValues(stderr, secrets)
	sr.ExitCode = exitCode
	if err != nil {
		sr.Status = StatusFailed
		if sr.Stderr == "" {
			sr.Stderr = err.Error()
		}
		return
	}
	if exitCode != 0 {
		sr.Status = StatusFailed
		return
	}
	sr.Status = StatusSuccess
}
