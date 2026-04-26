package redactor

import (
	"testing"

	"github.com/google/uuid"
)

// TestInferSensitivity_TableDriven covers the rules from the module spec.
//
//  1. scope=client_knowledge OR clientID set        -> at least 'client'
//  2. RFC / CURP / CLABE detected                    -> 'confidential'
//  3. Amount >= 100,000 detected                     -> at least 'client'
//  4. scope=personal AND password/token leak detected -> 'confidential'
//  5. otherwise                                      -> 'internal'
func TestInferSensitivity_TableDriven(t *testing.T) {
	s := New(Config{}).(*service)
	id := uuid.New()

	cases := []struct {
		name      string
		narrative string
		facts     string
		scope     string
		clientID  *uuid.UUID
		want      Sensitivity
	}{
		// rule 5
		{"empty -> internal", "", "", "personal", nil, SensitivityInternal},
		{"plain notes -> internal", "fix bug in handler", "", "project", nil, SensitivityInternal},

		// rule 1 — scope or clientID elevates
		{"client_knowledge -> client", "anything", "", "client_knowledge", nil, SensitivityClient},
		{"clientID set -> client", "anything", "", "personal", &id, SensitivityClient},

		// rule 2 — overrides scope
		{"RFC in narrative -> confidential", "RFC ABCD123456XYZ del cliente", "", "personal", nil, SensitivityConfidential},
		{"RFC in facts -> confidential", "x", "rfc:ABCD123456XYZ", "personal", nil, SensitivityConfidential},
		{"CURP -> confidential", "CURP BADD110313HCMLNS09", "", "personal", nil, SensitivityConfidential},
		{"CLABE -> confidential", "deposito a 012180001234567890", "", "personal", nil, SensitivityConfidential},
		{"RFC in client scope still confidential", "RFC ABCD123456XYZ", "", "client_knowledge", nil, SensitivityConfidential},

		// rule 3
		{"amount > 100K personal -> client", "monto $500,000.00 MXN", "", "personal", nil, SensitivityClient},
		{"amount > 100K + clientID -> client", "monto $250,000", "", "personal", &id, SensitivityClient},
		{"amount < 100K -> internal", "monto $5,000", "", "personal", nil, SensitivityInternal},

		// rule 4
		{"password leak personal -> confidential", "creds: password=qwerty1234", "", "personal", nil, SensitivityConfidential},
		{"password leak project not personal -> internal", "creds: password=qwerty1234", "", "project", nil, SensitivityInternal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := s.InferSensitivity(c.narrative, c.facts, c.scope, c.clientID)
			if got != c.want {
				t.Errorf("got %s want %s", got, c.want)
			}
		})
	}
}

// TestInferSensitivityString_BridgeContract verifies that the string-keyed
// adapter (used by ariamem to avoid a package import cycle) produces the same
// classifications as the typed API.
func TestInferSensitivityString_BridgeContract(t *testing.T) {
	s := New(Config{}).(*service)
	got := s.InferSensitivityString("RFC ABCD123456XYZ", "", "personal", "")
	if got != string(SensitivityConfidential) {
		t.Errorf("expected confidential, got %q", got)
	}
	got = s.InferSensitivityString("hola", "", "client_knowledge", "")
	if got != string(SensitivityClient) {
		t.Errorf("expected client, got %q", got)
	}
	got = s.InferSensitivityString("hola", "", "personal", uuid.New().String())
	if got != string(SensitivityClient) {
		t.Errorf("clientID set should be client, got %q", got)
	}
}
