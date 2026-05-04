package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildVersionsTable_Empty(t *testing.T) {
	root := t.TempDir()
	body, err := BuildVersionsTable(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, want := range []string{"Skills VERSIONS", "Total: **0**", "Auto-generated"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
}

func TestBuildVersionsTable_PopulatedAndSorted(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zeta", "alpha", "mu"} {
		dir := filepath.Join(root, name)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, "SKILL.md"),
			[]byte("---\nname: "+name+"\ndescription: A skill at least 10 chars.\nversion: 1.0.0\nowner: a@b.com\n---\n\n## When to Use\n\nbody\n\n## Verification\n\n- [ ] x\n"),
			0o644)
	}
	body, err := BuildVersionsTable(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// Alphabetical: alpha first, mu second, zeta last.
	a := strings.Index(body, "alpha")
	m := strings.Index(body, "mu")
	z := strings.Index(body, "zeta")
	if a < 0 || m < 0 || z < 0 {
		t.Fatal("rows missing")
	}
	if !(a < m && m < z) {
		t.Errorf("not sorted alphabetically: a=%d m=%d z=%d", a, m, z)
	}
}

func TestWriteVersionsFile_Atomic(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "alpha")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: alpha\ndescription: 10 chars min.\nversion: 1.0.0\nowner: a@b.com\n---\n\nbody\n"),
		0o644)

	if err := WriteVersionsFile(root); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, VersionsFile))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(data), "alpha") {
		t.Error("VERSIONS.md missing alpha row")
	}
}

func TestBuildVersionsTable_AgentFlag(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "agent-skill")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: agent-skill\ndescription: Agent at least 10 chars.\nversion: 1.0.0\nowner: a@b.com\nagent: true\nposition_in_chain: 0\n---\n\nbody\n"),
		0o644)
	body, err := BuildVersionsTable(root)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !strings.Contains(body, "✓") {
		t.Errorf("expected agent ✓ marker in:\n%s", body)
	}
}
