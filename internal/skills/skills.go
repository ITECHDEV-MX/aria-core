// Package skills implements the validator and helpers for ARIA Core
// SKILL.md files. It enforces the canonical schema published at
// skills/schema/aria-skill-v1.schema.json.
//
// Two consumers:
//
//   - CLI (cmd/aria-core/skills.go): "aria-core skills validate [path]"
//     runs in soft mode (warnings non-fatal) by default; --strict is hard.
//   - Cloud server (future F1.b): rejects skills that fail the schema
//     before serving them via aria_get_skills.
package skills

import (
	_ "embed"
	"fmt"
	"strings"
)

//go:embed schema/aria-skill-v1.schema.json
var schemaBytes []byte

// Schema returns the embedded JSON Schema bytes. The schema is
// authoritative — local copies in other repos are advisory.
func Schema() []byte {
	return append([]byte(nil), schemaBytes...)
}

// Severity ranks how bad a finding is.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warn"
	SeverityInfo    Severity = "info"
)

// Finding is a single problem detected during validation.
type Finding struct {
	Path     string   `json:"path"`     // file path
	Field    string   `json:"field"`    // frontmatter field name (or "body")
	Severity Severity `json:"severity"`
	Message  string   `json:"message"`
}

// Result aggregates all findings across one or more skill files.
type Result struct {
	Skills    int       `json:"skills"`
	Findings  []Finding `json:"findings"`
	Errors    int       `json:"errors"`
	Warnings  int       `json:"warnings"`
}

// HasErrors reports whether the result contains any error-severity finding.
// Cloud (hard) mode and CLI --strict treat HasErrors as exit-1.
func (r Result) HasErrors() bool {
	return r.Errors > 0
}

// summarize counts severities into r.Errors / r.Warnings.
func (r *Result) summarize() {
	for _, f := range r.Findings {
		switch f.Severity {
		case SeverityError:
			r.Errors++
		case SeverityWarning:
			r.Warnings++
		}
	}
}

// Pretty returns a human-readable rendering of the result. Empty when
// no findings.
func (r Result) Pretty() string {
	if len(r.Findings) == 0 {
		return fmt.Sprintf("✓ %d skill(s) validated, no findings.", r.Skills)
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Validated %d skill(s) — %d error(s), %d warning(s)\n\n",
		r.Skills, r.Errors, r.Warnings))
	for _, f := range r.Findings {
		var icon string
		switch f.Severity {
		case SeverityError:
			icon = "✗"
		case SeverityWarning:
			icon = "⚠"
		default:
			icon = "ⓘ"
		}
		b.WriteString(fmt.Sprintf("  %s %s [%s]: %s\n", icon, f.Path, f.Field, f.Message))
	}
	return b.String()
}
