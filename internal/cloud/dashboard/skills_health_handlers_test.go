package dashboard

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a-h/templ"
)

func writeSkillFixture(t *testing.T, root, name, body string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

const minimalSkill = `---
name: example
description: Example skill at least 10 chars long.
version: 1.0.0
owner: a@b.com
last_reviewed: 2026-05-04
---

## When to Use

body

## Rules

1. x

## Verification

- [ ] x
`

const minimalAgentSkill = `---
name: example
description: Example agent skill at least 10 chars.
version: 1.0.0
owner: a@b.com
last_reviewed: 2026-05-04
agent: true
position_in_chain: 0
---

## When to Use

body

## Rules

1. x

## Verification

- [ ] x
`

func TestBuildSkillsHealth_Counts(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "alpha",
		strings.Replace(minimalSkill, "name: example", "name: alpha", 1))
	writeSkillFixture(t, root, "beta",
		strings.Replace(minimalAgentSkill, "name: example", "name: beta", 1))

	rows, totals, err := buildSkillsHealth(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if totals.Total != 2 {
		t.Errorf("total: want 2 got %d (rows=%+v)", totals.Total, rows)
	}
	if totals.Agent != 1 {
		t.Errorf("agent: want 1 got %d", totals.Agent)
	}
}

func TestBuildSkillsHealth_DraftsCounted(t *testing.T) {
	root := t.TempDir()
	writeSkillFixture(t, root, "alpha",
		strings.Replace(minimalSkill, "name: example", "name: alpha", 1))
	// Drafts live under root/_drafts/
	writeSkillFixture(t, filepath.Join(root, "_drafts"), "in-review",
		strings.Replace(minimalSkill, "name: example", "name: in-review", 1))

	_, totals, err := buildSkillsHealth(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if totals.Drafts != 1 {
		t.Errorf("drafts: want 1 got %d", totals.Drafts)
	}
	if totals.Total != 2 {
		t.Errorf("total (including drafts): want 2 got %d", totals.Total)
	}
}

func TestBuildSkillsHealth_UnreviewedDetection(t *testing.T) {
	root := t.TempDir()

	stale := strings.Replace(minimalSkill,
		"last_reviewed: 2026-05-04", "last_reviewed: 2024-01-01", 1)
	writeSkillFixture(t, root, "stale",
		strings.Replace(stale, "name: example", "name: stale", 1))

	missing := strings.Replace(minimalSkill,
		"last_reviewed: 2026-05-04\n", "", 1)
	writeSkillFixture(t, root, "no-date",
		strings.Replace(missing, "name: example", "name: no-date", 1))

	_, totals, err := buildSkillsHealth(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if totals.UnreviewedRecent != 2 {
		t.Errorf("unreviewed: want 2 got %d", totals.UnreviewedRecent)
	}
}

func TestHandleSkillsHealth_Renders(t *testing.T) {
	withStubLayoutForSkills(t)
	root := t.TempDir()
	writeSkillFixture(t, root, "alpha",
		strings.Replace(minimalSkill, "name: example", "name: alpha", 1))

	h := &handlers{cfg: MountConfig{SkillsRoot: root}}
	req := httptest.NewRequest(http.MethodGet, "/dashboard/skills/health", nil)
	req = req.WithContext(context.Background())
	w := httptest.NewRecorder()
	h.handleSkillsHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"alpha", "1.0.0", "a@b.com"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in body", want)
		}
	}
}

// withStubLayoutForSkills swaps frameLayoutFn for a plain renderer.
// Restored after the test.
func withStubLayoutForSkills(t *testing.T) {
	t.Helper()
	prev := frameLayoutFn
	frameLayoutFn = func(w http.ResponseWriter, r *http.Request, title string, comp templ.Component) error {
		return comp.Render(r.Context(), w)
	}
	t.Cleanup(func() { frameLayoutFn = prev })
}
