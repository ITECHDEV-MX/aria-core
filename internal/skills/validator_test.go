package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validSkill = `---
name: example-skill
description: A skill that demonstrates the canonical SKILL.md format for ARIA Core. Long enough to pass the 10-char minimum and short enough to be one paragraph.
version: 1.0.0
owner: example@itechpymes.com.mx
license: Apache-2.0
expertise_domains: [example, demo]
last_reviewed: 2026-05-04
---

## When to Use

Use this skill when validating that the test fixtures actually represent a real, well-formed SKILL.md file.

## Rules

1. Always include a clear When to Use section.
2. Always include verification.
3. Keep the body over 200 chars.

## Verification

- [ ] When to Use is clear
- [ ] Rules numbered
- [ ] Body length adequate
`

func writeFixture(t *testing.T, content, dirName string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	return root
}

func TestValidateFile_HappyPath(t *testing.T) {
	root := writeFixture(t, validSkill, "example-skill")
	findings, err := ValidateFile(filepath.Join(root, "example-skill", "SKILL.md"), "example-skill")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, f := range findings {
		if f.Severity == SeverityError {
			t.Errorf("unexpected error finding: %+v", f)
		}
	}
}

func TestValidateFile_MissingRequiredFields(t *testing.T) {
	bad := `---
description: short
---

body
`
	root := writeFixture(t, bad, "example-skill")
	findings, err := ValidateFile(filepath.Join(root, "example-skill", "SKILL.md"), "example-skill")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	required := map[string]bool{"name": false, "version": false, "owner": false}
	for _, f := range findings {
		if _, ok := required[f.Field]; ok && f.Severity == SeverityError {
			required[f.Field] = true
		}
	}
	for field, found := range required {
		if !found {
			t.Errorf("expected error finding for missing %q", field)
		}
	}
}

func TestValidateFile_NameMustMatchDir(t *testing.T) {
	mismatched := strings.Replace(validSkill, "name: example-skill", "name: different-name", 1)
	root := writeFixture(t, mismatched, "example-skill")
	findings, _ := ValidateFile(filepath.Join(root, "example-skill", "SKILL.md"), "example-skill")
	hasMismatch := false
	for _, f := range findings {
		if f.Field == "name" && f.Severity == SeverityError && strings.Contains(f.Message, "must match") {
			hasMismatch = true
		}
	}
	if !hasMismatch {
		t.Error("expected name/dir mismatch error")
	}
}

