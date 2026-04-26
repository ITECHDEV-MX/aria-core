package pages

import (
	"strings"
	"testing"
)

// TestBuiltinTemplatesShape verifica que los 5 templates builtin existan y tengan
// la forma esperada (key, name, body no vacío). Tests de DB (SeedTemplates idempotente)
// se cubren en pages_pg_test.go con build tag pg.
func TestBuiltinTemplatesShape(t *testing.T) {
	tpls := BuiltinTemplates()
	if len(tpls) != 5 {
		t.Fatalf("expected 5 builtin templates, got %d", len(tpls))
	}
	wantKeys := map[string]bool{
		"prd-v1":               false,
		"incident-v1":          false,
		"one-on-one-v1":        false,
		"adr-v1":               false,
		"client-onboarding-v1": false,
	}
	for _, tpl := range tpls {
		if _, ok := wantKeys[tpl.Key]; !ok {
			t.Errorf("unexpected template key %q", tpl.Key)
			continue
		}
		wantKeys[tpl.Key] = true
		if strings.TrimSpace(tpl.Name) == "" {
			t.Errorf("template %q has blank Name", tpl.Key)
		}
		if strings.TrimSpace(tpl.BodyMD) == "" {
			t.Errorf("template %q has blank BodyMD", tpl.Key)
		}
		// Cada body debería arrancar con un H1.
		if !strings.HasPrefix(tpl.BodyMD, "# ") {
			t.Errorf("template %q body should start with H1, got: %q", tpl.Key, firstLineOf(tpl.BodyMD))
		}
	}
	for k, found := range wantKeys {
		if !found {
			t.Errorf("missing template key %q", k)
		}
	}
}

func TestGetBuiltinTemplate(t *testing.T) {
	tpl := GetBuiltinTemplate("prd-v1")
	if tpl == nil {
		t.Fatal("expected prd-v1 template")
	}
	if !strings.Contains(tpl.BodyMD, "PRD") {
		t.Errorf("expected PRD body to contain 'PRD'")
	}
	if got := GetBuiltinTemplate("nonexistent"); got != nil {
		t.Errorf("expected nil for unknown key, got %v", got)
	}
}

func TestPRDTemplateHasExpectedSections(t *testing.T) {
	tpl := GetBuiltinTemplate("prd-v1")
	if tpl == nil {
		t.Fatal("missing prd-v1")
	}
	wanted := []string{"## Problema", "## Métricas", "## Requisitos funcionales", "## Riesgos", "Decisión final"}
	for _, w := range wanted {
		if !strings.Contains(tpl.BodyMD, w) {
			t.Errorf("PRD template missing section %q", w)
		}
	}
}

func TestIncidentTemplateHasTimeline(t *testing.T) {
	tpl := GetBuiltinTemplate("incident-v1")
	if tpl == nil {
		t.Fatal("missing incident-v1")
	}
	wanted := []string{"## Timeline", "## Root cause", "Severity", "## Lessons learned"}
	for _, w := range wanted {
		if !strings.Contains(tpl.BodyMD, w) {
			t.Errorf("Incident template missing %q", w)
		}
	}
}

func TestADRTemplateHasStatusAndAlternatives(t *testing.T) {
	tpl := GetBuiltinTemplate("adr-v1")
	if tpl == nil {
		t.Fatal("missing adr-v1")
	}
	wanted := []string{"**Status**", "## Context", "## Decision", "## Consequences", "## Alternatives"}
	for _, w := range wanted {
		if !strings.Contains(tpl.BodyMD, w) {
			t.Errorf("ADR template missing %q", w)
		}
	}
}

func firstLineOf(s string) string {
	for _, line := range strings.Split(s, "\n") {
		t := strings.TrimSpace(line)
		if t != "" {
			return t
		}
	}
	return s
}
