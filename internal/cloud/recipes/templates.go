package recipes

import (
	"bytes"
	"fmt"
	"strings"
	"text/template"
)

// TemplateContext is the rendering context exposed to step templates.
// .Vars carries the user-supplied vars passed to Execute(); .Prev is the
// previous step's outcome (stdout/stderr/exit_code/status/label).
type TemplateContext struct {
	Vars map[string]string
	Prev *StepResult
	// AllSteps allows referring to any prior step's outcome by index.
	AllSteps []StepResult
}

// RenderString runs Go-template substitution against a single string.
// Empty input passes through unchanged. Errors fall back to the original
// string with a brace-stripped warning so the runner does not crash on a
// malformed template — the failure surfaces in the resulting command output.
func RenderString(in string, ctx TemplateContext) string {
	if !strings.Contains(in, "{{") {
		return in
	}
	tpl, err := template.New("step").Option("missingkey=zero").Parse(in)
	if err != nil {
		return in
	}
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, templateView(ctx)); err != nil {
		return in
	}
	return buf.String()
}

// RenderMap applies RenderString to every value in a map[string]string.
func RenderMap(in map[string]string, ctx TemplateContext) map[string]string {
	if len(in) == 0 {
		return in
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = RenderString(v, ctx)
	}
	return out
}

// templateView is the data shape that templates see. Exposes Vars + Prev as
// stable, well-named fields so users can write {{ .Vars.branch }} or
// {{ .Prev.Stdout }} without surprises.
func templateView(ctx TemplateContext) map[string]any {
	view := map[string]any{
		"Vars": ctx.Vars,
	}
	if ctx.Prev != nil {
		view["Prev"] = map[string]any{
			"Index":    ctx.Prev.Index,
			"Kind":     ctx.Prev.Kind,
			"Label":    ctx.Prev.Label,
			"Status":   ctx.Prev.Status,
			"ExitCode": ctx.Prev.ExitCode,
			"Stdout":   ctx.Prev.Stdout,
			"Stderr":   ctx.Prev.Stderr,
		}
	} else {
		view["Prev"] = map[string]any{}
	}
	steps := make([]map[string]any, 0, len(ctx.AllSteps))
	for _, s := range ctx.AllSteps {
		steps = append(steps, map[string]any{
			"Index":    s.Index,
			"Kind":     s.Kind,
			"Label":    s.Label,
			"Status":   s.Status,
			"ExitCode": s.ExitCode,
			"Stdout":   s.Stdout,
			"Stderr":   s.Stderr,
		})
	}
	view["Steps"] = steps
	return view
}

// MaskValues replaces every secret value in s with <<MASKED:NAME>>. Values
// shorter than 6 chars are skipped to avoid clobbering legitimate substrings
// (matches the heuristic used in aria_vault_mcp.go::maskValue).
func MaskValues(s string, secrets map[string]string) string {
	if len(secrets) == 0 || s == "" {
		return s
	}
	out := s
	for name, value := range secrets {
		if len(value) < 6 {
			continue
		}
		out = strings.ReplaceAll(out, value, fmt.Sprintf("<<MASKED:%s>>", name))
	}
	return out
}
