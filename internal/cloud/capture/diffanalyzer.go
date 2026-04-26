// Package capture implements pasivo auto-capture for ARIA observations:
// git hooks, CI integrations and Claude Code session watchers.
//
// The DiffAnalyzer in this file converts a raw commit message + git diff
// (or stat) into a normalized Suggestion that other call-sites can use to
// decide whether to silently skip the event or persist it as an
// aria_observations row.
package capture

import (
	"strings"
)

// Observation type constants (mirror the canonical aria_observations.type values).
const (
	TypeGeneral  = "general"
	TypeDecision = "decision"
	TypeLearning = "learning"
	TypeTechDebt = "tech_debt"
	TypeCommit   = "commit"
	TypeRisk     = "risk"
)

// Scope constants.
const (
	ScopePersonal = "personal"
	ScopeProject  = "project"
	ScopeTeam     = "team"
)

// Suggestion is the structured output of DiffAnalyzer.Suggest.
type Suggestion struct {
	Type       string  `json:"type"`
	Title      string  `json:"title"`
	Scope      string  `json:"scope"`
	Skip       bool    `json:"skip"`
	Confidence float64 `json:"confidence"`
	// Reason is a short human-readable label explaining the choice
	// (used for debug logging, not for storage).
	Reason string `json:"reason,omitempty"`
}

// trivialPrefixes are conventional commit prefixes that should be skipped
// silently — bumping deps or formatting whitespace is not worth a memory.
var trivialPrefixes = map[string]string{
	"chore":  "chore",
	"style":  "style",
	"deps":   "deps",
	"build":  "build",
	"ci":     "ci",
	"format": "format",
}

// substantivePrefixes maps the conventional commit prefix → canonical
// observation type.
var substantivePrefixes = map[string]string{
	"feat":     TypeDecision,
	"feature":  TypeDecision,
	"fix":      TypeLearning,
	"bugfix":   TypeLearning,
	"hotfix":   TypeLearning,
	"refactor": TypeTechDebt,
	"perf":     TypeTechDebt,
	"docs":     TypeGeneral,
	"test":     TypeGeneral,
}

// Suggest produces a Suggestion for the given commit message and diff text.
// The diff input may be either a raw `git diff` payload or a `git diff --stat`
// summary; both are supported because heuristics work on aggregate signals.
//
// Heuristic order:
//  1. Trivial conventional commit prefix (chore/style/deps/...) → Skip=true.
//  2. Whitespace-only diff with <10 lines → Skip=true.
//  3. Substantive prefix (feat/fix/refactor/...) → mapped Type.
//  4. Fallback → Type=commit, low confidence.
func Suggest(commitMsg, diff string) Suggestion {
	title := firstLine(commitMsg)
	prefix, rest := splitConventionalPrefix(title)
	cleanTitle := strings.TrimSpace(rest)
	if cleanTitle == "" {
		cleanTitle = strings.TrimSpace(title)
	}

	// Default scope is personal (devs own their commits unless promoted).
	scope := ScopePersonal

	// Trivial prefix → silent skip.
	if prefix != "" {
		if _, trivial := trivialPrefixes[prefix]; trivial {
			return Suggestion{
				Type:       TypeCommit,
				Title:      cleanTitle,
				Scope:      scope,
				Skip:       true,
				Confidence: 0.95,
				Reason:     "trivial-prefix:" + prefix,
			}
		}
	}

	// Whitespace-only diff with very small footprint → skip.
	if isWhitespaceOnlyDiff(diff) {
		return Suggestion{
			Type:       TypeCommit,
			Title:      cleanTitle,
			Scope:      scope,
			Skip:       true,
			Confidence: 0.85,
			Reason:     "whitespace-only",
		}
	}

	// Substantive prefix mapping.
	if prefix != "" {
		if mapped, ok := substantivePrefixes[prefix]; ok {
			return Suggestion{
				Type:       mapped,
				Title:      cleanTitle,
				Scope:      scope,
				Skip:       false,
				Confidence: 0.9,
				Reason:     "prefix:" + prefix,
			}
		}
	}

	// Fallback. Don't skip — let the caller decide; default low confidence.
	return Suggestion{
		Type:       TypeCommit,
		Title:      cleanTitle,
		Scope:      scope,
		Skip:       false,
		Confidence: 0.4,
		Reason:     "fallback",
	}
}

// firstLine returns the first non-empty line of s, trimmed.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return ""
}

// splitConventionalPrefix extracts the lowercase conventional-commit prefix
// from titles like "feat(auth): add login" or "fix: NPE". Returns
// (prefix, rest) where rest is the title without the prefix and colon.
// Returns empty prefix if the title does not match the convention.
func splitConventionalPrefix(title string) (string, string) {
	colon := strings.Index(title, ":")
	if colon <= 0 {
		return "", title
	}
	head := strings.TrimSpace(title[:colon])
	rest := strings.TrimSpace(title[colon+1:])
	// Strip optional scope: feat(auth) → feat
	if paren := strings.Index(head, "("); paren > 0 {
		head = strings.TrimSpace(head[:paren])
	}
	// Strip ! marker for breaking changes (feat!:)
	head = strings.TrimSuffix(head, "!")
	head = strings.ToLower(head)
	if head == "" {
		return "", title
	}
	// Sanity: prefix must be a single token (no spaces).
	if strings.ContainsAny(head, " \t") {
		return "", title
	}
	return head, rest
}

// isWhitespaceOnlyDiff returns true if the diff content (excluding the @@/+++/---
// hunks) is empty or whitespace-only AND the total line count is small (<10).
// It accepts either raw unified diffs or `git diff --stat` summaries.
func isWhitespaceOnlyDiff(diff string) bool {
	if strings.TrimSpace(diff) == "" {
		return true
	}
	lines := strings.Split(diff, "\n")
	if len(lines) >= 10 {
		// Too big to assume trivial — treat as substantive.
		return false
	}
	for _, line := range lines {
		// Skip diff metadata lines.
		if strings.HasPrefix(line, "diff ") ||
			strings.HasPrefix(line, "index ") ||
			strings.HasPrefix(line, "---") ||
			strings.HasPrefix(line, "+++") ||
			strings.HasPrefix(line, "@@") {
			continue
		}
		// Stat line: " path | 2 +-" — these are metadata too.
		if strings.Contains(line, "|") && strings.ContainsAny(line, "+-") {
			continue
		}
		// Anything that adds/removes non-whitespace content counts.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// A change line starts with + or - in unified diff.
		if strings.HasPrefix(trimmed, "+") || strings.HasPrefix(trimmed, "-") {
			body := strings.TrimSpace(trimmed[1:])
			if body != "" {
				return false
			}
			continue
		}
		// Non-diff non-empty content (e.g. summary lines) → not whitespace-only.
		return false
	}
	return true
}
