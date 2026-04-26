package recipes

import "context"

// execPause delegates to PauseHandler. The default no-op handler returns
// proceed=true so dashboard-driven runs do not stall on a pause step that
// nobody is watching; CLI runs override the handler with a TTY prompt.
func execPause(ctx context.Context, h PauseHandler, sr *StepResult, step Step, tCtx TemplateContext) {
	prompt := RenderString(step.PausePrompt, tCtx)
	if prompt == "" {
		prompt = step.Label
	}
	sr.Stdout = prompt
	if h == nil {
		sr.Status = StatusSuccess
		return
	}
	proceed, note, err := h.HandlePause(ctx, prompt)
	if err != nil {
		sr.Status = StatusFailed
		sr.Stderr = err.Error()
		return
	}
	if note != "" {
		sr.Stdout = sr.Stdout + "\n[note] " + note
	}
	if !proceed {
		sr.Status = StatusFailed
		sr.Stderr = "pause: aborted by operator"
		return
	}
	sr.Status = StatusSuccess
}

// defaultPauseHandler proceeds without prompting — used when no override is supplied.
type defaultPauseHandler struct{}

func (defaultPauseHandler) HandlePause(ctx context.Context, prompt string) (bool, string, error) {
	return true, "", nil
}
