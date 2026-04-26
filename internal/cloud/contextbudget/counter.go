// Package contextbudget centraliza el accounting de tokens, ranking inteligente,
// auto-context injection y telemetría de skills para el proxy ARIA Core.
//
// Counter — token estimation. Ranker — multi-signal scoring de observations y skills.
// Injector — primer mensaje de sesión con canon+skills+recipes relevantes.
// Telemetry — tracking de uso/efectividad de skills + Wilson lower bound.
package contextbudget

import (
	"strings"
	"sync"
	"unicode"
)

// ClaudeOverheadFactor ajusta el conteo cl100k al overhead típico de tokenizers
// Claude (~10% más). Se aplica en el adapter tiktoken; el heurístico ya lo absorbe.
const ClaudeOverheadFactor = 1.10

// Counter estima cuántos tokens consume un texto u objeto en una ventana de
// contexto Claude. Es una abstracción para poder swappear backends (heurístico
// rápido vs tiktoken oficial cl100k_base + overhead).
type Counter interface {
	Count(text string) int
	CountObservation(o Observation) int
	CountSkill(s Skill) int
	CountRecipe(r Recipe) int
}

// Observation es la versión liviana que necesita el counter/ranker — no acopla
// al modelo de ariamem.Observation para que el package sea reusable.
type Observation struct {
	ID            string
	Title         string
	Subtitle      string
	Narrative     string
	Facts         string
	Concepts      string
	Project       string
	Scope         string
	Canon         bool
	CreatedAtDays float64 // edad en días al momento del ranking
	FTSRank       float64 // rank devuelto por Postgres ts_rank (0..1 normalizado)
}

// Skill es el subset que necesita ranking + budgeting.
type Skill struct {
	ID           string
	Name         string
	Description  string
	Content      string
	Stack        []string
	FTSRank      float64
	Effectiveness float64 // Wilson lower bound (0..1)
	UpdatedAtDays float64
}

// Recipe subset usado para auto-injection.
type Recipe struct {
	ID          string
	TaskPattern string
	StepsJSON   string
	Stack       []string
}

// isCommonPunct retorna true para puntuación natural que cl100k suele incluir
// dentro de la misma palabra (no la cuenta como token separado).
func isCommonPunct(r rune) bool {
	switch r {
	case '.', ',', ';', ':', '!', '?', '\'', '`', '…', '¿', '¡':
		return true
	}
	return false
}

// HeuristicCounter es un counter aproximado que NO depende de ningún tokenizer.
// Útil en tests, en arranque sin internet (tiktoken-go baja BPE on-demand) y
// como fallback. El resultado típico cae dentro de ±15% del cl100k oficial para
// texto en español/inglés mezclado.
//
// Reglas:
//   - palabras ASCII-ish: 1 token cada ~4 caracteres (regla OpenAI clásica)
//   - puntuación, números, símbolos cuentan como tokens propios
//   - se aplica overhead Claude (1.10) ya integrado
type HeuristicCounter struct{}

// NewHeuristicCounter retorna el counter sin estado.
func NewHeuristicCounter() *HeuristicCounter { return &HeuristicCounter{} }

// Count estima tokens. Mínimo 1 si el texto es no-vacío.
func (h *HeuristicCounter) Count(text string) int {
	t := strings.TrimSpace(text)
	if t == "" {
		return 0
	}
	// Estrategia: contar runes "alfanuméricas" agrupadas como palabras y dividir
	// por 4 (≈4 chars/token cl100k para texto natural). La puntuación NO sumada
	// por separado: el divisor 4 ya la absorbe en texto natural. Solo contamos
	// extra los símbolos "ricos" (JSON, código) que tiktoken sí desdobla.
	var alphaRunes, symbolRuns int
	prevWasSymbol := false
	for _, r := range t {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			alphaRunes++
			prevWasSymbol = false
		case unicode.IsSpace(r):
			prevWasSymbol = false
		case isCommonPunct(r):
			// puntuación natural (.,;:) no se cuenta extra
			prevWasSymbol = false
		default:
			// símbolos de código (), {}, [], ", =, +, -, /, etc.
			if !prevWasSymbol {
				symbolRuns++
			}
			prevWasSymbol = true
		}
	}
	// 4 chars ≈ 1 token; redondeo hacia arriba.
	tokens := (alphaRunes + 3) / 4
	tokens += symbolRuns
	// Aplicar overhead Claude (~10% más que cl100k).
	tokens = int(float64(tokens)*ClaudeOverheadFactor + 0.5)
	if tokens < 1 {
		tokens = 1
	}
	return tokens
}

