package redactor

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func newTestService() Service {
	return New(Config{
		ProviderAllowed: []string{"anthropic", "ollama-local"},
	})
}

func TestScrub_RFCEmailPhoneAmount(t *testing.T) {
	s := newTestService()
	in := "Cliente ABCD123456XYZ contacto@parkinc.com tel +52 81 1234 5678 monto $1,250,000.00 MXN"
	res, err := s.Scrub(context.Background(), in, ScrubOptions{Mode: ModeTokens})
	if err != nil {
		t.Fatalf("Scrub error: %v", err)
	}
	out := res.Output
	if strings.Contains(out, "ABCD123456XYZ") {
		t.Errorf("RFC not scrubbed: %s", out)
	}
	if strings.Contains(out, "contacto@parkinc.com") {
		t.Errorf("email not scrubbed: %s", out)
	}
	if strings.Contains(out, "1,250,000") {
		t.Errorf("amount not scrubbed: %s", out)
	}
	if !strings.Contains(out, "[RFC-") {
		t.Errorf("expected RFC token, got: %s", out)
	}
	if !strings.Contains(out, "[EMAIL-") {
		t.Errorf("expected EMAIL token, got: %s", out)
	}
	if !strings.Contains(out, "[AMOUNT-") {
		t.Errorf("expected AMOUNT token, got: %s", out)
	}
	types := redactionTypes(res.Redactions)
	for _, want := range []string{"rfc", "email", "amount"} {
		if !contains(types, want) {
			t.Errorf("expected redaction type %q in %v", want, types)
		}
	}
}

func TestScrub_DeterministicTokens(t *testing.T) {
	s := newTestService()
	r1, _ := s.Scrub(context.Background(), "RFC ABCD123456XYZ", ScrubOptions{Mode: ModeTokens})
	r2, _ := s.Scrub(context.Background(), "RFC ABCD123456XYZ", ScrubOptions{Mode: ModeTokens})
	if r1.Output != r2.Output {
		t.Errorf("tokens not deterministic: %q vs %q", r1.Output, r2.Output)
	}
}

func TestScrub_RedactMode(t *testing.T) {
	s := newTestService()
	r, _ := s.Scrub(context.Background(), "RFC ABCD123456XYZ y email a@b.com", ScrubOptions{Mode: ModeRedact})
	if !strings.Contains(r.Output, "[REDACTED]") {
		t.Errorf("expected [REDACTED] tokens, got %q", r.Output)
	}
	if strings.Contains(r.Output, "ABCD123456XYZ") {
		t.Errorf("RFC leaked in redact mode: %q", r.Output)
	}
}

func TestScrub_RedactPreserveEmailDomain(t *testing.T) {
	s := newTestService()
	r, _ := s.Scrub(context.Background(), "email a@itechdev.com.mx", ScrubOptions{Mode: ModeRedact, PreserveStructure: true})
	if !strings.Contains(r.Output, "@itechdev.com.mx") {
		t.Errorf("expected preserved email domain, got %q", r.Output)
	}
	if strings.Contains(r.Output, "a@itechdev") {
		t.Errorf("local part should be redacted, got %q", r.Output)
	}
}

func TestExpand_RoundtripsTokens(t *testing.T) {
	s := newTestService()
	in := "RFC ABCD123456XYZ y email a@b.com"
	r, _ := s.Scrub(context.Background(), in, ScrubOptions{Mode: ModeTokens})
	expanded, err := s.Expand(context.Background(), r.Output)
	if err != nil {
		t.Fatalf("Expand error: %v", err)
	}
	if !strings.Contains(expanded, "ABCD123456XYZ") {
		t.Errorf("Expand did not restore RFC: got %q", expanded)
	}
	if !strings.Contains(expanded, "a@b.com") {
		t.Errorf("Expand did not restore email: got %q", expanded)
	}
}

