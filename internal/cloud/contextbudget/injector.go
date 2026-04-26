package contextbudget

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// SessionStartParams describe el dev/proyecto/sesion para construir el primer
// mensaje. Goal es la frase libre del developer ("estoy debuggeando auth").
type SessionStartParams struct {
	Project      string
	Goal         string
	Stack        []string
	DeveloperUID string
	SessionID    string
	// TokenBudget total para el primer mensaje. Default 8000 si <=0.
	TokenBudget int
}

// SessionStartContext es la carga retornada — el caller decide si lo serializa
// como string para el campo `auto_context` o como objeto estructurado para
// dashboard.
type SessionStartContext struct {
	Markdown        string
	TokensUsed      int
	TruncatedItems  int
	CanonObs        []Observation
	SuggestedSkills []Skill
	MatchingRecipes []Recipe
	OpenSessions    []OpenSessionRef
}

// OpenSessionRef se inyecta para que el dev pueda retomar trabajo.
type OpenSessionRef struct {
	ID         string
	Project    string
	Goal       string
	StartedAgo string // ej "hace 2 horas"
}

// InjectorSources es el contrato de las queries que el injector necesita; se
// inyectan funciones para que el package no dependa de ariamem y los tests
// puedan mockear sin DB.
type InjectorSources struct {
	TopCanonObservations func(project string, limit int) ([]Observation, error)
	SkillsForGoal        func(goal string, stack []string, limit int) ([]Skill, error)
	RecipesForStack      func(taskDescription string, stack []string, limit int) ([]Recipe, error)
	OpenSessionsForDev   func(devUID string, excludeSessionID string, limit int) ([]OpenSessionRef, error)
}

// Defaults para la composición.
const (
	DefaultSessionTokenBudget = 8000
	DefaultCanonLimit         = 5
	DefaultSkillLimit         = 3
	DefaultRecipeLimit        = 2
	DefaultOpenSessionLimit   = 3
)

// BuildSessionStartContext arma el primer mensaje markdown estructurado.
// Si counter es nil usa HeuristicCounter. Si srcs es nil retorna placeholder.
func BuildSessionStartContext(p SessionStartParams, counter Counter, srcs InjectorSources) SessionStartContext {
	if counter == nil {
		counter = NewHeuristicCounter()
	}
	budget := p.TokenBudget
	if budget <= 0 {
		budget = DefaultSessionTokenBudget
	}

	// 1. Recolectar candidatos sin truncar (cada source tira top N propio).
	var canonObs []Observation
	var skills []Skill
	var recipes []Recipe
	var openSess []OpenSessionRef
	if srcs.TopCanonObservations != nil {
		obs, err := srcs.TopCanonObservations(p.Project, DefaultCanonLimit)
		if err == nil {
			canonObs = obs
		}
	}
	if srcs.SkillsForGoal != nil {
		sks, err := srcs.SkillsForGoal(p.Goal, p.Stack, DefaultSkillLimit)
		if err == nil {
			skills = sks
		}
	}
	if srcs.RecipesForStack != nil {
		rcs, err := srcs.RecipesForStack(p.Goal, p.Stack, DefaultRecipeLimit)
		if err == nil {
			recipes = rcs
		}
	}
	if srcs.OpenSessionsForDev != nil {
		os, err := srcs.OpenSessionsForDev(p.DeveloperUID, p.SessionID, DefaultOpenSessionLimit)
		if err == nil {
			openSess = os
		}
	}

	// 2. Score + token estimation por sección. Reservamos cuotas suaves para
	//    cada bloque pero compartimos un budget global (greedy por score).
	items := make([]ScoredItem, 0, len(canonObs)+len(skills)+len(recipes)+len(openSess))
	q := QueryContext{Project: p.Project, Scope: "project", Query: p.Goal}
	for i, o := range canonObs {
		// canon listing: score por canon-first, además boost por orden de input.
		// El injector NO quiere truncar canon agresivamente: agrega un floor.
		s := ScoreObservation(o, q, StrategyCanonFirst) + (1.0 / float64(i+1))*0.05
		items = append(items, ScoredItem{
			Key:    "obs:" + o.ID,
			Score:  s,
			Tokens: counter.CountObservation(o),
			Ref:    o,
		})
	}
	for _, sk := range skills {
		items = append(items, ScoredItem{
			Key:    "skill:" + sk.ID,
			Score:  ScoreSkill(sk, StrategyCanonFirst) + 0.05, // skills levemente prioritarios
			Tokens: counter.CountSkill(sk),
			Ref:    sk,
		})
	}
	for i, r := range recipes {
		items = append(items, ScoredItem{
			Key:    "recipe:" + r.ID,
			Score:  0.6 + (1.0/float64(i+1))*0.1, // recipes tienen score fijo intermedio
			Tokens: counter.CountRecipe(r),
			Ref:    r,
		})
	}
	for _, sess := range openSess {
		// open sessions cuestan ~50 tokens; alta prioridad: el dev quiere saber
		// que tiene cosas abiertas.
		items = append(items, ScoredItem{
			Key:    "open_session:" + sess.ID,
			Score:  0.95,
			Tokens: counter.Count(sess.Project + " " + sess.Goal),
			Ref:    sess,
		})
	}

	selected, truncated, used := SelectWithinBudget(items, budget)

	// 3. Recompose: separamos por tipo para mantener orden estético.
	finalCanon := []Observation{}
	finalSkills := []Skill{}
	finalRecipes := []Recipe{}
	finalSess := []OpenSessionRef{}
	for _, it := range selected {
		switch v := it.Ref.(type) {
		case Observation:
			finalCanon = append(finalCanon, v)
		case Skill:
			finalSkills = append(finalSkills, v)
		case Recipe:
			finalRecipes = append(finalRecipes, v)
		case OpenSessionRef:
			finalSess = append(finalSess, v)
		}
	}
	// Conservar orden estable por Score desc dentro de cada bucket.
	sort.SliceStable(finalCanon, func(i, j int) bool {
		return ScoreObservation(finalCanon[i], q, StrategyCanonFirst) >
			ScoreObservation(finalCanon[j], q, StrategyCanonFirst)
	})
	sort.SliceStable(finalSkills, func(i, j int) bool {
		return ScoreSkill(finalSkills[i], StrategyCanonFirst) > ScoreSkill(finalSkills[j], StrategyCanonFirst)
	})

	md := renderMarkdown(p, finalCanon, finalSkills, finalRecipes, finalSess, truncated)

	return SessionStartContext{
		Markdown:        md,
		TokensUsed:      used,
		TruncatedItems:  truncated,
		CanonObs:        finalCanon,
		SuggestedSkills: finalSkills,
		MatchingRecipes: finalRecipes,
		OpenSessions:    finalSess,
	}
}