// CountObservation suma los campos visibles que terminan en el contexto del LLM.
func (h *HeuristicCounter) CountObservation(o Observation) int {
	// Estructura típica que el ranker termina escribiendo: title + subtitle +
	// narrative + facts. concepts/files quedan opcionales fuera del injector.
	return h.Count(o.Title) +
		h.Count(o.Subtitle) +
		h.Count(o.Narrative) +
		h.Count(o.Facts) +
		h.Count(o.Concepts)
}

// CountSkill cuenta el "encabezado del skill" + content. El content suele ser
// la mayor parte del peso.
func (h *HeuristicCounter) CountSkill(s Skill) int {
	return h.Count(s.Name) + h.Count(s.Description) + h.Count(s.Content)
}

// CountRecipe cuenta task pattern + JSON steps (bytes).
func (h *HeuristicCounter) CountRecipe(r Recipe) int {
	return h.Count(r.TaskPattern) + h.Count(r.StepsJSON)
}

// === TiktokenCounter — wrapper opcional sobre github.com/pkoukk/tiktoken-go ===

// TiktokenCounter usa cl100k_base (encoding por defecto de GPT-4 / similar al
// que estima Claude bien) y multiplica por ClaudeOverheadFactor. Se materializa
// vía NewTiktokenCounter; si la inicialización falla (típicamente porque no hay
// red para bajar el BPE), retorna un HeuristicCounter como fallback con error.
type TiktokenCounter struct {
	encode func(text string) int
}

// tiktokenLoader es indirecto para no acoplar el package a la inicialización
// pesada al import-time. NewTiktokenCounter inyecta el encoder real.
var (
	tiktokenLoaderMu sync.Mutex
	tiktokenLoader   func() (func(string) int, error)
)

// SetTiktokenLoader permite que el binario principal registre el loader real
// (con tiktoken-go) sin que este package deba importarlo directamente — así
// los tests no requieren red para resolver el BPE.
func SetTiktokenLoader(loader func() (func(string) int, error)) {
	tiktokenLoaderMu.Lock()
	tiktokenLoader = loader
	tiktokenLoaderMu.Unlock()
}

// NewTiktokenCounter construye un counter cl100k+overhead. Si no hay loader
// registrado o si la inicialización falla, retorna (HeuristicCounter, error).
// El caller puede ignorar el error y usar igualmente el counter retornado.
func NewTiktokenCounter() (Counter, error) {
	tiktokenLoaderMu.Lock()
	loader := tiktokenLoader
	tiktokenLoaderMu.Unlock()
	if loader == nil {
		return NewHeuristicCounter(), errTiktokenUnavailable
	}
	enc, err := loader()
	if err != nil {
		return NewHeuristicCounter(), err
	}
	return &TiktokenCounter{encode: enc}, nil
}

// Count delega en el encoder cl100k y aplica overhead.
func (t *TiktokenCounter) Count(text string) int {
	if t.encode == nil {
		return NewHeuristicCounter().Count(text)
	}
	if strings.TrimSpace(text) == "" {
		return 0
	}
	raw := t.encode(text)
	return int(float64(raw) * ClaudeOverheadFactor)
}

// CountObservation idem heurístico.
func (t *TiktokenCounter) CountObservation(o Observation) int {
	return t.Count(o.Title) + t.Count(o.Subtitle) + t.Count(o.Narrative) +
		t.Count(o.Facts) + t.Count(o.Concepts)
}

func (t *TiktokenCounter) CountSkill(s Skill) int {
	return t.Count(s.Name) + t.Count(s.Description) + t.Count(s.Content)
}

func (t *TiktokenCounter) CountRecipe(r Recipe) int {
	return t.Count(r.TaskPattern) + t.Count(r.StepsJSON)
}

// errTiktokenUnavailable marca cuando no se registró el loader (no es fatal —
// el caller recibe el HeuristicCounter de fallback).
var errTiktokenUnavailable = newConstErr("contextbudget: tiktoken loader not registered, using heuristic")

type constErr string

func newConstErr(s string) error { return constErr(s) }
func (c constErr) Error() string { return string(c) }
