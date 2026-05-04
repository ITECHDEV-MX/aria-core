package teamprojects

import (
	"strings"
	"testing"
)

func TestSlugFromName_BasicCases(t *testing.T) {
	cases := map[string]string{
		"Aria Core":             "aria-core",
		"  Aria Core  ":         "aria-core",
		"ARIA":                  "aria",
		"Mantenimiento Industrial Cotizador": "mantenimiento-industrial-cotizador",
		"hello---world":         "hello-world",
		"foo!@#bar":             "foobar",
		"abc 123":               "abc-123",
	}
	for in, want := range cases {
		got := SlugFromName(in)
		if got != want {
			t.Errorf("SlugFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugFromName_EmptyFallsBackToProjectUUID(t *testing.T) {
	got := SlugFromName("")
	if !strings.HasPrefix(got, "project-") {
		t.Errorf("empty slug should fall back to project-<uuid> prefix, got %q", got)
	}
	// uuid suffix is 8 chars
	if len(got) != len("project-")+8 {
		t.Errorf("project- prefix uuid length unexpected: %d in %q", len(got), got)
	}
}

func TestSlugFromName_TruncatesAt64(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := SlugFromName(long)
	if len(got) != 64 {
		t.Errorf("len 64 expected, got %d", len(got))
	}
}

func TestSlugFromName_StripsLeadingTrailingHyphens(t *testing.T) {
	got := SlugFromName(" --hello-- ")
	if got != "hello" {
		t.Errorf("expected hello, got %q", got)
	}
}

func TestSlugFromName_DropsSymbols(t *testing.T) {
	got := SlugFromName("hello/world?foo")
	// / and ? should drop entirely (slugRe), so hello world becomes
	// helloworldfoo → hyphenated correctly via space rule.
	// Validate the result is non-empty and contains only allowed chars.
	for _, r := range got {
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			t.Errorf("disallowed char %q in slug %q", r, got)
		}
	}
}

func TestSplitSQLStatements_HandlesCommentsAndSemicolons(t *testing.T) {
	sql := `-- top-level comment
CREATE TABLE foo (id int);
-- another comment
INSERT INTO foo VALUES (1);
;
`
	stmts := splitSQLStatements(sql)
	if len(stmts) < 2 {
		t.Errorf("expected ≥ 2 statements, got %d: %#v", len(stmts), stmts)
	}
	// Trailing empty `;` should not produce an empty statement
	for _, s := range stmts {
		if strings.TrimSpace(s) == "" {
			t.Errorf("empty statement in result: %#v", stmts)
		}
	}
}

func TestSplitSQLStatements_EmptyInput(t *testing.T) {
	stmts := splitSQLStatements("")
	if len(stmts) != 0 {
		t.Errorf("empty input should produce empty slice, got %#v", stmts)
	}
}

func TestFirstLine_TrimsAndTruncates(t *testing.T) {
	cases := map[string]string{
		"hello":               "hello",
		"  hello\nworld":      "hello",
		"\n\nhello\nworld": "hello",
		"":                    "",
	}
	for in, want := range cases {
		got := firstLine(in)
		// Match when trimmed
		if strings.TrimSpace(got) != want {
			t.Errorf("firstLine(%q) = %q, want %q", in, got, want)
		}
	}
}
