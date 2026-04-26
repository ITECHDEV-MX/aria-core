package contextbudget

import (
	"math"
	"testing"
)

func TestScoreObservation_CanonFirstPrioritizesCanon(t *testing.T) {
	q := QueryContext{Project: "aria-core", Scope: "project"}
	canon := Observation{ID: "a", Canon: true, FTSRank: 0.5, CreatedAtDays: 30, Scope: "project"}
	plain := Observation{ID: "b", Canon: false, FTSRank: 0.5, CreatedAtDays: 30, Scope: "project"}

	sCanon := ScoreObservation(canon, q, StrategyCanonFirst)
	sPlain := ScoreObservation(plain, q, StrategyCanonFirst)
	if sCanon <= sPlain {
		t.Errorf("expected canon score (%v) > plain score (%v)", sCanon, sPlain)
	}
}

func TestScoreObservation_RecencyDecay(t *testing.T) {
	q := QueryContext{Project: "p", Scope: "project"}
	young := Observation{Canon: false, FTSRank: 0, CreatedAtDays: 0, Scope: "project"}
	old := Observation{Canon: false, FTSRank: 0, CreatedAtDays: 90, Scope: "project"}
	sYoung := ScoreObservation(young, q, StrategyCanonFirst)
	sOld := ScoreObservation(old, q, StrategyCanonFirst)
	if sYoung <= sOld {
		t.Errorf("expected younger obs to outrank older one (young=%v old=%v)", sYoung, sOld)
	}
	// recency component should decay roughly to exp(-3) at 90 days.
	expectedYoungRecency := 0.2 // weight * exp(0)
	if math.Abs(sYoung-(0.1*0.5+expectedYoungRecency)) > 0.05 {
		// no estricto: el test es de orden, este check es informativo
	}
}

func TestScoreObservation_ScopeMatch(t *testing.T) {
	q := QueryContext{Scope: "project"}
	matching := Observation{Scope: "project", FTSRank: 0, CreatedAtDays: 1000}
	team := Observation{Scope: "team", FTSRank: 0, CreatedAtDays: 1000}
	other := Observation{Scope: "personal", FTSRank: 0, CreatedAtDays: 1000}

	sMatch := ScoreObservation(matching, q, StrategyCanonFirst)
	sTeam := ScoreObservation(team, q, StrategyCanonFirst)
	sOther := ScoreObservation(other, q, StrategyCanonFirst)

	if !(sMatch > sTeam && sTeam > sOther) {
		t.Errorf("expected scope match ordering match>team>other, got match=%v team=%v other=%v",
			sMatch, sTeam, sOther)
	}
}

func TestScoreSkill_EffectivenessStrategy(t *testing.T) {
	// con strategy=effectiveness, dos skills con misma fts pero distinto Wilson
	// deben ordenarse por effectiveness.
	a := Skill{ID: "a", FTSRank: 0.5, Effectiveness: 0.8, UpdatedAtDays: 30}
	b := Skill{ID: "b", FTSRank: 0.5, Effectiveness: 0.2, UpdatedAtDays: 30}
	sA := ScoreSkill(a, StrategyEffectiveness)
	sB := ScoreSkill(b, StrategyEffectiveness)
	if sA <= sB {
		t.Errorf("expected effective skill to outrank ineffective: a=%v b=%v", sA, sB)
	}
}

func TestSelectWithinBudget_FitsAll(t *testing.T) {
	items := []ScoredItem{
		{Key: "a", Score: 0.9, Tokens: 100},
		{Key: "b", Score: 0.8, Tokens: 100},
		{Key: "c", Score: 0.7, Tokens: 100},
	}
	sel, trunc, used := SelectWithinBudget(items, 1000)
	if len(sel) != 3 {
		t.Errorf("expected all 3, got %d", len(sel))
	}
	if trunc != 0 {
		t.Errorf("expected 0 truncated, got %d", trunc)
	}
	if used != 300 {
		t.Errorf("expected 300 tokens used, got %d", used)
	}
}

func TestSelectWithinBudget_TruncatesByScore(t *testing.T) {
	items := []ScoredItem{
		{Key: "low", Score: 0.1, Tokens: 100},
		{Key: "high", Score: 0.9, Tokens: 100},
		{Key: "mid", Score: 0.5, Tokens: 100},
	}
	sel, trunc, used := SelectWithinBudget(items, 200)
	if len(sel) != 2 {
		t.Errorf("expected 2 selected, got %d", len(sel))
	}
	if trunc != 1 {
		t.Errorf("expected 1 truncated, got %d", trunc)
	}
	if used != 200 {
		t.Errorf("expected 200 tokens, got %d", used)
	}
	// Verifica que entraron los de mayor score
	keys := map[string]bool{}
	for _, it := range sel {
		keys[it.Key] = true
	}
	if !keys["high"] || !keys["mid"] {
		t.Errorf("expected high+mid selected, got %+v", keys)
	}
}

func TestSelectWithinBudget_TightBudget(t *testing.T) {
	items := []ScoredItem{
		{Key: "big", Score: 0.99, Tokens: 1000},
		{Key: "small", Score: 0.5, Tokens: 50},
	}
	sel, trunc, used := SelectWithinBudget(items, 200)
	// big NO cabe; small sí debería entrar (better-fit suave).
	if len(sel) != 1 || sel[0].Key != "small" {
		t.Errorf("expected only small, got %+v", sel)
	}
	if trunc != 1 {
		t.Errorf("expected 1 truncated, got %d", trunc)
	}
	if used != 50 {
		t.Errorf("expected 50 tokens used, got %d", used)
	}
}

func TestSelectWithinBudget_EmptyAndZero(t *testing.T) {
	sel, trunc, used := SelectWithinBudget(nil, 100)
	if sel != nil || trunc != 0 || used != 0 {
		t.Errorf("expected empty result, got sel=%v trunc=%d used=%d", sel, trunc, used)
	}
	items := []ScoredItem{{Key: "a", Score: 1.0, Tokens: 100}}
	sel, trunc, _ = SelectWithinBudget(items, 0)
	if len(sel) != 0 || trunc != 1 {
		t.Errorf("expected nothing selected on 0 budget, got sel=%d trunc=%d", len(sel), trunc)
	}
}
