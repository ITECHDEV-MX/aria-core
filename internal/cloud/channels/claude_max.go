package channels

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// ClaudeMaxConfig configures the Claude Code subprocess channel.
type ClaudeMaxConfig struct {
	// BinaryPath is the path to the claude binary. Defaults to "/usr/bin/claude".
	BinaryPath string
	// DefaultModel is the model passed via --model when Request.Model is empty.
	// Defaults to "sonnet". "opus" is also accepted; anything else is passed
	// through verbatim to the binary.
	DefaultModel string
	// ExtraArgs adds CLI flags appended after the standard set. Mostly for tests.
	ExtraArgs []string
	// Runner is the subprocess hook. Tests stub it; production uses runRealClaude.
	Runner func(ctx context.Context, args []string, stdin string) (stdout, stderr string, exitCode int, err error)
}

// ClaudeMaxChannel spawns `claude --print` and reads the response from stdout.
// Stderr is inspected for rate-limit signals so the router can fall back to
// gemma-local.
type ClaudeMaxChannel struct {
	cfg ClaudeMaxConfig
}

// NewClaudeMaxChannel returns a channel wired to the local claude CLI.
// If cfg.BinaryPath is "", it defaults to /usr/bin/claude.
func NewClaudeMaxChannel(cfg ClaudeMaxConfig) *ClaudeMaxChannel {
	if strings.TrimSpace(cfg.BinaryPath) == "" {
		cfg.BinaryPath = "/usr/bin/claude"
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		cfg.DefaultModel = "sonnet"
	}
	if cfg.Runner == nil {
		cfg.Runner = runRealClaude
	}
	return &ClaudeMaxChannel{cfg: cfg}
}

// Name returns "claude-max-vps".
func (c *ClaudeMaxChannel) Name() string { return ChannelClaudeMax }

// Query renders the request as a markdown prompt, spawns claude, and parses
// stdout. Rate-limit signals in stderr trigger ErrChannelRateLimited so the
// router can fall back to gemma.
func (c *ClaudeMaxChannel) Query(ctx context.Context, req Request) (*Response, error) {
	if c == nil {
		return nil, ErrChannelUnavailable
	}
	prompt, lastUser := buildClaudePrompt(req)
	if strings.TrimSpace(prompt) == "" {
		return nil, errors.New("claude_max: empty prompt")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.cfg.DefaultModel
	}

	args := []string{
		"--print",
		"--bare",
		"--model", model,
	}
	if sys := strings.TrimSpace(req.SystemPrompt); sys != "" {
		args = append(args, "--append-system-prompt", sys)
	}
	args = append(args, c.cfg.ExtraArgs...)

	subCtx, cancel := applyTimeout(ctx, req)
	defer cancel()

	start := time.Now()
	stdout, stderr, code, err := c.cfg.Runner(subCtx, append([]string{c.cfg.BinaryPath}, args...), lastUser)
	elapsed := time.Since(start)

	// Treat context-deadline / cancel as transient.
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil, fmt.Errorf("%w: claude timeout: %w", ErrChannelUnavailable, err)
	}

	if isRateLimit(stderr, code, err) {
		return nil, fmt.Errorf("%w: %s", ErrChannelRateLimited, firstLine(stderr))
	}

	if err != nil {
		return nil, fmt.Errorf("%w: claude exec: %w: %s", ErrChannelUnavailable, err, firstLine(stderr))
	}
	if code != 0 {
		return nil, fmt.Errorf("%w: claude exit %d: %s", ErrChannelUnavailable, code, firstLine(stderr))
	}

	out := strings.TrimSpace(stdout)
	resp := &Response{
		Content:    out,
		Model:      model,
		Channel:    ChannelClaudeMax,
		DurationMs: int(elapsed / time.Millisecond),
		TokensIn:   estimateTokens(prompt),
		TokensOut:  estimateTokens(out),
	}
	return resp, nil
}

// buildClaudePrompt renders the prior history as markdown and returns the
// flattened prompt + the last user message (which is sent on stdin so the
// claude binary treats it as the live turn).
func buildClaudePrompt(req Request) (full, lastUser string) {
	var b strings.Builder
	for _, m := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch role {
		case "user":
			b.WriteString("## USER\n")
			b.WriteString(content)
			b.WriteString("\n\n")
			lastUser = content
		case "assistant":
			b.WriteString("## ASSISTANT\n")
			b.WriteString(content)
			b.WriteString("\n\n")
		case "system":
			// Skip — system prompt goes through --append-system-prompt.
		}
	}
	if lastUser == "" {
		// No user turn — fall back to the whole render.
		lastUser = strings.TrimSpace(b.String())
	}
	return b.String(), lastUser
}

// isRateLimit inspects stderr/exit code to decide if this was a quota error.
func isRateLimit(stderr string, exitCode int, _ error) bool {
	low := strings.ToLower(stderr)
	if strings.Contains(low, "rate limit") || strings.Contains(low, "rate_limit") ||
		strings.Contains(low, "quota exceeded") || strings.Contains(low, "too many requests") ||
		strings.Contains(low, "429") {
		return true
	}
	if exitCode == 429 {
		return true
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// runRealClaude is the production runner. argv[0] is the binary, argv[1:] the args.
func runRealClaude(ctx context.Context, argv []string, stdin string) (string, string, int, error) {
	if len(argv) == 0 {
		return "", "", -1, errors.New("claude_max: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	} else {
		cmd.Stdin = bytes.NewReader(nil)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	return stdout.String(), stderr.String(), exitCode, err
}

// readAll is a small helper kept here so future runners can plug io.Readers.
var _ = io.ReadAll