func TestCanSendToLLM(t *testing.T) {
	s := newTestService()
	cases := []struct {
		sens    Sensitivity
		prov    string
		want    bool
		comment string
	}{
		{SensitivityPublic, "anthropic", true, "public always OK"},
		{SensitivityPublic, "openai", true, "public always OK even with disallowed"},
		{SensitivityInternal, "anthropic", true, "internal always OK"},
		{SensitivityClient, "anthropic", true, "client OK on allowlist"},
		{SensitivityClient, "ollama-local", true, "ollama-local in allowlist"},
		{SensitivityClient, "openai", false, "client NOT OK off allowlist"},
		{SensitivityClient, "", false, "client NOT OK without provider"},
		{SensitivityConfidential, "anthropic", false, "confidential never OK"},
		{SensitivityConfidential, "ollama-local", false, "confidential never OK"},
	}
	for _, c := range cases {
		got := s.CanSendToLLM(c.sens, c.prov)
		if got != c.want {
			t.Errorf("[%s] CanSendToLLM(%s, %s) = %v want %v", c.comment, c.sens, c.prov, got, c.want)
		}
	}
}

func TestInferSensitivity(t *testing.T) {
	s := newTestService().(*service)
	cases := []struct {
		name      string
		narrative string
		facts     string
		scope     string
		clientID  bool
		want      Sensitivity
	}{
		{"plain text -> internal", "hola mundo", "", "personal", false, SensitivityInternal},
		{"client_knowledge -> client", "lorem", "", "client_knowledge", false, SensitivityClient},
		{"clientID set -> client", "lorem", "", "personal", true, SensitivityClient},
		{"RFC present -> confidential", "el RFC ABCD123456XYZ", "", "personal", false, SensitivityConfidential},
		{"CURP present -> confidential", "CURP BADD110313HCMLNS09", "", "personal", false, SensitivityConfidential},
		{"CLABE present -> confidential", "CLABE 012180001234567890", "", "personal", false, SensitivityConfidential},
		{"big amount -> client", "monto $250,000 MXN", "", "personal", false, SensitivityClient},
		{"small amount -> internal", "monto $5,000 MXN", "", "personal", false, SensitivityInternal},
		{"password leak in personal -> confidential", "password=qwerty1234", "", "personal", false, SensitivityConfidential},
	}
	for _, c := range cases {
		var clientPtr = ptrIf(c.clientID)
		got := s.InferSensitivity(c.narrative, c.facts, c.scope, clientPtr)
		if got != c.want {
			t.Errorf("[%s] got %s want %s", c.name, got, c.want)
		}
	}
}

func TestScrub_DedupsRepeatedMatches(t *testing.T) {
	s := newTestService()
	in := "primer RFC ABCD123456XYZ, segundo RFC ABCD123456XYZ"
	r, _ := s.Scrub(context.Background(), in, ScrubOptions{Mode: ModeTokens})
	// Both occurrences must use the same token.
	tok := extractFirstToken(r.Output, "[RFC-")
	if tok == "" {
		t.Fatalf("no RFC token found in %q", r.Output)
	}
	count := strings.Count(r.Output, tok)
	if count != 2 {
		t.Errorf("expected 2 tokens, got %d in %q", count, r.Output)
	}
	// The redaction summary should list count=2.
	for _, red := range r.Redactions {
		if red.Type == "rfc" && red.Count != 2 {
			t.Errorf("expected RFC count=2, got %d", red.Count)
		}
	}
}

// ─── helpers ────────────────────────────────────────────────────────────────

func redactionTypes(rs []Redaction) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.Type)
	}
	return out
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}

// ptrIf returns a non-nil uuid pointer when on=true. The tests only care
// whether the pointer is nil, not the value.
func ptrIf(on bool) *uuid.UUID {
	if !on {
		return nil
	}
	v := uuid.New()
	return &v
}

func extractFirstToken(s, prefix string) string {
	i := strings.Index(s, prefix)
	if i < 0 {
		return ""
	}
	end := strings.Index(s[i:], "]")
	if end < 0 {
		return ""
	}
	return s[i : i+end+1]
}
