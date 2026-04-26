package cotizador

import (
	"strings"
	"testing"
)

func TestParseSectionBlocks(t *testing.T) {
	in := `Acá tenés un primer borrador.

## SECCIÓN: Resumen ejecutivo
PARK Salesforce v2 — implementación greenfield para [CLIENT-1234].
Cierra Q3 2026.

## SECCIÓN: Alcance
- Análisis de requerimientos
- Migración legacy a SF
- Capacitación

## Sección: Inversión
Total: [AMOUNT-9876]
`

	got := ParseSectionBlocks(in)
	if len(got) != 3 {
		t.Fatalf("expected 3 sections, got %d: %#v", len(got), got)
	}
	if got[0].Title != "Resumen ejecutivo" {
		t.Errorf("section[0].Title = %q", got[0].Title)
	}
	if !strings.Contains(got[0].Content, "PARK") {
		t.Errorf("section[0] content missing body: %q", got[0].Content)
	}
	if got[1].Title != "Alcance" {
		t.Errorf("section[1].Title = %q", got[1].Title)
	}
	if !strings.Contains(got[1].Content, "Migración") {
		t.Errorf("section[1] content body wrong: %q", got[1].Content)
	}
	if got[2].Title != "Inversión" {
		t.Errorf("section[2].Title = %q", got[2].Title)
	}
}

func TestParseSectionBlocksEmpty(t *testing.T) {
	if got := ParseSectionBlocks(""); got != nil {
		t.Errorf("empty input should yield nil, got %v", got)
	}
	if got := ParseSectionBlocks("just text"); got != nil {
		t.Errorf("no headers should yield nil, got %v", got)
	}
}

func TestSlugifySectionKey(t *testing.T) {
	cases := map[string]string{
		"Resumen ejecutivo":      "resumen-ejecutivo",
		"Alcance Técnico":        "alcance-tecnico",
		"Inversión / Pricing":    "inversion-pricing",
		"Plan de trabajo":        "plan-de-trabajo",
		"  Términos & condiciones ": "terminos-condiciones",
		"":                       "section",
	}
	for in, want := range cases {
		if got := slugifySectionKey(in); got != want {
			t.Errorf("slugifySectionKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestProviderForChannel(t *testing.T) {
	if providerForChannel("claude-max-vps") != "anthropic" {
		t.Errorf("claude-max-vps mapping wrong")
	}
	if providerForChannel("gemma-local") != "ollama-local" {
		t.Errorf("gemma-local mapping wrong")
	}
	if providerForChannel("") != "unknown" {
		t.Errorf("empty mapping wrong")
	}
}
