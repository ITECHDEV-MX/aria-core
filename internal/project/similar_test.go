package project

import "testing"

// ─── levenshtein unit tests ──────────────────────────────────────────────────

func TestLevenshtein(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{a: "", b: "", want: 0},
		{a: "abc", b: "", want: 3},
		{a: "", b: "abc", want: 3},
		{a: "abc", b: "abc", want: 0},
		{a: "kitten", b: "sitting", want: 3},
		{a: "saturday", b: "sunday", want: 3},
		{a: "aria-core", b: "ariacore", want: 1},  // single deletion (hyphen)
		{a: "aria-core", b: "aria-core", want: 0}, // identical
		{a: "a", b: "b", want: 1},
		{a: "abc", b: "ac", want: 1},   // one deletion
		{a: "abc", b: "axc", want: 1},  // one substitution
		{a: "abc", b: "abcd", want: 1}, // one insertion
	}

	for _, tc := range tests {
		got := levenshtein(tc.a, tc.b)
		if got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d; want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestLevenshtein_Symmetry verifies that levenshtein(a,b) == levenshtein(b,a).
func TestLevenshtein_Symmetry(t *testing.T) {
	pairs := [][2]string{
		{"aria-core", "ariacore"},
		{"kitten", "sitting"},
		{"abc", "xyz"},
		{"", "hello"},
	}
	for _, p := range pairs {
		ab := levenshtein(p[0], p[1])
		ba := levenshtein(p[1], p[0])
		if ab != ba {
			t.Errorf("levenshtein(%q,%q)=%d != levenshtein(%q,%q)=%d (symmetry broken)",
				p[0], p[1], ab, p[1], p[0], ba)
		}
	}
}

// ─── FindSimilar unit tests ──────────────────────────────────────────────────

func TestFindSimilar_CaseInsensitiveAndSubstring(t *testing.T) {
	existing := []string{"ARIA-CORE", "aria-core-memory", "totally-different"}
	matches := FindSimilar("aria-core", existing, 3)

	if len(matches) < 2 {
		t.Fatalf("expected at least 2 matches, got %d: %v", len(matches), matches)
	}

	// First match should be case-insensitive
	if matches[0].Name != "ARIA-CORE" || matches[0].MatchType != "case-insensitive" {
		t.Errorf("first match = %+v; want {ARIA-CORE, case-insensitive}", matches[0])
	}

	// Second match should be substring
	if matches[1].Name != "aria-core-memory" || matches[1].MatchType != "substring" {
		t.Errorf("second match = %+v; want {aria-core-memory, substring}", matches[1])
	}
}

func TestFindSimilar_TiandaGroup(t *testing.T) {
	existing := []string{"tianda-for-woocommerce", "tianda-wc-plugin", "Tianda"}
	matches := FindSimilar("tianda", existing, 3)

	if len(matches) != 3 {
		t.Fatalf("expected 3 matches, got %d: %v", len(matches), matches)
	}

	// Tianda should be case-insensitive
	foundCase := false
	foundSub1 := false
	foundSub2 := false
	for _, m := range matches {
		switch m.Name {
		case "Tianda":
			foundCase = true
			if m.MatchType != "case-insensitive" {
				t.Errorf("Tianda match type = %q; want case-insensitive", m.MatchType)
			}
		case "tianda-for-woocommerce":
			foundSub1 = true
			if m.MatchType != "substring" {
				t.Errorf("tianda-for-woocommerce match type = %q; want substring", m.MatchType)
			}
		case "tianda-wc-plugin":
			foundSub2 = true
			if m.MatchType != "substring" {
				t.Errorf("tianda-wc-plugin match type = %q; want substring", m.MatchType)
			}
		}
	}
	if !foundCase {
		t.Error("Tianda not found in results")
	}
	if !foundSub1 {
		t.Error("tianda-for-woocommerce not found in results")
	}
	if !foundSub2 {
		t.Error("tianda-wc-plugin not found in results")
	}
}

func TestFindSimilar_ExcludesExactMatch(t *testing.T) {
	existing := []string{"aria-core"}
	matches := FindSimilar("aria-core", existing, 3)

	if len(matches) != 0 {
		t.Errorf("expected empty results for exact match, got %v", matches)
	}
}

func TestFindSimilar_NothingSimilar(t *testing.T) {
	existing := []string{"xyz", "qrs", "totally-unrelated"}
	matches := FindSimilar("abc", existing, 1)

	if len(matches) != 0 {
		t.Errorf("expected no matches, got %v", matches)
	}
}

func TestFindSimilar_LevenshteinHit(t *testing.T) {
	existing := []string{"ariacore"} // distance 1 from "aria-core" (drop hyphen)
	matches := FindSimilar("aria-core", existing, 2)

	if len(matches) != 1 {
		t.Fatalf("expected 1 levenshtein match, got %d: %v", len(matches), matches)
	}
	if matches[0].Name != "ariacore" {
		t.Errorf("match name = %q; want ariacore", matches[0].Name)
	}
	if matches[0].MatchType != "levenshtein" {
		t.Errorf("match type = %q; want levenshtein", matches[0].MatchType)
	}
	if matches[0].Distance != 1 {
		t.Errorf("distance = %d; want 1", matches[0].Distance)
	}
}

func TestFindSimilar_LevenshteinBeyondMaxDistance(t *testing.T) {
	existing := []string{"completely-different-string"}
	// "aria-core" vs "completely-different-string" is far beyond maxDistance=2
	matches := FindSimilar("aria-core", existing, 2)

	if len(matches) != 0 {
		t.Errorf("expected no matches beyond max distance, got %v", matches)
	}
}

func TestFindSimilar_OrderingCaseFirst(t *testing.T) {
	// Verify ordering: case-insensitive → substring → levenshtein
	existing := []string{
		"ariacore",      // levenshtein distance 1 (drop hyphen)
		"ARIA-CORE",     // case-insensitive
		"aria-core-old", // substring
	}
	matches := FindSimilar("aria-core", existing, 2)

	if len(matches) < 3 {
		t.Fatalf("expected 3 matches, got %d: %v", len(matches), matches)
	}

	if matches[0].MatchType != "case-insensitive" {
		t.Errorf("matches[0].MatchType = %q; want case-insensitive", matches[0].MatchType)
	}
	if matches[1].MatchType != "substring" {
		t.Errorf("matches[1].MatchType = %q; want substring", matches[1].MatchType)
	}
	if matches[2].MatchType != "levenshtein" {
		t.Errorf("matches[2].MatchType = %q; want levenshtein", matches[2].MatchType)
	}
}

func TestFindSimilar_EmptyExisting(t *testing.T) {
	matches := FindSimilar("aria-core", []string{}, 3)
	if len(matches) != 0 {
		t.Errorf("expected no matches for empty existing list, got %v", matches)
	}
}

func TestFindSimilar_ZeroMaxDistance(t *testing.T) {
	// With maxDistance=0, only exact levenshtein=0 would match — but those are
	// caught by the case-insensitive check first. Verify levenshtein matches
	// at distance > 0 are excluded.
	existing := []string{"ariacore"} // distance 1 (drop hyphen)
	matches := FindSimilar("aria-core", existing, 0)

	if len(matches) != 0 {
		t.Errorf("expected no matches with maxDistance=0, got %v", matches)
	}
}
