package roi

import (
	"math"
	"os"
	"testing"
)

func TestSavedMinutesFromPcts_AllZeros(t *testing.T) {
	got := SavedMinutesFromPcts(0, 0, 0, 0)
	if got != 0 {
		t.Fatalf("expected 0 saved minutes when all pcts are zero, got %v", got)
	}
}

func TestSavedMinutesFromPcts_AllPerfect(t *testing.T) {
	// rdr=cwr=svr=1.0 (100%) y dttDelta=30 minutes saved.
	got := SavedMinutesFromPcts(1.0, 1.0, 1.0, 30.0)
	want := BaselineInvestigationMin + BaselineCredentialsMin + BaselineSkillAppliedMin + 30.0
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected %v saved minutes for perfect scores, got %v", want, got)
	}
}

func TestSavedMinutesFromPcts_NegativeDeltaClamps(t *testing.T) {
	// Si DTT actual > baseline (peor que antes), delta es negativo y debe clampear a 0.
	got := SavedMinutesFromPcts(0, 0, 0, -50)
	if got != 0 {
		t.Fatalf("expected 0 when only contribution is negative DTT delta, got %v", got)
	}
}

func TestSavedMinutesFromPcts_PartialPcts(t *testing.T) {
	// 50% RDR + 80% CWR + 100% SVR + 0 DTT delta.
	got := SavedMinutesFromPcts(0.5, 0.8, 1.0, 0)
	want := 0.5*BaselineInvestigationMin + 0.8*BaselineCredentialsMin + 1.0*BaselineSkillAppliedMin
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected %v, got %v", want, got)
	}
}

func TestDevCostMXNPerMin_Default(t *testing.T) {
	// Limpia env para forzar default.
	prev := os.Getenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR")
	defer os.Setenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR", prev)
	os.Unsetenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR")
	got := DevCostMXNPerMin()
	want := DefaultMXNPerHour / 60.0
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected default %v MXN/min, got %v", want, got)
	}
}

func TestDevCostMXNPerMin_Custom(t *testing.T) {
	prev := os.Getenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR")
	defer os.Setenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR", prev)
	os.Setenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR", "600")
	got := DevCostMXNPerMin()
	want := 10.0 // 600/60
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected %v MXN/min for 600/h, got %v", want, got)
	}
}

func TestDevCostMXNPerMin_InvalidFallsBackToDefault(t *testing.T) {
	prev := os.Getenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR")
	defer os.Setenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR", prev)
	os.Setenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR", "not-a-number")
	got := DevCostMXNPerMin()
	want := DefaultMXNPerHour / 60.0
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected default fallback %v, got %v", want, got)
	}
}

func TestDevCostMXNPerMin_ZeroFallsBackToDefault(t *testing.T) {
	prev := os.Getenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR")
	defer os.Setenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR", prev)
	os.Setenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR", "0")
	got := DevCostMXNPerMin()
	want := DefaultMXNPerHour / 60.0
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected default fallback for zero, got %v", got)
	}
}

func TestPillarsBreakdown_SumsCorrectly(t *testing.T) {
	// Reproduce el cálculo manual de breakdown por pillar.
	rdr := 0.6
	cwr := 0.9
	svr := 0.5
	dttDelta := 20.0
	pillarContextDev := svr*BaselineSkillAppliedMin + dttDelta
	pillarClaudeSkills := rdr * BaselineInvestigationMin
	pillarVaultRedactor := cwr * BaselineCredentialsMin
	wantTotal := pillarContextDev + pillarClaudeSkills + pillarVaultRedactor

	got := SavedMinutesFromPcts(rdr, cwr, svr, dttDelta)
	if math.Abs(got-wantTotal) > 1e-6 {
		t.Fatalf("pillar sum mismatch: pillars=%v, formula=%v", wantTotal, got)
	}
}

func TestSavingsView_WindowDays(t *testing.T) {
	tests := []struct {
		name     string
		hours    float64
		expected int
	}{
		{"7-day window", 7 * 24, 7},
		{"24-hour window", 24, 1},
		{"sub-day rounds to 1", 12, 1},
		{"30 days", 30 * 24, 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			now := timeNow()
			got := windowDays(now, now.Add(durationHours(tc.hours)))
			if got != tc.expected {
				t.Fatalf("windowDays(%v hours) = %v, want %v", tc.hours, got, tc.expected)
			}
		})
	}
}
