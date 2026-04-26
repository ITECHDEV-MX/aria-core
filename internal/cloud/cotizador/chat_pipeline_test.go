package cotizador

import (
	"context"
	"strings"
	"testing"
)

// fakeChatScrubber is a stub redactor that echoes input unchanged but tracks
// whether ScrubString / Expand were called.
type fakeChatScrubber struct {
	scrubbed bool
	expanded bool
}

func (f *fakeChatScrubber) ScrubString(_ context.Context, text string) (string, string) {
	f.scrubbed = true
	// Replace "CLIENT-ACME" with the alias [CLIENT-1234] to simulate scrub.
	out := strings.ReplaceAll(text, "ACME SA", "[CLIENT-1234]")
	return out, "[]"
}

func (f *fakeChatScrubber) Expand(_ context.Context, text string) (string, error) {
	f.expanded = true
	out := strings.ReplaceAll(text, "[CLIENT-1234]", "ACME SA")
	return out, nil
}

func (f *fakeChatScrubber) LogEgress(_ context.Context, _, _, _, _, _, _, _, _ string, _ int, _ bool, _ string) error {
	return nil
}

// TestScrubExpandRoundTrip validates the contract used by the orchestrator.
// We don't test SendUserMessage end-to-end here (that needs a DB) — but we
// do verify the scrubber contract works for the data flow described in the
// orchestrator.
func TestScrubExpandRoundTrip(t *testing.T) {
	s := &fakeChatScrubber{}
	// Step 1: scrub user message before sending to LLM.
	input := "Necesito propuesta para ACME SA, contacto Juan."
	scrubbed, _ := s.ScrubString(context.Background(), input)
	if !strings.Contains(scrubbed, "[CLIENT-1234]") {
		t.Errorf("scrubbed should contain alias: %q", scrubbed)
	}
	if strings.Contains(scrubbed, "ACME SA") {
		t.Errorf("scrubbed should NOT contain real name: %q", scrubbed)
	}
	// Step 2: assistant response uses the alias too.
	llmResponse := "Para [CLIENT-1234] propongo:\n## SECCIÓN: Resumen\nPropuesta inicial."
	// Step 3: expand the alias back for display.
	expanded, err := s.Expand(context.Background(), llmResponse)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if !strings.Contains(expanded, "ACME SA") {
		t.Errorf("expanded should restore real name: %q", expanded)
	}
	if !s.scrubbed {
		t.Error("ScrubString should have been called")
	}
	if !s.expanded {
		t.Error("Expand should have been called")
	}
}

// TestParseSectionExtractsFromAssistantResponse verifies the section parser
// pulls structured blocks from a real-shaped assistant response.
func TestParseSectionExtractsFromAssistantResponse(t *testing.T) {
	resp := `Listo, te dejo el primer borrador:

## SECCIÓN: Resumen ejecutivo
Implementación PARK Salesforce v2 para [CLIENT-1234], 8 semanas.

## SECCIÓN: Alcance
- Análisis
- Migración
- QA + Capacitación

## SECCIÓN: Inversión
Total: [AMOUNT-9876] MXN.`

	got := ParseSectionBlocks(resp)
	if len(got) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(got))
	}
	titles := []string{got[0].Title, got[1].Title, got[2].Title}
	want := []string{"Resumen ejecutivo", "Alcance", "Inversión"}
	for i, w := range want {
		if titles[i] != w {
			t.Errorf("title[%d] = %q, want %q", i, titles[i], w)
		}
	}
}
