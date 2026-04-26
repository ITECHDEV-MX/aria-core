package pages

import (
	"strings"
	"testing"
)

// Estos tests son unit-level: validan la lógica pura del paquete sin DB.
// Tests con Postgres viven en pages_pg_test.go (build tag pg) — fuera del default
// para CI donde no hay DB. Las assertions abajo cubren slugify, normalizers,
// breadcrumb building (vía DB), y el shape de los templates builtin.

func TestSlugify(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"Hello World", "hello-world"},
		{"  Espacios   raros  ", "espacios-raros"},
		{"Acentúos áéíóú ñ", "acentuos-aeiou-n"},
		{"Special!@#$%^&*()chars", "special-chars"},
		{"", ""},
	}
	for _, c := range cases {
		if got := slugify(c.in); got != c.want {
			t.Errorf("slugify(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSlugifyTruncates(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := slugify(long)
	if len(got) > 60 {
		t.Errorf("slugify did not truncate: len=%d", len(got))
	}
}

func TestNormalizeScope(t *testing.T) {
	cases := map[string]string{
		"":                 "project",
		"PERSONAL":         "personal",
		"team":             "team",
		"client_knowledge": "client_knowledge",
		"weird":            "project",
	}
	for in, want := range cases {
		if got := normalizeScope(in); got != want {
			t.Errorf("normalizeScope(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeSensitivity(t *testing.T) {
	cases := map[string]string{
		"":              "internal",
		"PUBLIC":        "public",
		"client":        "client",
		"confidential":  "confidential",
		"unknown_value": "internal",
	}
	for in, want := range cases {
		if got := normalizeSensitivity(in); got != want {
			t.Errorf("normalizeSensitivity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsUUID(t *testing.T) {
	if !isUUID("11111111-2222-3333-4444-555555555555") {
		t.Error("expected valid uuid")
	}
	if isUUID("not-a-uuid") {
		t.Error("expected invalid uuid")
	}
	if isUUID("") {
		t.Error("expected empty to be invalid")
	}
}

func TestNullableUUIDFiltersInvalid(t *testing.T) {
	// Vacío → nil
	if got := nullableUUID(""); got != nil {
		t.Errorf("expected nil for empty, got %v", got)
	}
	// Inválido → nil (no panic)
	if got := nullableUUID("garbage"); got != nil {
		t.Errorf("expected nil for invalid uuid, got %v", got)
	}
	// Válido → string passthrough
	id := "11111111-2222-3333-4444-555555555555"
	if got := nullableUUID(id); got != id {
		t.Errorf("expected %q, got %v", id, got)
	}
}

func TestSlugComputesIDSuffix(t *testing.T) {
	p := &Page{
		ID:    "abcdef01-2345-6789-abcd-ef0123456789",
		Title: "Hello World",
	}
	got := Slug(p)
	if !strings.HasPrefix(got, "hello-world-") {
		t.Errorf("expected prefix 'hello-world-', got %q", got)
	}
	// Suffix son los últimos 8 chars del UUID sin guiones.
	if !strings.HasSuffix(got, "23456789") {
		t.Errorf("expected suffix '23456789', got %q", got)
	}
}

func TestSlugFallsBackToPageWhenTitleBlank(t *testing.T) {
	p := &Page{
		ID:    "abcdef01-2345-6789-abcd-ef0123456789",
		Title: "",
	}
	got := Slug(p)
	if !strings.HasPrefix(got, "page-") {
		t.Errorf("expected fallback 'page-', got %q", got)
	}
}

// ─── QuickHit composeSubtitle ───────────────────────────────────────────────

func TestComposeSubtitle(t *testing.T) {
	cases := []struct {
		parts []string
		want  string
	}{
		{[]string{"foo", "bar"}, "foo · bar"},
		{[]string{"", "bar"}, "bar"},
		{[]string{"", ""}, ""},
		{[]string{"  spaced  ", "x"}, "spaced · x"},
	}
	for _, c := range cases {
		if got := composeSubtitle(c.parts...); got != c.want {
			t.Errorf("composeSubtitle(%v) = %q, want %q", c.parts, got, c.want)
		}
	}
}

// ─── QuickSearchResult.All ──────────────────────────────────────────────────

func TestQuickSearchResultAllOrdersByScore(t *testing.T) {
	r := &QuickSearchResult{
		Pages: []QuickHit{
			{ID: "p1", Title: "p1", Score: 0.9},
		},
		Observations: []QuickHit{
			{ID: "o1", Title: "o1", Score: 1.5},
		},
		Skills: []QuickHit{
			{ID: "s1", Title: "s1", Score: 0.5},
		},
	}
	all := r.All()
	if len(all) != 3 {
		t.Fatalf("expected 3 hits, got %d", len(all))
	}
	if all[0].ID != "o1" || all[1].ID != "p1" || all[2].ID != "s1" {
		t.Errorf("expected order o1, p1, s1; got %v", all)
	}
}

func TestQuickSearchResultAllNilSafe(t *testing.T) {
	var r *QuickSearchResult
	if got := r.All(); got != nil {
		t.Errorf("expected nil from nil receiver, got %v", got)
	}
}

// ─── stringOr ───────────────────────────────────────────────────────────────

func TestStringOrUsesPointerOrFallback(t *testing.T) {
	v := "supplied"
	if got := stringOr(&v, "fallback"); got != "supplied" {
		t.Errorf("expected 'supplied', got %q", got)
	}
	if got := stringOr(nil, "fallback"); got != "fallback" {
		t.Errorf("expected 'fallback', got %q", got)
	}
}
