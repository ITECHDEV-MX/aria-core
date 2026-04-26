package roi

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"
)

// Baselines (declarativos, configurables sólo por code-change).
//
// Estos valores están alineados con el pitch de "30% ahorro tiempo+costo":
// representan minutos por dia perdidos antes de ARIA Core en cada pillar.
// Multiplicar por el % de éxito de cada métrica da el ahorro neto.
const (
	BaselineInvestigationMin = 25.0 // RDR-related: redescubrir lo que ya está en memoria
	BaselineCredentialsMin   = 10.0 // CWR-related: copiar/pegar credenciales sin gating
	BaselineSkillAppliedMin  = 5.0  // SVR-related: hacer que un skill termine en ahorro real
	BaselineDeployMin        = 60.0 // DTT-related: deploy lento sin recipes
	WorkdayMinutes           = 480.0
	DefaultMXNPerHour        = 250.0
)

// SavedMinutesFromPcts es la fórmula consolidada del documento:
//
//	saved = RDR_pct * 25 + CWR_pct * 10 + SVR_pct * 5 + (DTT_baseline - DTT_actual)
//
// Si dttDelta <= 0 (no aplica todavía), se usa 0 como contribución.
func SavedMinutesFromPcts(rdr, cwr, svr, dttDelta float64) float64 {
	if dttDelta < 0 {
		dttDelta = 0
	}
	return rdr*BaselineInvestigationMin +
		cwr*BaselineCredentialsMin +
		svr*BaselineSkillAppliedMin +
		dttDelta
}

// SavingsView es el dato consolidado que el dashboard renderiza en la hero card.
type SavingsView struct {
	WindowDays        int
	TotalSavedMinutes float64
	TotalSavedMXN     float64
	WorkdayPctSaved   float64 // saved_min/480 (asume 1 día base)
	ByPillar          map[string]float64
	ByPillarMXN       map[string]float64
	Compared          SavingsCompared

	// Métricas crudas que llevaron al cálculo (para tooltips/debug).
	RDR float64
	CWR float64
	SVR float64
	TTC float64
	DTT float64

	// Configuración usada (debug/transparencia).
	CostMXNPerMin float64
}

// SavingsCompared encapsula la comparación contra el período anterior.
type SavingsCompared struct {
	PreviousMinutes float64
	PreviousMXN     float64
	DeltaMinutes    float64 // current - previous
	DeltaMXN        float64
	DeltaPct        float64 // (current-previous)/previous, 0 si previous==0
}

// CalculateSavings consolida todas las métricas en SavingsView para el rango
// [since, until). Usa baselines fijos + cost/min configurable vía env.
func (m *MetricsStore) CalculateSavings(ctx context.Context, devUID string, since, until time.Time) (*SavingsView, error) {
	if since.After(until) {
		since, until = until, since
	}
	costPerMin := DevCostMXNPerMin()
	rdr, _, err := m.CalcRDR(ctx, devUID, since, until)
	if err != nil {
		return nil, err
	}
	cwr, err := m.CalcCWR(ctx, devUID, since, until)
	if err != nil {
		return nil, err
	}
	svr, err := m.CalcSVR(ctx, devUID, since, until)
	if err != nil {
		return nil, err
	}
	ttc, _, err := m.CalcTTC(ctx, devUID, since, until)
	if err != nil {
		return nil, err
	}
	dtt, err := m.CalcDTT(ctx, devUID, since, until)
	if err != nil {
		return nil, err
	}

	dttDelta := BaselineDeployMin - dtt
	if dtt <= 0 {
		dttDelta = 0
	}

	pillarContextDev := svr*BaselineSkillAppliedMin + dttDelta // pillar 1+2 (skills + deploy automation)
	pillarClaudeSkills := rdr * BaselineInvestigationMin       // pillar 2 (memoria evita redescubrir)
	pillarVaultRedactor := cwr * BaselineCredentialsMin        // pillar 3 (credentials gated)

	totalMin := pillarContextDev + pillarClaudeSkills + pillarVaultRedactor

	view := &SavingsView{
		WindowDays:        windowDays(since, until),
		TotalSavedMinutes: totalMin,
		TotalSavedMXN:     totalMin * costPerMin,
		WorkdayPctSaved:   totalMin / WorkdayMinutes,
		ByPillar: map[string]float64{
			"context_dev":     pillarContextDev,
			"claude_skills":   pillarClaudeSkills,
			"vault_redactor":  pillarVaultRedactor,
		},
		ByPillarMXN: map[string]float64{
			"context_dev":    pillarContextDev * costPerMin,
			"claude_skills":  pillarClaudeSkills * costPerMin,
			"vault_redactor": pillarVaultRedactor * costPerMin,
		},
		RDR:           rdr,
		CWR:           cwr,
		SVR:           svr,
		TTC:           ttc,
		DTT:           dtt,
		CostMXNPerMin: costPerMin,
	}

	// Comparación vs período anterior de igual largo.
	span := until.Sub(since)
	prevUntil := since
	prevSince := since.Add(-span)
	prev, err := m.calculateRawTotalMinutes(ctx, devUID, prevSince, prevUntil)
	if err == nil {
		view.Compared.PreviousMinutes = prev
		view.Compared.PreviousMXN = prev * costPerMin
		view.Compared.DeltaMinutes = totalMin - prev
		view.Compared.DeltaMXN = view.TotalSavedMXN - view.Compared.PreviousMXN
		if prev > 0 {
			view.Compared.DeltaPct = (totalMin - prev) / prev
		}
	}
	return view, nil
}

// calculateRawTotalMinutes hace los mismos calls que CalculateSavings pero
// devuelve sólo el total para evitar re-allocar SavingsView.
func (m *MetricsStore) calculateRawTotalMinutes(ctx context.Context, devUID string, since, until time.Time) (float64, error) {
	rdr, _, err := m.CalcRDR(ctx, devUID, since, until)
	if err != nil {
		return 0, err
	}
	cwr, err := m.CalcCWR(ctx, devUID, since, until)
	if err != nil {
		return 0, err
	}
	svr, err := m.CalcSVR(ctx, devUID, since, until)
	if err != nil {
		return 0, err
	}
	dtt, err := m.CalcDTT(ctx, devUID, since, until)
	if err != nil {
		return 0, err
	}
	dttDelta := BaselineDeployMin - dtt
	if dtt <= 0 {
		dttDelta = 0
	}
	return SavedMinutesFromPcts(rdr, cwr, svr, dttDelta), nil
}

// DevCostMXNPerMin parsea ARIA_CORE_DEV_COST_MXN_PER_HOUR y devuelve costo/min.
// Fallback DefaultMXNPerHour si vacío o inválido.
func DevCostMXNPerMin() float64 {
	raw := strings.TrimSpace(os.Getenv("ARIA_CORE_DEV_COST_MXN_PER_HOUR"))
	if raw == "" {
		return DefaultMXNPerHour / 60.0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 {
		return DefaultMXNPerHour / 60.0
	}
	return v / 60.0
}

func windowDays(since, until time.Time) int {
	d := until.Sub(since).Hours() / 24
	if d < 0 {
		d = -d
	}
	if d < 1 {
		return 1
	}
	return int(d + 0.5)
}