func TestValidateFile_BadSemver(t *testing.T) {
	bad := strings.Replace(validSkill, "version: 1.0.0", "version: not-semver", 1)
	root := writeFixture(t, bad, "example-skill")
	findings, _ := ValidateFile(filepath.Join(root, "example-skill", "SKILL.md"), "example-skill")
	hasErr := false
	for _, f := range findings {
		if f.Field == "version" && f.Severity == SeverityError {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("expected version error")
	}
}

func TestValidateFile_BadEmail(t *testing.T) {
	bad := strings.Replace(validSkill, "owner: example@itechpymes.com.mx", "owner: not-an-email", 1)
	root := writeFixture(t, bad, "example-skill")
	findings, _ := ValidateFile(filepath.Join(root, "example-skill", "SKILL.md"), "example-skill")
	hasErr := false
	for _, f := range findings {
		if f.Field == "owner" && f.Severity == SeverityError {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("expected owner email error")
	}
}

func TestValidateFile_AgentRequiresPosition(t *testing.T) {
	skill := strings.Replace(validSkill,
		"last_reviewed: 2026-05-04",
		"last_reviewed: 2026-05-04\nagent: true",
		1)
	root := writeFixture(t, skill, "example-skill")
	findings, _ := ValidateFile(filepath.Join(root, "example-skill", "SKILL.md"), "example-skill")
	hasErr := false
	for _, f := range findings {
		if f.Field == "position_in_chain" && f.Severity == SeverityError {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("expected position_in_chain required when agent=true")
	}
}

func TestValidateFile_BodyTooShort(t *testing.T) {
	short := `---
name: example-skill
description: A skill description that is at least ten chars long.
version: 1.0.0
owner: a@b.com
---

x
`
	root := writeFixture(t, short, "example-skill")
	findings, _ := ValidateFile(filepath.Join(root, "example-skill", "SKILL.md"), "example-skill")
	hasWarn := false
	for _, f := range findings {
		if f.Field == "body" && f.Severity == SeverityWarning {
			hasWarn = true
		}
	}
	if !hasWarn {
		t.Error("expected body length warning")
	}
}

func TestValidateDir_AggregatesAllSkills(t *testing.T) {
	root := writeFixture(t, validSkill, "example-skill")
	// Add a second skill in same root
	dir := filepath.Join(root, "second-skill")
	_ = os.MkdirAll(dir, 0o755)
	second := strings.Replace(validSkill, "example-skill", "second-skill", -1)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(second), 0o644)

	res, err := ValidateDir(root)
	if err != nil {
		t.Fatalf("validate dir: %v", err)
	}
	if res.Skills != 2 {
		t.Errorf("expected 2 skills, got %d", res.Skills)
	}
	if res.HasErrors() {
		t.Errorf("unexpected errors: %s", res.Pretty())
	}
}

func TestSchemaIsEmbedded(t *testing.T) {
	if len(Schema()) < 100 {
		t.Errorf("embedded schema too small: %d bytes", len(Schema()))
	}
	if !strings.Contains(string(Schema()), "ARIA Skill v1") {
		t.Error("schema missing expected title")
	}
}

// ─── Agent-skill validation tests (F3) ───────────────────────────────────

const validAgentSkill = `---
name: example-agent
description: Example agent skill that runs as a sub-agent in a chain. Tests the multi-agent fields validate correctly.
version: 1.0.0
owner: example@itechpymes.com.mx
license: Apache-2.0
agent: true
position_in_chain: 1
inputs:
  required_artifacts: [0-prev.md]
outputs:
  artifact: 1-output.md
  aria_save_type: decision
  aria_save_scope: project
---

## When to Use

Use as a chained step in a story-creation pipeline.

## Rules

1. Read prior artifact.
2. Produce output.

## Verification

- [ ] Reads input
- [ ] Writes output
`

func TestValidateFile_AgentSkillHappy(t *testing.T) {
	root := writeFixture(t, validAgentSkill, "example-agent")
	findings, err := ValidateFile(filepath.Join(root, "example-agent", "SKILL.md"), "example-agent")
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, f := range findings {
		if f.Severity == SeverityError {
			t.Errorf("unexpected error: %+v", f)
		}
	}
}

func TestValidateFile_AgentArtifactPattern(t *testing.T) {
	bad := strings.Replace(validAgentSkill,
		"artifact: 1-output.md",
		"artifact: bad_filename.md",
		1)
	root := writeFixture(t, bad, "example-agent")
	findings, _ := ValidateFile(filepath.Join(root, "example-agent", "SKILL.md"), "example-agent")
	hasErr := false
	for _, f := range findings {
		if f.Field == "outputs.artifact" && f.Severity == SeverityError {
			hasErr = true
		}
	}
	if !hasErr {
		t.Error("expected artifact pattern error")
	}
}

func TestValidateFile_AgentZeroPositionAccepted(t *testing.T) {
	skill := strings.Replace(validAgentSkill,
		"position_in_chain: 1",
		"position_in_chain: 0",
		1)
	skill = strings.Replace(skill, "artifact: 1-output.md", "artifact: 0-first.md", 1)
	root := writeFixture(t, skill, "example-agent")
	findings, _ := ValidateFile(filepath.Join(root, "example-agent", "SKILL.md"), "example-agent")
	for _, f := range findings {
		if f.Severity == SeverityError {
			t.Errorf("position 0 should be valid, got: %+v", f)
		}
	}
}

// TestValidateExistingAgentSkills compiles each shipped agent-skill
// file and ensures it passes strict. This is the test that locks in
// the F4 deliverables.
func TestValidateExistingAgentSkills(t *testing.T) {
	repo := repoRoot(t)
	skillsDir := filepath.Join(repo, "skills")

	for _, name := range []string{"office-hours", "plan-ceo-review", "autoplan", "story-writer"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(skillsDir, name, "SKILL.md")
			if _, err := os.Stat(path); err != nil {
				t.Skipf("skill %q not present: %v", name, err)
			}
			findings, err := ValidateFile(path, name)
			if err != nil {
				t.Fatalf("validate %s: %v", name, err)
			}
			for _, f := range findings {
				if f.Severity == SeverityError {
					t.Errorf("agent skill %q has error: %+v", name, f)
				}
			}
		})
	}
}

// repoRoot walks up from the test's CWD until it finds go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for dir := cwd; dir != "/" && dir != "."; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}
	t.Fatal("could not find repo root (no go.mod walking up)")
	return ""
}
