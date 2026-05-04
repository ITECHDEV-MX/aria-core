package cotizador

import (
	"strings"
	"testing"
)

func TestValidOutcome(t *testing.T) {
	for _, ok := range []string{OutcomeWon, OutcomeLost, OutcomeExpired} {
		if !ValidOutcome(ok) {
			t.Errorf("expected %q to be valid", ok)
		}
	}
	for _, bad := range []string{"", "draft", "approved", "won-maybe"} {
		if ValidOutcome(bad) {
			t.Errorf("expected %q to be invalid", bad)
		}
	}
}

func TestStatusToOutcome(t *testing.T) {
	cases := map[string]string{
		QuoteStatusApproved: OutcomeWon,
		QuoteStatusRejected: OutcomeLost,
		QuoteStatusExpired:  OutcomeExpired,
		"draft":             "",
		"sent":              "",
		"":                  "",
	}
	for status, want := range cases {
		got := statusToOutcome(status)
		if got != want {
			t.Errorf("statusToOutcome(%q) = %q, want %q", status, got, want)
		}
	}
}

func TestIsTerminalStatus(t *testing.T) {
	terminal := []string{QuoteStatusApproved, QuoteStatusRejected, QuoteStatusExpired}
	for _, s := range terminal {
		if !IsTerminalStatus(s) {
			t.Errorf("%q should be terminal", s)
		}
	}
	for _, s := range []string{"draft", "sent", "negotiating"} {
		if IsTerminalStatus(s) {
			t.Errorf("%q should NOT be terminal", s)
		}
	}
}

func TestProjectNameFromLead_PrefersCompany(t *testing.T) {
	got := projectNameFromLead("Juan Pérez", "ITech Pymes SA", "q-123")
	if !strings.HasPrefix(got, "itech-pymes-sa") {
		t.Errorf("expected company-derived prefix, got %q", got)
	}
}

func TestProjectNameFromLead_FallsBackToLead(t *testing.T) {
	got := projectNameFromLead("Juan Pérez", "", "q-456")
	if !strings.HasPrefix(got, "juan-p") {
		t.Errorf("expected lead-derived prefix, got %q", got)
	}
}

func TestProjectNameFromLead_FinalFallback(t *testing.T) {
	got := projectNameFromLead("", "", "q-789")
	if !strings.HasPrefix(got, "lead") {
		t.Errorf("expected 'lead' prefix when both name+company empty, got %q", got)
	}
}

func TestProjectNameFromLead_TruncatesAt32(t *testing.T) {
	long := strings.Repeat("a", 100)
	got := projectNameFromLead("", long, "q-x")
	if len(got) > 41 { // 32 cap + "-" + 8 char quoteID suffix
		t.Errorf("expected truncation to <=32, got len %d (%q)", len(got), got)
	}
}

func TestProjectNameFromLead_StripsSpecials(t *testing.T) {
	got := projectNameFromLead("", "ITech!@# Pymes SA", "q-1")
	for _, r := range got {
		// Allow lowercase letters, digits, hyphen
		if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-') {
			t.Errorf("disallowed char %q in result %q", r, got)
		}
	}
}
