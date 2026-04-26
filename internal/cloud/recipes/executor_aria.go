package recipes

import (
	"context"
	"strings"
)

// execAriaSave persists the rendered observation via the AriaMemBridge.
// If no bridge is configured the step is marked failed (we don't want to
// silently swallow data the recipe was supposed to record).
func execAriaSave(ctx context.Context, a AriaMemBridge, sr *StepResult, step Step, tCtx TemplateContext, byUID string) {
	if step.Save == nil {
		sr.Status = StatusFailed
		sr.Stderr = "aria_save step: save params are required"
		return
	}
	if a == nil {
		sr.Status = StatusFailed
		sr.Stderr = "aria_save step: no aria-memory bridge configured"
		return
	}
	title := RenderString(step.Save.Title, tCtx)
	content := RenderString(step.Save.ContentTemplate, tCtx)
	if strings.TrimSpace(title) == "" {
		sr.Status = StatusFailed
		sr.Stderr = "aria_save step: title is required"
		return
	}
	in := AriaSaveBridgeInput{
		Title:        title,
		Type:         RenderString(step.Save.Type, tCtx),
		Scope:        RenderString(step.Save.Scope, tCtx),
		Project:      RenderString(step.Save.Project, tCtx),
		TopicKey:     RenderString(step.Save.TopicKey, tCtx),
		Content:      content,
		DeveloperUID: byUID,
		Source:       "recipe-runner",
	}
	if err := a.Save(ctx, in); err != nil {
		sr.Status = StatusFailed
		sr.Stderr = err.Error()
		return
	}
	sr.Status = StatusSuccess
	sr.Stdout = "saved observation: " + title
}
