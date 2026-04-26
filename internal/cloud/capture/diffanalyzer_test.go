package capture

import "testing"

func TestSuggest_ConventionalPrefixes(t *testing.T) {
	cases := []struct {
		name     string
		msg      string
		diff     string
		wantType string
		wantSkip bool
	}{
		{"feat maps to decision", "feat(auth): add SSO", "diff --git a/x b/x\n+real change", TypeDecision, false},
		{"fix maps to learning", "fix: NPE on login", "diff --git a/x b/x\n+real change", TypeLearning, false},
		{"refactor maps to tech_debt", "refactor: split server", "diff --git a/x b/x\n+real change", TypeTechDebt, false},
		{"chore is skipped", "chore: bump deps", "diff --git a/x b/x\n+x", TypeCommit, true},
		{"style is skipped", "style: gofmt", "", TypeCommit, true},
		{"deps is skipped", "deps: upgrade pgx", "", TypeCommit, true},
		{"feat with breaking marker", "feat!: new API", "diff --git a/x b/x\n+content", TypeDecision, false},
		{"unknown prefix falls back", "wip: stuff", "diff --git a/x b/x\n+more", TypeCommit, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Suggest(tc.msg, tc.diff)
			if got.Type != tc.wantType {
				t.Fatalf("type: want %q got %q (%+v)", tc.wantType, got.Type, got)
			}
			if got.Skip != tc.wantSkip {
				t.Fatalf("skip: want %v got %v (%+v)", tc.wantSkip, got.Skip, got)
			}
		})
	}
}

func TestSuggest_TitleCleansPrefix(t *testing.T) {
	got := Suggest("feat(auth): add SSO", "diff --git a/x b/x\n+content")
	if got.Title != "add SSO" {
		t.Fatalf("title: want %q got %q", "add SSO", got.Title)
	}
}

func TestSuggest_WhitespaceOnlySkipped(t *testing.T) {
	diff := "diff --git a/x b/x\n@@\n-   \n+   \n"
	got := Suggest("misc tweak", diff)
	if !got.Skip {
		t.Fatalf("expected whitespace-only diff to skip, got %+v", got)
	}
}

func TestSuggest_LargeDiffNotSkippedEvenIfWhitespaceyHeader(t *testing.T) {
	// >10 lines diff: must not be classified as whitespace-only.
	big := "diff --git a/x b/x\n"
	for i := 0; i < 12; i++ {
		big += "+significant line " + string(rune('a'+i)) + "\n"
	}
	got := Suggest("feat: big change", big)
	if got.Skip {
		t.Fatalf("large diff should not skip: %+v", got)
	}
	if got.Type != TypeDecision {
		t.Fatalf("want decision got %s", got.Type)
	}
}

func TestSplitConventionalPrefix(t *testing.T) {
	cases := []struct {
		title      string
		wantPrefix string
		wantRest   string
	}{
		{"feat: x", "feat", "x"},
		{"feat(auth): x", "feat", "x"},
		{"feat!: x", "feat", "x"},
		{"feat(auth)!: x", "feat", "x"},
		{"plain title", "", "plain title"},
		{"two words: rest", "", "two words: rest"}, // space disqualifies
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			gotP, gotR := splitConventionalPrefix(tc.title)
			if gotP != tc.wantPrefix || gotR != tc.wantRest {
				t.Fatalf("split(%q): want (%q,%q) got (%q,%q)",
					tc.title, tc.wantPrefix, tc.wantRest, gotP, gotR)
			}
		})
	}
}

func TestIsWhitespaceOnlyDiff(t *testing.T) {
	if !isWhitespaceOnlyDiff("") {
		t.Fatal("empty diff must be whitespace-only")
	}
	if !isWhitespaceOnlyDiff("diff --git a/x b/x\n+   \n-   \n") {
		t.Fatal("space-only changes must be whitespace-only")
	}
	if isWhitespaceOnlyDiff("diff --git a/x b/x\n+real content\n") {
		t.Fatal("real content must not be whitespace-only")
	}
}
