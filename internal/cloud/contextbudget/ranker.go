package contextbudget

import (
	"math"
	"sort"
	"strings"
)

// RankStrategy selecciona la heurística de re-ranking aplicada antes del
// budgeting. Los pesos exactos del default ('canon-first') se documentan en el
// AGENTS.md del repo.
type RankStrategy string

const (
	StrategyCanonFirst    RankStrategy = "canon-first"
	StrategyRecentFirst   RankStrategy = "recent-first"
	StrategyEffectiveness RankStrategy = "effectiveness"
)

// QueryContext expone los hints que el ranker usa para boostear scope match.
type QueryContext struct {
	Project string
	Scope   string
	Query   string
}

// === Observations ===

// ScoreObservation aplica los pesos:
//
//	score = 0.4 * fts + 0.3 * canon + 0.2 * recencyDecay + 0.1 * scopeMatch
func ScoreObservation(o Observation, q QueryContext, strat RankStrategy) float64 {
	switch strat {
	case StrategyRecentFirst:
		return scoreRecentFirstObs(o)
	case StrategyEffectiveness:
		// observations no tienen telemetry de skill effectiveness directa; cae
		// al canon-first que ya privilegia material curado.
		return scoreCanonFirstObs(o, q)
	default:
		return scoreCanonFirstObs(o, q)
	}
}

func scoreCanonFirstObs(o Observation, q QueryContext) float64 {
	canon := 0.0
	if o.Canon {
		canon = 1.0
	}
	recency := math.Exp(-o.CreatedAtDays / 30.0)
	scope := scopeMatchScore(o.Scope, q.Scope)
	return 0.4*clamp01(o.FTSRank) + 0.3*canon + 0.2*recency + 0.1*scope
}

func scoreRecentFirstObs(o Observation) float64 {
	// pondera más fuerte la recencia y conserva canon como tiebreaker.
	canon := 0.0
	if o.Canon {
		canon = 1.0
	}
	recency := math.Exp(-o.CreatedAtDays / 14.0) // half-life más corto
	return 0.5*recency + 0.3*clamp01(o.FTSRank) + 0.2*canon
}

func scopeMatchScore(obsScope, queryScope string) float64 {
	a := strings.TrimSpace(strings.ToLower(obsScope))
	b := strings.TrimSpace(strings.ToLower(queryScope))
	if b == "" {
		// si la query no especifica scope, todo lo personal/project pesa por
		// igual y el canon decide.
		return 0.5
	}
	if a == b {
		return 1.0
	}
	if a == "team" || a == "global" {
		return 0.5
	}
	return 0.0
}

// === Skills ===

// ScoreSkill:
//
//	score = 0.5 * ftsRank + 0.3 * effectiveness + 0.2 * recency
func ScoreSkill(s Skill, strat RankStrategy) float64 {
	recency := math.Exp(-s.UpdatedAtDays / 60.0)
	switch strat {
	case StrategyEffectiveness:
		// peso máximo en effectiveness.
		return 0.3*clamp01(s.FTSRank) + 0.5*clamp01(s.Effectiveness) + 0.2*recency
	case StrategyRecentFirst:
		return 0.3*clamp01(s.FTSRank) + 0.2*clamp01(s.Effectiveness) + 0.5*recency
	default:
		return 0.5*clamp01(s.FTSRank) + 0.3*clamp01(s.Effectiveness) + 0.2*recency
	}
}

func clamp01(v float64) float64 {
	if math.IsNaN(v) {
		return 0
	}
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// === Budget selection ===

// ScoredItem es la unidad de selección genérica para SelectWithinBudget.
type ScoredItem struct {
	Key    string  // identificador para tracking/diagnóstico
	Score  float64 // mayor = mejor
	Tokens int     // costo en tokens estimado
	Ref    any     // referencia al objeto original (Observation, Skill, …)
}

// SelectWithinBudget ordena por Score desc y retorna el subset que cabe en
// budget tokens, junto con el conteo de items truncados y el total tokens
// efectivamente consumido. Greedy: una vez que un item NO cabe, aún se intenta
// meter items más chicos (better-fit suave).
func SelectWithinBudget(items []ScoredItem, budget int) (selected []ScoredItem, truncated int, used int) {
	if len(items) == 0 {
		return nil, 0, 0
	}
	// orden estable por score desc; tiebreak por menor costo de tokens (compacto
	// gana en caso de empate, deja más cupo).
	sorted := make([]ScoredItem, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Score != sorted[j].Score {
			return sorted[i].Score > sorted[j].Score
		}
		return sorted[i].Tokens < sorted[j].Tokens
	})
	selected = make([]ScoredItem, 0, len(sorted))
	if budget <= 0 {
		return selected, len(sorted), 0
	}
	for _, it := range sorted {
		if it.Tokens <= 0 {
			// items que no consumen tokens (raro) entran sin afectar el presupuesto.
			selected = append(selected, it)
			continue
		}
		if used+it.Tokens > budget {
			truncated++
			continue
		}
		selected = append(selected, it)
		used += it.Tokens
	}
	return selected, truncated, used
}
