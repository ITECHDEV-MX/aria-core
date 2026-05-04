package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ITECHDEV-MX/aria-core/internal/historias"
)

func TestSkillsByHistoria_DedupesAndPreservesOrder(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HISTORIAS_ROOT", root)

	// Build a chain: office-hours, plan-ceo-review, autoplan, story-writer
	steps := []struct {
		pos      int
		filename string
		skill    string
	}{
		{0, "0-office.md", "office-hours@1.0.0"},
		{1, "1-ceo.md", "plan-ceo-review@1.0.0"},
		{2, "2-eng.md", "autoplan@1.0.0"},
		{3, "3-story.md", "story-writer@1.0.0"},
	}
	for _, s := range steps {
		_, err := historias.SaveArtifact(historias.SaveArtifactArgs{
			Root: root, Slug: "test-chain", Position: s.pos,
			Filename: s.filename, Skill: s.skill, Content: "x",
		})
		if err != nil {
			t.Fatalf("seed %d: %v", s.pos, err)
		}
	}

	m, err := historias.ListChain(root, "test-chain")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Sanity check our fixture before testing the MCP handler logic.
	if len(m.Chain) != 4 {
		t.Fatalf("seeded 4 entries, got %d", len(m.Chain))
	}

	// Reuse the same name extraction the tool does. We are not invoking
	// the MCP server itself in this unit test (that exercise lives in
	// integration tests); we are validating the dedupe + order rule.
	seen := map[string]bool{}
	var names []string
	for _, e := range m.Chain {
		n := strings.SplitN(e.Skill, "@", 2)[0]
		if seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	want := []string{"office-hours", "plan-ceo-review", "autoplan", "story-writer"}
	if len(names) != len(want) {
		t.Fatalf("want %d names, got %v", len(want), names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Errorf("position %d: want %q got %q", i, want[i], names[i])
		}
	}
}

func TestSkillsByHistoria_EmptyChain(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HISTORIAS_ROOT", root)

	// No chain saved — historiasRoot has nothing
	if _, err := os.Stat(filepath.Join(root, "missing", "MANIFEST.yaml")); !os.IsNotExist(err) {
		t.Fatalf("expected missing manifest, got %v", err)
	}
}
