package redactor

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// TestLogEgress_NilDB_NoOp asserts that a nil-DB redactor swallows LogEgress
// silently (used in tests / when audit table is not yet migrated).
func TestLogEgress_NilDB_NoOp(t *testing.T) {
	s := New(Config{}) // no DB
	err := s.LogEgress(context.Background(), EgressParams{
		RequestID:      uuid.New(),
		LLMProvider:    "anthropic",
		Scrubbed:       true,
		Payload:        "hola",
		InitiatedByUID: uuid.New(),
		Reason:         "test",
	})
	if err != nil {
		t.Errorf("nil-DB LogEgress should be no-op, got %v", err)
	}
}

// TestSensitivityRules_TablesMatchSpec walks the contract.
func TestSensitivityRules_TablesMatchSpec(t *testing.T) {
	s := New(Config{}).(*service)

	// Personal scope, plain text -> internal.
	if got := s.InferSensitivity("hola", "", "personal", nil); got != SensitivityInternal {
		t.Errorf("plain personal should be internal, got %s", got)
	}

	// client_knowledge always >= client.
	if got := s.InferSensitivity("hola", "", "client_knowledge", nil); got != SensitivityClient {
		t.Errorf("client_knowledge should be client, got %s", got)
	}

	// clientID set escalates to at least client.
	id := uuid.New()
	if got := s.InferSensitivity("hola", "", "personal", &id); got != SensitivityClient {
		t.Errorf("clientID set should escalate to client, got %s", got)
	}

	// RFC anywhere -> confidential.
	if got := s.InferSensitivity("contiene RFC ABCD123456XYZ", "", "personal", nil); got != SensitivityConfidential {
		t.Errorf("RFC should be confidential, got %s", got)
	}

	// Big amount with no clientID escalates to client.
	if got := s.InferSensitivity("monto $500,000 MXN", "", "personal", nil); got != SensitivityClient {
		t.Errorf("big amount should be client, got %s", got)
	}

	// Big amount with clientID still confidential? -> client (no PII).
	if got := s.InferSensitivity("monto $500,000 MXN", "", "personal", &id); got != SensitivityClient {
		t.Errorf("big amount + client should be client, got %s", got)
	}
}

// TestProviderAllowlist_FromEnvOverride verifies that the allowlist field is
// honored, overriding env.
func TestProviderAllowlist_FromEnvOverride(t *testing.T) {
	s := New(Config{ProviderAllowed: []string{"anthropic"}}).(*service)
	if !s.providerAllowed("anthropic") {
		t.Errorf("anthropic should be allowed when in list")
	}
	if s.providerAllowed("ollama-local") {
		t.Errorf("ollama-local NOT in list -> should be denied")
	}
	if s.providerAllowed("openai") {
		t.Errorf("openai NOT in list -> denied")
	}
}
