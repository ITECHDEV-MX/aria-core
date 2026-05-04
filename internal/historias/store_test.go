package historias

import (
	"path/filepath"
	"testing"
)

func TestSaveArtifact_HappyPath(t *testing.T) {
	root := t.TempDir()
	entry, err := SaveArtifact(SaveArtifactArgs{
		Root: root, Slug: "test-historia", Position: 0,
		Filename: "0-office-hours.md",
		Skill: "office-hours@1.0.0", AgentModel: "claude-opus-4-7",
		Content: "# Office Hours\n\nReframe of request.\n",
		CreatedBy: "tester@test.com", DurationMs: 1000,
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if entry.Position != 0 || entry.ContentSHA256 == "" {
		t.Errorf("bad entry: %+v", entry)
	}
}

func TestSaveArtifact_ChainOrder(t *testing.T) {
	root := t.TempDir()
	for i, fn := range []string{"0-a.md", "1-b.md", "2-c.md"} {
		_, err := SaveArtifact(SaveArtifactArgs{
			Root: root, Slug: "h", Position: i, Filename: fn,
			Skill: "x@1.0.0", AgentModel: "m", Content: "x",
			CreatedBy: "t@t.com",
		})
		if err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}

	m, err := ListChain(root, "h")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(m.Chain) != 3 {
		t.Errorf("chain len: want 3 got %d", len(m.Chain))
	}
	for i, e := range m.Chain {
		if e.Position != i {
			t.Errorf("position %d: got %d", i, e.Position)
		}
	}
}

func TestSaveArtifact_RejectsInvalidSlug(t *testing.T) {
	_, err := SaveArtifact(SaveArtifactArgs{
		Root: t.TempDir(), Slug: "Bad Slug", Position: 0,
		Filename: "0-x.md", Skill: "s", Content: "c",
	})
	if err != ErrSlugInvalid {
		t.Errorf("want ErrSlugInvalid, got %v", err)
	}
}

func TestSaveArtifact_RejectsBadFilename(t *testing.T) {
	_, err := SaveArtifact(SaveArtifactArgs{
		Root: t.TempDir(), Slug: "h", Position: 0,
		Filename: "no-position-prefix.md", Skill: "s", Content: "c",
	})
	if err == nil {
		t.Error("want filename error")
	}
}

func TestSaveArtifact_PositionMismatch(t *testing.T) {
	_, err := SaveArtifact(SaveArtifactArgs{
		Root: t.TempDir(), Slug: "h", Position: 0,
		Filename: "5-x.md", Skill: "s", Content: "c",
	})
	if err == nil {
		t.Error("want position mismatch error")
	}
}

func TestSaveArtifact_FirstWriterWins(t *testing.T) {
	root := t.TempDir()
	args := SaveArtifactArgs{
		Root: root, Slug: "h", Position: 0,
		Filename: "0-a.md", Skill: "s@1", Content: "first",
	}
	if _, err := SaveArtifact(args); err != nil {
		t.Fatalf("first save: %v", err)
	}
	args.Content = "second"
	if _, err := SaveArtifact(args); err != ErrPositionTaken {
		t.Errorf("want ErrPositionTaken, got %v", err)
	}
}

func TestSaveArtifact_OverwriteAllowed(t *testing.T) {
	root := t.TempDir()
	args := SaveArtifactArgs{
		Root: root, Slug: "h", Position: 0,
		Filename: "0-a.md", Skill: "s@1", Content: "first",
	}
	_, _ = SaveArtifact(args)
	args.Content = "second"
	args.Overwrite = true
	if _, err := SaveArtifact(args); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	_, body, err := GetArtifact(root, "h", 0)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if body != "second" {
		t.Errorf("want overwrite to win, got %q", body)
	}
}

func TestSaveArtifact_InputMustExist(t *testing.T) {
	root := t.TempDir()
	_, err := SaveArtifact(SaveArtifactArgs{
		Root: root, Slug: "h", Position: 1,
		Filename: "1-b.md", Skill: "s", Content: "c",
		Inputs: []string{"0-a.md"}, // doesn't exist
	})
	if err == nil {
		t.Error("want missing input error")
	}
}

func TestListSlugs(t *testing.T) {
	root := t.TempDir()
	for _, slug := range []string{"alpha", "beta"} {
		_, err := SaveArtifact(SaveArtifactArgs{
			Root: root, Slug: slug, Position: 0,
			Filename: "0-a.md", Skill: "s", Content: "c",
		})
		if err != nil {
			t.Fatalf("seed %s: %v", slug, err)
		}
	}
	slugs, err := ListSlugs(root)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(slugs) != 2 {
		t.Errorf("want 2 slugs, got %v", slugs)
	}
}

func TestCompleteChain(t *testing.T) {
	root := t.TempDir()
	_, _ = SaveArtifact(SaveArtifactArgs{
		Root: root, Slug: "h", Position: 0,
		Filename: "0-final.md", Skill: "s", Content: "c",
	})
	if err := CompleteChain(root, "h", "0-final.md"); err != nil {
		t.Fatalf("complete: %v", err)
	}
	m, _ := ListChain(root, "h")
	if m.Status != StatusCompleted || m.FinalArtifact != "0-final.md" {
		t.Errorf("complete didn't apply: %+v", m)
	}
}

func TestGetArtifact_Missing(t *testing.T) {
	root := t.TempDir()
	_, _ = SaveArtifact(SaveArtifactArgs{
		Root: root, Slug: "h", Position: 0,
		Filename: "0-a.md", Skill: "s", Content: "c",
	})
	_, _, err := GetArtifact(root, "h", 99)
	if err != ErrArtifactMissing {
		t.Errorf("want ErrArtifactMissing, got %v", err)
	}
}

func TestSaveArtifact_ManifestPersisted(t *testing.T) {
	root := t.TempDir()
	_, err := SaveArtifact(SaveArtifactArgs{
		Root: root, Slug: "h", Position: 0,
		Filename: "0-a.md", Skill: "s", Content: "c",
	})
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := readManifest(filepath.Join(root, "h", "MANIFEST.yaml")); err != nil {
		t.Errorf("manifest not persisted: %v", err)
	}
}
