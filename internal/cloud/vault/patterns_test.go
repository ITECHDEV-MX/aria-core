package vault

import (
	"strings"
	"testing"
)

func TestLeakDetector_DetectsCanonicalPatterns(t *testing.T) {
	d := NewLeakDetector()
	cases := []struct {
		name    string
		text    string
		pattern string
	}{
		{"AWS access key", "credentials AKIAIOSFODNN7EXAMPLE here", "AWS_AK"},
		{"AWS secret key", `aws_secret_access_key="wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"`, "AWS_SK"},
		{"GitHub PAT classic", "ghp_aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789", "GH_PAT"},
		{"GitHub fine-grained", "github_pat_11AAAAAAA0aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "GH_OAUTH"},
		{"GitLab PAT", "glpat-aBcDeFgHiJkLmNoPqRsTuVwX", "GITLAB_PAT"},
		{"Slack bot token", "xoxb-1234567890-1234567890-aBcDeFgHiJkLmNoPqRsTuV", "SLACK_TOKEN"},
		{"Stripe live key", "key=sk_live_abc123ABC123abc123ABC", "STRIPE_KEY"},
		{"Google API key", "GOOGLE_API_KEY=AIzaSyA-1234567890abcdefghijk_lmnopqrst", "GOOGLE_API"},
		{"JWT", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTYifQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", "JWT"},
		{"PEM private key", "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBg", "BEGIN_PRIVATE_KEY"},
		{"OpenSSH private key", "-----BEGIN OPENSSH PRIVATE KEY-----\n", "BEGIN_PRIVATE_KEY"},
		{"password assign", "DB_PASSWORD=supersecret123", "PASSWORD_ASSIGN"},
		{"api_key assign", `api_key: "1234567890abcdefghijABCDEF"`, "GENERIC_API_KEY"},
		{"GCP SA private", `{"private_key": "-----BEGIN PRIVATE KEY-----...`, "GCP_SA_PRIVATE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			matches := d.Scan(tc.text)
			if len(matches) == 0 {
				t.Fatalf("expected match for %s, got 0", tc.pattern)
			}
			found := false
			for _, m := range matches {
				if m.Pattern == tc.pattern {
					found = true
					break
				}
			}
			if !found {
				names := make([]string, 0, len(matches))
				for _, m := range matches {
					names = append(names, m.Pattern)
				}
				t.Fatalf("expected pattern %s in matches, got %v", tc.pattern, names)
			}
		})
	}
}

func TestLeakDetector_NoFalsePositivesOnTypicalProse(t *testing.T) {
	d := NewLeakDetector()
	cases := []string{
		"El bug fue resuelto cambiando el orden de los joins en la query.",
		"Se decidió usar JWT como mecanismo de auth para la API v2.",
		"Hay que leer documentación sobre AWS lambdas y configurar el SDK.",
		"Reunión: el cliente quiere ver el dashboard antes del viernes.",
		"Implementación con TypeScript + React + Vite + Tailwind.",
		"file: cmd/aria-core/main.go modificado en commit abc123def.",
	}
	for _, txt := range cases {
		matches := d.Scan(txt)
		if len(matches) > 0 {
			names := make([]string, 0, len(matches))
			for _, m := range matches {
				names = append(names, m.Pattern+":"+m.Match)
			}
			t.Errorf("false positive in %q: %s", txt, strings.Join(names, ","))
		}
	}
}

func TestLeakDetector_DedupesIdenticalMatches(t *testing.T) {
	d := NewLeakDetector()
	txt := "key1=AKIAIOSFODNN7EXAMPLE key2=AKIAIOSFODNN7EXAMPLE"
	matches := d.Scan(txt)
	awsCount := 0
	for _, m := range matches {
		if m.Pattern == "AWS_AK" {
			awsCount++
		}
	}
	if awsCount != 1 {
		t.Errorf("expected dedupe of identical AWS_AK match, got %d", awsCount)
	}
}

func TestFormatLeakError(t *testing.T) {
	matches := []LeakMatch{
		{Pattern: "AWS_AK", Match: "AKIA..."},
		{Pattern: "AWS_AK", Match: "AKIA..."},
		{Pattern: "GH_PAT", Match: "ghp_..."},
	}
	err := FormatLeakError(matches)
	if err == nil {
		t.Fatal("expected error from FormatLeakError")
	}
	msg := err.Error()
	if !strings.Contains(msg, "AWS_AK") || !strings.Contains(msg, "GH_PAT") {
		t.Errorf("error should list both patterns, got: %s", msg)
	}
	if strings.Count(msg, "AWS_AK") != 1 {
		t.Errorf("error should dedupe pattern names, got: %s", msg)
	}
}

func TestFormatLeakError_NoMatches(t *testing.T) {
	if err := FormatLeakError(nil); err != nil {
		t.Errorf("expected nil for empty matches, got %v", err)
	}
}
