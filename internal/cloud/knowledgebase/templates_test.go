package knowledgebase

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRenderPageMarkdownPRD(t *testing.T) {
	fm := PRDFrontmatter{
		ID:           "11111111-1111-1111-1111-111111111111",
		Title:        "Cobranza inteligente",
		Project:      "park",
		Type:         "prd",
		Status:       "draft",
		CreatedBy:    "jc",
		CreatedAt:    time.Date(2026, 4, 1, 10, 0, 0, 0, time.UTC),
		LastUpdated:  time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC),
		DashboardURL: "https://ariacore.itechdev.com.mx/dashboard/pages/abc",
	}
	body := "# PRD\n\n## Problema\nLas cobranzas tardan demasiado.\n"
	got := RenderPageMarkdown(fm, body)

	wantSubs := []string{
		"---",
		"id: 11111111-1111-1111-1111-111111111111",
		"title: Cobranza inteligente",
		"project: park",
		"type: prd",
		"status: draft",
		`dashboard_url: "https://ariacore.itechdev.com.mx/dashboard/pages/abc"`,
		"# PRD",
		"## Problema",
	}
	for _, s := range wantSubs {
		if !strings.Contains(got, s) {
			t.Fatalf("RenderPageMarkdown missing %q in:\n%s", s, got)
		}
	}
	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("expected frontmatter to be the first thing, got:\n%s", got)
	}
}

func TestRenderQuoteMarkdownIncludesItemsAndTotals(t *testing.T) {
	in := QuoteRenderInput{
		Folio:                  "ITD-2026-001-001",
		ProductName:            "Plataforma cobranza",
		ProductSubtitle:        "Suite RPA + dashboard",
		ProposalType:           "servicio",
		PreparedForCompany:     "PARK Inmobiliaria",
		PreparedForArea:        "Cobranzas",
		PreparedForContactName: "Ana Pérez",
		PreparedByName:         "JC",
		Currency:               "MXN",
		Subtotal:               150000,
		Total:                  150000,
		Items: []QuoteItemView{
			{SKU: "RPA-001", Description: "Bot de cobranza", Qty: 1, UnitPrice: 100000, Subtotal: 100000},
			{SKU: "DASH-001", Description: "Dashboard ejecutivo", Qty: 1, UnitPrice: 50000, Subtotal: 50000},
		},
		Sections: []QuoteSectionView{
			{Key: "alcance", Title: "Alcance", ContentMD: "Implementación end-to-end.", SortOrder: 1},
			{Key: "soporte", Title: "Soporte", ContentMD: "Soporte 30 días post go-live.", SortOrder: 2},
		},
	}
	out := RenderQuoteMarkdown(in)
	want := []string{
		"# Plataforma cobranza",
		"*Suite RPA + dashboard*",
		"ITD-2026-001-001",
		"PARK Inmobiliaria",
		"## Conceptos",
		"RPA-001",
		"DASH-001",
		"$150,000.00 MXN",
		"## Alcance",
		"## Soporte",
	}
	for _, s := range want {
		if !strings.Contains(out, s) {
			t.Fatalf("RenderQuoteMarkdown missing %q in:\n%s", s, out)
		}
	}
}

func TestRenderProjectReadmeRendersTeamTable(t *testing.T) {
	now := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	p := ProjectInfo{
		Slug:        "park",
		Name:        "PARK Inmobiliaria",
		Description: "Plataforma cobranza + dashboard.",
		Status:      "in_progress",
		StartedAt:   &now,
		Members: []ProjectMember{
			{UID: "u1", Name: "JC", Role: "Lead"},
			{UID: "u2", Name: "Ana", Role: "Engineer"},
		},
		DashboardURL: "https://ariacore.itechdev.com.mx/dashboard/projects/team/park",
	}
	out := RenderProjectReadme(p)
	for _, w := range []string{"# PARK Inmobiliaria", "## Equipo", "JC", "Ana", "in_progress", "Engineer"} {
		if !strings.Contains(out, w) {
			t.Fatalf("RenderProjectReadme missing %q in:\n%s", w, out)
		}
	}
}

func TestStandardProjectReadmeIsConsumableByWave7(t *testing.T) {
	p := ProjectInfo{
		Slug:        "mercedes-rpa",
		Name:        "Mercedes RPA",
		Description: "Automatización procesos Mercedes.",
		Status:      "shipped",
		Members: []ProjectMember{
			{UID: "u1", Name: "JC", Role: "PM"},
		},
		CodeRepoURL:  "https://github.com/ITECHDEV-MX/mercedes-rpa",
		DashboardURL: "https://ariacore.itechdev.com.mx/dashboard/projects/team/mercedes-rpa",
	}
	out := StandardProjectReadme(p)
	mustContain := []string{
		"# Mercedes RPA",
		"team-knowledge-base",
		"proyectos/mercedes-rpa/",
		"shipped",
	}
	for _, w := range mustContain {
		if !strings.Contains(out, w) {
			t.Fatalf("StandardProjectReadme missing %q in:\n%s", w, out)
		}
	}
}

func TestRenderRootIndexSortsAlphabetically(t *testing.T) {
	entries := []IndexEntry{
		{Slug: "zeta", Name: "Zeta Project", Status: "draft"},
		{Slug: "alfa", Name: "Alfa Project", Status: "in_progress"},
		{Slug: "mike", Name: "Mike", Status: "shipped"},
	}
	out := RenderRootIndex(entries)
	idxAlfa := strings.Index(out, "Alfa Project")
	idxMike := strings.Index(out, "Mike")
	idxZeta := strings.Index(out, "Zeta Project")
	if idxAlfa < 0 || idxMike < 0 || idxZeta < 0 {
		t.Fatalf("missing entries:\n%s", out)
	}
	if !(idxAlfa < idxMike && idxMike < idxZeta) {
		t.Fatalf("expected alphabetical order (Alfa<Mike<Zeta), got positions %d,%d,%d:\n%s",
			idxAlfa, idxMike, idxZeta, out)
	}
}

func TestQuoteMetadataMarshalsJSON(t *testing.T) {
	m := QuoteMetadata{
		QuoteID:     "abc",
		Folio:       "ITD-X",
		Status:      "draft",
		Currency:    "MXN",
		Total:       1000,
		ProductName: "Demo",
		Tags:        []string{"a", "b"},
		GeneratedAt: time.Now().UTC(),
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"quote_id":"abc"`) {
		t.Fatalf("missing quote_id in %s", string(b))
	}
	if !strings.Contains(string(b), `"tags":["a","b"]`) {
		t.Fatalf("missing tags array in %s", string(b))
	}
}

func TestRenderRootIndexEmptyShowsHint(t *testing.T) {
	out := RenderRootIndex(nil)
	if !strings.Contains(out, "Aún no hay proyectos") {
		t.Fatalf("expected empty hint, got:\n%s", out)
	}
}

func TestSlugifyHandlesAccents(t *testing.T) {
	cases := map[string]string{
		"PRD: Cobranza Automática": "prd-cobranza-automatica",
		"Mañana es lunes":          "manana-es-lunes",
		"   spaces   ":             "spaces",
		"":                         "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestExtractSequence(t *testing.T) {
	cases := map[string]int{
		"proyectos/park/prds/00007-cobranza.md": 7,
		"proyectos/park/prds/0007-cobranza.md":  0, // requires 5 digits
		"random/path":                           0,
	}
	for in, want := range cases {
		if got := extractSequence(in); got != want {
			t.Errorf("extractSequence(%q) = %d, want %d", in, got, want)
		}
	}
}
