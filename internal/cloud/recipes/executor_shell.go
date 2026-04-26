package recipes

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// execShell runs a shell step via the configured ShellExecutor.
func execShell(ctx context.Context, sh ShellExecutor, sr *StepResult, step Step, tCtx TemplateContext) {
	cmd := RenderString(step.Command, tCtx)
	cwd := RenderString(step.CWD, tCtx)
	if strings.TrimSpace(cmd) == "" {
		sr.Status = StatusFailed
		sr.Stderr = "shell step: command is required"
		return
	}
	timeout := time.Duration(step.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	envOverlay := []string{}
	for k, v := range RenderMap(step.Vars, tCtx) {
		envOverlay = append(envOverlay, k+"="+v)
	}
	stdout, stderr, exitCode, err := sh.Run(ctx, cmd, cwd, envOverlay, nil, timeout)
	sr.Stdout = stdout
	sr.Stderr = stderr
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

// defaultShellExecutor uses /bin/sh -c (mirrors aria_vault_mcp.go::pickShell).
type defaultShellExecutor struct{}

// Run executes the command, captures stdout/stderr, applies timeout via ctx,
// and masks any provided secret values in the captured streams before returning.
func (defaultShellExecutor) Run(ctx context.Context, command, cwd string, env []string, secrets map[string]string, timeout time.Duration) (string, string, int, error) {
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(execCtx, "/bin/sh", "-c", command)
	if strings.TrimSpace(cwd) != "" {
		cmd.Dir = cwd
	}
	cmd.Env = append(os.Environ(), env...)

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stdout := MaskValues(stdoutBuf.String(), secrets)
	stderr := MaskValues(stderrBuf.String(), secrets)

	if execCtx.Err() == context.DeadlineExceeded {
		if stderr == "" {
			stderr = fmt.Sprintf("shell timeout after %s", timeout)
		}
		return stdout, stderr, -1, execCtx.Err()
	}
	if err == nil {
		return stdout, stderr, 0, nil
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout, stderr, exitErr.ExitCode(), nil
	}
	if stderr == "" {
		stderr = err.Error()
	}
	return stdout, stderr, -1, err
}