// renderMarkdown produce la salida en formato listo para inyectar al chat.
func renderMarkdown(p SessionStartParams, obs []Observation, skills []Skill, recipes []Recipe, sessions []OpenSessionRef, truncated int) string {
	var b strings.Builder
	b.WriteString("# Contexto de sesión ARIA Core\n\n")
	if proj := strings.TrimSpace(p.Project); proj != "" {
		fmt.Fprintf(&b, "Proyecto: **%s**\n", proj)
	}
	if goal := strings.TrimSpace(p.Goal); goal != "" {
		fmt.Fprintf(&b, "Goal: %s\n", goal)
	}
	if len(p.Stack) > 0 {
		fmt.Fprintf(&b, "Stack: %s\n", strings.Join(p.Stack, ", "))
	}
	b.WriteString("\n")

	if len(obs) > 0 {
		b.WriteString("## Decisiones canon del proyecto (top 5)\n")
		for _, o := range obs {
			line := "- **" + safeOneLine(o.Title) + "**"
			if sub := safeOneLine(o.Subtitle); sub != "" {
				line += " — " + sub
			}
			b.WriteString(line + "\n")
			if narr := snippet(o.Narrative, 200); narr != "" {
				b.WriteString("  > " + narr + "\n")
			}
		}
		b.WriteString("\n")
	}

	if len(skills) > 0 {
		goalLabel := strings.TrimSpace(p.Goal)
		if goalLabel == "" {
			goalLabel = "tu tarea actual"
		}
		fmt.Fprintf(&b, "## Skills sugeridos para tu goal \"%s\"\n", goalLabel)
		for _, s := range skills {
			fmt.Fprintf(&b, "- **%s**", safeOneLine(s.Name))
			if d := safeOneLine(s.Description); d != "" {
				fmt.Fprintf(&b, " (%s)", d)
			}
			b.WriteString("\n")
			if cnt := snippet(s.Content, 160); cnt != "" {
				b.WriteString("  Cuándo: " + cnt + "\n")
			}
		}
		b.WriteString("\n")
	}

	if len(recipes) > 0 {
		b.WriteString("## Recipes que aplican\n")
		for _, r := range recipes {
			fmt.Fprintf(&b, "- %s\n", safeOneLine(r.TaskPattern))
		}
		b.WriteString("\n")
	}

	if len(sessions) > 0 {
		b.WriteString("## Sesiones abiertas\n")
		for _, s := range sessions {
			line := "- " + s.ID
			if s.Project != "" {
				line += " en proyecto " + s.Project
			}
			if s.StartedAgo != "" {
				line += ", iniciada " + s.StartedAgo
			}
			if g := safeOneLine(s.Goal); g != "" {
				line += " — " + g
			}
			b.WriteString(line + "\n")
		}
		b.WriteString("\n")
	}

	if truncated > 0 {
		fmt.Fprintf(&b, "_(%d items omitidos por límite de tokens)_\n", truncated)
	}
	return b.String()
}

func safeOneLine(s string) string {
	t := strings.TrimSpace(s)
	if t == "" {
		return ""
	}
	t = strings.ReplaceAll(t, "\r", " ")
	t = strings.ReplaceAll(t, "\n", " ")
	for strings.Contains(t, "  ") {
		t = strings.ReplaceAll(t, "  ", " ")
	}
	return t
}

func snippet(s string, max int) string {
	t := safeOneLine(s)
	if t == "" {
		return ""
	}
	runes := []rune(t)
	if len(runes) <= max {
		return t
	}
	if max <= 1 {
		return string(runes[:1])
	}
	return string(runes[:max-1]) + "…"
}

// HumanAgo retorna "hace X" para timestamps recientes (helper utilitario para
// quien arme OpenSessionRef.StartedAgo).
func HumanAgo(t time.Time, now time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "hace segundos"
	case d < time.Hour:
		return fmt.Sprintf("hace %d min", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("hace %d h", int(d.Hours()))
	default:
		return fmt.Sprintf("hace %d días", int(d.Hours()/24))
	}
}
