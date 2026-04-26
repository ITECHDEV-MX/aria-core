package contextbudget

import (
	"strings"
	"testing"
	"time"
)

func TestBuildSessionStartContext_RespectsBudget(t *testing.T) {
	// 5 obs, cada uno ~50 tokens en heurístico → 250. Forzamos budget muy chico
	// para verificar truncado.
	obs := []Observation{}
	for i := 0; i < 5; i++ {
		obs = append(obs, Observation{
			ID:        "o" + string(rune('a'+i)),
			Title:     "Decision " + string(rune('A'+i)),
			Subtitle:  "subtitle short",
			Narrative: strings.Repeat("Some narrative content for context. ", 5),
			Canon:     true,
			FTSRank:   0.5,
		})
	}
	srcs := InjectorSources{
		TopCanonObservations: func(p string, l int) ([]Observation, error) {
			return obs, nil
		},
	}
	out := BuildSessionStartContext(SessionStartParams{
		Project:     "test",
		Goal:        "fix auth",
		TokenBudget: 80, // muy chico
	}, NewHeuristicCounter(), srcs)
	if out.TruncatedItems == 0 {
		t.Errorf("expected items to be truncated under tight budget; tokens used=%d", out.TokensUsed)
	}
	if out.TokensUsed > 80 {
		t.Errorf("budget violated: used %d > 80", out.TokensUsed)
	}
	if !strings.Contains(out.Markdown, "Contexto de sesión") {
		t.Errorf("markdown missing header; got: %s", out.Markdown)
	}
}

func TestBuildSessionStartContext_OrdersConsistently(t *testing.T) {
	obs := []Observation{
		{ID: "low", Title: "low", Canon: false, FTSRank: 0.1, CreatedAtDays: 100},
		{ID: "canon", Title: "canon item", Canon: true, FTSRank: 0.1, CreatedAtDays: 100},
		{ID: "fresh", Title: "fresh item", Canon: false, FTSRank: 0.1, CreatedAtDays: 0},
	}
	srcs := InjectorSources{
		TopCanonObservations: func(p string, l int) ([]Observation, error) { return obs, nil },
	}
	out1 := BuildSessionStartContext(SessionStartParams{Project: "p", Goal: "g"}, nil, srcs)
	out2 := BuildSessionStartContext(SessionStartParams{Project: "p", Goal: "g"}, nil, srcs)
	if len(out1.CanonObs) != len(out2.CanonObs) {
		t.Fatalf("non-deterministic length: %d vs %d", len(out1.CanonObs), len(out2.CanonObs))
	}
	for i := range out1.CanonObs {
		if out1.CanonObs[i].ID != out2.CanonObs[i].ID {
			t.Errorf("non-deterministic order at %d: %s vs %s",
				i, out1.CanonObs[i].ID, out2.CanonObs[i].ID)
		}
	}
}

func TestBuildSessionStartContext_NoSourcesReturnsBare(t *testing.T) {
	out := BuildSessionStartContext(SessionStartParams{Project: "x", Goal: "y"}, nil, InjectorSources{})
	if !strings.Contains(out.Markdown, "Contexto de sesión") {
		t.Errorf("expected header even with no sources; got: %s", out.Markdown)
	}
	if len(out.CanonObs) != 0 || len(out.SuggestedSkills) != 0 || len(out.MatchingRecipes) != 0 {
		t.Errorf("expected empty buckets when sources are nil")
	}
}

func TestBuildSessionStartContext_AllSections(t *testing.T) {
	srcs := InjectorSources{
		TopCanonObservations: func(p string, l int) ([]Observation, error) {
			return []Observation{{ID: "1", Title: "Canon decision", Narrative: "narr", Canon: true}}, nil
		},
		SkillsForGoal: func(g string, st []string, l int) ([]Skill, error) {
			return []Skill{{ID: "s1", Name: "skill-1", Description: "desc", Content: "content"}}, nil
		},
		RecipesForStack: func(td string, st []string, l int) ([]Recipe, error) {
			return []Recipe{{ID: "r1", TaskPattern: "How to deploy"}}, nil
		},
		OpenSessionsForDev: func(uid, sid string, l int) ([]OpenSessionRef, error) {
			return []OpenSessionRef{{ID: "ses_1", Project: "p", Goal: "fix bug", StartedAgo: "hace 2 h"}}, nil
		},
	}
	out := BuildSessionStartContext(SessionStartParams{
		Project:      "p",
		Goal:         "shipping",
		Stack:        []string{"go", "postgres"},
		DeveloperUID: "00000000-0000-0000-0000-000000000001",
		TokenBudget:  4000,
	}, nil, srcs)
	for _, want := range []string{
		"Decisiones canon", "Skills sugeridos", "Recipes que aplican", "Sesiones abiertas",
		"Canon decision", "skill-1", "How to deploy", "ses_1",
	} {
		if !strings.Contains(out.Markdown, want) {
			t.Errorf("markdown missing section/content %q\nfull: %s", want, out.Markdown)
		}
	}
}

func TestHumanAgo(t *testing.T) {
	now := time.Date(2026, 4, 26, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		offset time.Duration
		want   string
	}{
		{30 * time.Second, "hace segundos"},
		{5 * time.Minute, "hace 5 min"},
		{3 * time.Hour, "hace 3 h"},
		{72 * time.Hour, "hace 3 días"},
	}
	for _, tc := range cases {
		got := HumanAgo(now.Add(-tc.offset), now)
		if got != tc.want {
			t.Errorf("HumanAgo(%v): want %q, got %q", tc.offset, tc.want, got)
		}
	}
}
