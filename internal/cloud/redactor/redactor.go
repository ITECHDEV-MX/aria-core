package redactor

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Sensitivity tags drive both storage and egress policy.
type Sensitivity string

const (
	SensitivityPublic       Sensitivity = "public"
	SensitivityInternal     Sensitivity = "internal"
	SensitivityClient       Sensitivity = "client"
	SensitivityConfidential Sensitivity = "confidential"
)

// ScrubMode chooses the replacement strategy.
//
//   - ModeTokens: replace each match with a deterministic [TOKEN-XXXX] (default).
//     Lets Claude correlate references and lets ARIA expand them back later.
//   - ModeRedact: replace each match with the literal string "[REDACTED]".
//     Used for confidential payloads that must never round-trip.
type ScrubMode string

const (
	ModeTokens ScrubMode = "tokens"
	ModeRedact ScrubMode = "redact"
)

// ScrubOptions configures one Scrub() call.
type ScrubOptions struct {
	Mode              ScrubMode
	PreserveStructure bool       // keep '@' and '.' for emails when redacting
	ClientContext     *uuid.UUID // optional: client-scoped aliases
}

// Redaction is a per-pattern summary of what was replaced.
type Redaction struct {
	Type         string   `json:"type"`
	Count        int      `json:"count"`
	ReplacedWith []string `json:"replaced_with,omitempty"`
}

// ScrubResult is what Scrub returns to callers.
type ScrubResult struct {
	Output         string            `json:"output"`
	Redactions     []Redaction       `json:"redactions"`
	AliasesCreated map[string]string `json:"aliases_created,omitempty"` // token -> displayValue
}

// EgressParams describes one row to insert into aria_llm_egress_log.
type EgressParams struct {
	RequestID      uuid.UUID
	ObservationID  *uuid.UUID
	LLMProvider    string
	LLMModel       string
	ClientID       *uuid.UUID
	Scrubbed       bool
	Redactions     []Redaction
	Payload        string // post-scrub payload; only its hash + size are stored
	InitiatedByUID uuid.UUID
	Reason         string
}

// EntityLookup is the optional dependency that lets Scrub resolve cotizador
// entities (clients / quotes / leads) to opaque tokens. If a Service has no
// EntityLookup wired, that detection layer is skipped.
type EntityLookup interface {
	// LookupClientName returns the canonical display name and UUID for a client
	// matched by partial name (case-insensitive substring). Returns "" if not
	// found.
	LookupClientByNameContains(ctx context.Context, fragment string) (id string, displayName string, err error)
	// LookupQuoteByFolio returns the UUID for an exact folio match (e.g.
	// "ITD-2026-PARK-001").
	LookupQuoteByFolio(ctx context.Context, folio string) (id string, err error)
	// LookupLeadByName returns the UUID for a partial lead name match.
	LookupLeadByNameContains(ctx context.Context, fragment string) (id string, displayName string, err error)
}

// Service is the public API of the redactor module.
type Service interface {
	Scrub(ctx context.Context, text string, opts ScrubOptions) (*ScrubResult, error)
	Expand(ctx context.Context, text string) (string, error)
	CanSendToLLM(sens Sensitivity, llmProvider string) bool
	LogEgress(ctx context.Context, params EgressParams) error
	InferSensitivity(narrative, facts string, scope string, clientID *uuid.UUID) Sensitivity
}

// StringInferrer is the string-keyed view of the inferrer used by ariamem
// (which avoids importing this package directly to keep the dep graph clean).
type StringInferrer interface {
	InferSensitivityString(narrative, facts, scope, clientID string) string
}

// ScrubString is the string-only convenience used by cloudserver's ScrubGate
// hook. It always uses ModeTokens and returns ("", "[]") on any error.
func (s *service) ScrubString(ctx context.Context, text string) (string, string) {
	if strings.TrimSpace(text) == "" {
		return text, "[]"
	}
	r, err := s.Scrub(ctx, text, ScrubOptions{Mode: ModeTokens})
	if err != nil {
		return text, "[]"
	}
	js, mErr := json.Marshal(r.Redactions)
	if mErr != nil {
		return r.Output, "[]"
	}
	return r.Output, string(js)
}

// LogEgressString is the cloudserver-shaped logger that converts loose
// strings to the typed EgressParams used internally.
func (s *service) LogEgressString(
	ctx context.Context,
	requestID, observationID, provider, model, clientID, userUID, reason, payloadHash string,
	payloadSize int,
	scrubbed bool,
	redactionsJSON string,
) error {
	var rid uuid.UUID
	if v := strings.TrimSpace(requestID); v != "" {
		if parsed, err := uuid.Parse(v); err == nil {
			rid = parsed
		} else {
			rid = uuid.New()
		}
	} else {
		rid = uuid.New()
	}
	var obsPtr *uuid.UUID
	if v := strings.TrimSpace(observationID); v != "" {
		// observation IDs in aria_observations may be the legacy "obs_xxx"
		// hex format, not UUIDs. Only attach if parses cleanly.
		if parsed, err := uuid.Parse(v); err == nil {
			obsPtr = &parsed
		}
	}
	var clientPtr *uuid.UUID
	if v := strings.TrimSpace(clientID); v != "" {
		if parsed, err := uuid.Parse(v); err == nil {
			clientPtr = &parsed
		}
	}
	var userID uuid.UUID
	if v := strings.TrimSpace(userUID); v != "" {
		if parsed, err := uuid.Parse(v); err == nil {
			userID = parsed
		}
	}
	if userID == uuid.Nil {
		// Egress log requires NOT NULL initiated_by_uid; fall back to a
		// sentinel deterministic UUID so the audit row is still inserted.
		userID = uuid.MustParse("00000000-0000-0000-0000-000000000000")
	}

	// Build the redactions slice from JSON. If parsing fails, just log the
	// raw JSON string as one entry.
	var reds []Redaction
	_ = json.Unmarshal([]byte(redactionsJSON), &reds)

	return s.LogEgress(ctx, EgressParams{
		RequestID:      rid,
		ObservationID:  obsPtr,
		LLMProvider:    provider,
		LLMModel:       model,
		ClientID:       clientPtr,
		Scrubbed:       scrubbed,
		Redactions:     reds,
		Payload:        payloadHash, // hash already; passthrough so SHA stored
		InitiatedByUID: userID,
		Reason:         reason,
	})
}

// CanSendToLLMString accepts the sensitivity as a plain string so cloudserver
// (which does not import this package) can call into the policy.
func (s *service) CanSendToLLMString(sensitivity, provider string) bool {
	return s.CanSendToLLM(Sensitivity(strings.TrimSpace(sensitivity)), provider)
}

// Config is the wiring struct for New().
type Config struct {
	DB              *sql.DB      // optional; if nil, audit + alias persistence is skipped
	EntityLookup    EntityLookup // optional cotizador integration
	ProviderAllowed []string     // override env var; empty -> uses ARIA_CORE_LLM_ALLOWLIST or default
}

// New builds a Service. db may be nil for tests; in that case egress logging
// is a no-op and aliases live only in-memory.
func New(cfg Config) Service {
	var aliases AliasStore
	if cfg.DB != nil {
		aliases = NewAliasDBStore(cfg.DB)
	} else {
		aliases = NewMemoryAliasStore()
	}
	allowed := cfg.ProviderAllowed
	if len(allowed) == 0 {
		allowed = parseAllowlistEnv()
	}
	return &service{
		db:           cfg.DB,
		aliases:      aliases,
		entities:     cfg.EntityLookup,
		patterns:     builtinPatterns(),
		allowlist:    allowed,
		tokenPattern: regexp.MustCompile(`\[[A-Z]+-[A-F0-9]{4}\]`),
	}
}

// parseAllowlistEnv reads ARIA_CORE_LLM_ALLOWLIST (comma-separated provider
// names). When unset, defaults to "anthropic,ollama-local".
func parseAllowlistEnv() []string {
	raw := strings.TrimSpace(os.Getenv("ARIA_CORE_LLM_ALLOWLIST"))
	if raw == "" {
		return []string{"anthropic", "ollama-local"}
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(strings.ToLower(p)); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return []string{"anthropic", "ollama-local"}
	}
	return out
}

type service struct {
	db           *sql.DB
	aliases      AliasStore
	entities     EntityLookup
	patterns     []patternDef
	allowlist    []string
	tokenPattern *regexp.Regexp
}

// Scrub walks `text` matching every builtin pattern + entity lookup. It returns
// the rewritten output and a redaction summary suitable for audit logging.
//
// Determinism: the same input always produces the same tokens (modulo the order
// in which entities are resolved, which only affects already-deterministic IDs).
func (s *service) Scrub(ctx context.Context, text string, opts ScrubOptions) (*ScrubResult, error) {
	if opts.Mode == "" {
		opts.Mode = ModeTokens
	}
	output := text
	redactionMap := map[PatternType]*Redaction{}
	createdAliases := map[string]string{}

	// Layer 1: cotizador entity lookups (only when wired).
	if s.entities != nil {
		var err error
		output, err = s.scrubEntities(ctx, output, redactionMap, createdAliases)
		if err != nil {
			return nil, err
		}
	}

	// Layer 2: builtin Spanish-locale patterns.
	for _, p := range s.patterns {
		matches := p.Re.FindAllString(output, -1)
		if len(matches) == 0 {
			continue
		}
		// Deduplicate raw matches so that 3 occurrences of the same RFC produce
		// 3 replacements but only 1 alias-store write.
		seen := map[string]string{} // raw -> token
		for _, raw := range matches {
			if p.Filter != nil && !p.Filter(raw) {
				continue
			}
			if _, ok := seen[raw]; ok {
				continue
			}
			replacement := s.replacementFor(p.Type, raw, opts)
			seen[raw] = replacement
			if _, exists := createdAliases[replacement]; !exists {
				createdAliases[replacement] = raw
				if opts.Mode == ModeTokens {
					_ = s.aliases.Upsert(ctx, replacement, string(p.Type), raw, nil)
				}
			}
		}
		// Now substitute every occurrence of every distinct match.
		count := 0
		for raw, token := range seen {
			occurrences := strings.Count(output, raw)
			count += occurrences
			output = strings.ReplaceAll(output, raw, token)
		}
		if count > 0 {
			redactionMap[p.Type] = appendRedaction(redactionMap[p.Type], p.Type, count, valuesOf(seen))
		}
	}

	return &ScrubResult{
		Output:         output,
		Redactions:     sortRedactions(redactionMap),
		AliasesCreated: createdAliases,
	}, nil
}

// scrubEntities iterates word-tokens of `text` and tries to match them against
// the cotizador EntityLookup. It is intentionally conservative: only token
// sequences that look like names ("S. de R.L.", folios "ITD-...") are queried.
func (s *service) scrubEntities(
	ctx context.Context,
	text string,
	redactionMap map[PatternType]*Redaction,
	createdAliases map[string]string,
) (string, error) {
	out := text

	// Folios "ITD-YYYY-XXXX-NNN" — strict pattern, very low false-positive risk.
	folioRe := regexp.MustCompile(`\bITD-\d{4}-[A-Z]{2,8}-\d{3,5}\b`)
	folios := uniqueStrings(folioRe.FindAllString(out, -1))
	for _, folio := range folios {
		id, err := s.entities.LookupQuoteByFolio(ctx, folio)
		if err != nil {
			continue // graceful degradation — skip if lookup fails
		}
		if id == "" {
			continue
		}
		token := tokenForValue(PatternQuote, folio)
		_ = s.aliases.Upsert(ctx, token, string(PatternQuote), folio, &id)
		count := strings.Count(out, folio)
		out = strings.ReplaceAll(out, folio, token)
		createdAliases[token] = folio
		redactionMap[PatternQuote] = appendRedaction(redactionMap[PatternQuote], PatternQuote, count, []string{token})
	}

	return out, nil
}

// replacementFor produces the actual replacement string for a match given the
// scrub mode + structural preservation hint.
func (s *service) replacementFor(p PatternType, raw string, opts ScrubOptions) string {
	switch opts.Mode {
	case ModeRedact:
		if !opts.PreserveStructure {
			return "[REDACTED]"
		}
		// Structure-preserving redact: keep email '@'+TLD, address numbers,
		// etc. For now we just keep '@' for emails.
		if p == PatternEmail {
			at := strings.LastIndex(raw, "@")
			if at >= 0 {
				return "[REDACTED]@" + raw[at+1:]
			}
		}
		return "[REDACTED]"
	default:
		return tokenForValue(p, raw)
	}
}

// Expand reverses Scrub: any [TOKEN-XXXX] in the input is replaced with its
// stored displayValue. Tokens not found in the alias store are left as-is.
func (s *service) Expand(ctx context.Context, text string) (string, error) {
	if s.tokenPattern == nil {
		return text, nil
	}
	tokens := uniqueStrings(s.tokenPattern.FindAllString(text, -1))
	if len(tokens) == 0 {
		return text, nil
	}
	out := text
	for _, tok := range tokens {
		v, err := s.aliases.Lookup(ctx, tok)
		if err != nil {
			return out, err
		}
		if v == "" {
			continue
		}
		out = strings.ReplaceAll(out, tok, v)
	}
	return out, nil
}

// CanSendToLLM is the policy gate. It is a pure function (no DB). The caller is
// expected to short-circuit egress when this returns false.
func (s *service) CanSendToLLM(sens Sensitivity, provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	switch sens {
	case SensitivityPublic, SensitivityInternal:
		return true
	case SensitivityClient:
		if provider == "" {
			return false
		}
		return s.providerAllowed(provider)
	case SensitivityConfidential:
		return false
	default:
		// Unknown sensitivity is treated as confidential (fail-closed).
		return false
	}
}

func (s *service) providerAllowed(provider string) bool {
	for _, p := range s.allowlist {
		if p == provider {
			return true
		}
	}
	return false
}

// LogEgress persists one immutable row to aria_llm_egress_log. If the service
// has no DB wired, this is a no-op (useful in tests).
func (s *service) LogEgress(ctx context.Context, params EgressParams) error {
	if s == nil || s.db == nil {
		return nil
	}
	if params.RequestID == uuid.Nil {
		params.RequestID = uuid.New()
	}
	hash := sha256Hex(params.Payload)
	redactionsJSON, err := json.Marshal(params.Redactions)
	if err != nil {
		redactionsJSON = []byte("[]")
	}
	var obsArg any
	if params.ObservationID != nil {
		obsArg = params.ObservationID.String()
	}
	var clientArg any
	if params.ClientID != nil {
		clientArg = params.ClientID.String()
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO aria_llm_egress_log (
			request_id, observation_id, llm_provider, llm_model, client_id,
			scrubbed, redactions, payload_hash, payload_size,
			initiated_by_uid, reason, created_at
		) VALUES ($1, NULLIF($2,'')::uuid, $3, NULLIF($4,''), NULLIF($5,'')::uuid,
		         $6, $7::jsonb, $8, $9,
		         $10, $11, $12)
	`,
		params.RequestID.String(),
		stringOrEmpty(obsArg),
		strings.TrimSpace(strings.ToLower(params.LLMProvider)),
		params.LLMModel,
		stringOrEmpty(clientArg),
		params.Scrubbed,
		string(redactionsJSON),
		hash,
		len(params.Payload),
		params.InitiatedByUID.String(),
		params.Reason,
		time.Now().UTC(),
	)
	if err != nil {
		return fmt.Errorf("redactor: log egress: %w", err)
	}
	return nil
}

// InferSensitivity derives a sensitivity tag from observation content. Rules:
//
//  1. scope=client_knowledge OR clientID set -> at least 'client'
//  2. RFC / CURP / CLABE detected             -> 'confidential'
//  3. Amount >= 100,000 detected              -> at least 'client'
//  4. scope=personal AND password/token leak  -> 'confidential'
//  5. otherwise                                -> 'internal'
// InferSensitivityString is the string-keyed adapter used by the ariamem
// SensitivityInferrer hook (which intentionally does not import this package
// to avoid a cycle). clientID is the raw UUID string; "" means none.
func (s *service) InferSensitivityString(narrative, facts, scope, clientID string) string {
	var clientPtr *uuid.UUID
	if id := strings.TrimSpace(clientID); id != "" {
		if parsed, err := uuid.Parse(id); err == nil {
			clientPtr = &parsed
		}
	}
	return string(s.InferSensitivity(narrative, facts, scope, clientPtr))
}

func (s *service) InferSensitivity(narrative, facts, scope string, clientID *uuid.UUID) Sensitivity {
	combined := narrative + " " + facts
	level := SensitivityInternal

	if scope == "client_knowledge" || clientID != nil {
		level = SensitivityClient
	}

	// Confidential triggers — always escalate.
	if reRFC.MatchString(combined) || reCURP.MatchString(combined) {
		return SensitivityConfidential
	}
	if matches := reCLABE.FindAllString(combined, -1); len(matches) > 0 {
		for _, m := range matches {
			if len(strings.TrimSpace(m)) == 18 {
				return SensitivityConfidential
			}
		}
	}
	if hasSecretLeak(combined) && scope == "personal" {
		return SensitivityConfidential
	}

	// Amount escalation — only if not already confidential.
	if amountAtLeast(combined, 100_000) {
		if level == SensitivityInternal {
			level = SensitivityClient
		}
	}
	return level
}

// hasSecretLeak detects obvious password / token leaks in a personal-scoped
// observation. Heuristic: looks for "password=", "token=", "secret=" with at
// least 8 trailing non-space chars, or AWS-shaped keys.
func hasSecretLeak(text string) bool {
	loose := regexp.MustCompile(`(?i)\b(password|passwd|secret|api[_\- ]?key|token)\s*[:=]\s*\S{8,}`)
	if loose.MatchString(text) {
		return true
	}
	awsLike := regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	return awsLike.MatchString(text)
}

// amountAtLeast scans for any monetary token whose numeric value is >= floor.
// Strips '$', spaces, currency suffixes, and thousands commas before parsing.
func amountAtLeast(text string, floor float64) bool {
	matches := reAmount.FindAllString(text, -1)
	for _, m := range matches {
		v := strings.TrimSpace(m)
		v = strings.TrimPrefix(v, "$")
		v = strings.TrimSpace(v)
		// Drop currency suffixes.
		for _, suffix := range []string{"MXN", "USD", "MN", "pesos", "dólares", "dolares"} {
			v = strings.TrimSuffix(strings.TrimSpace(v), suffix)
			v = strings.TrimSuffix(strings.TrimSpace(v), strings.ToLower(suffix))
		}
		v = strings.TrimSpace(v)
		v = strings.ReplaceAll(v, ",", "")
		f := parseFloatSafe(v)
		if f >= floor {
			return true
		}
	}
	return false
}

func parseFloatSafe(s string) float64 {
	var v float64
	_, err := fmt.Sscanf(s, "%f", &v)
	if err != nil {
		return 0
	}
	return v
}

// sha256Hex returns the hex-encoded sha256 hash of payload. Empty string for
// empty payload.
func sha256Hex(payload string) string {
	if payload == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

func valuesOf(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

func appendRedaction(existing *Redaction, t PatternType, addCount int, replaced []string) *Redaction {
	if existing == nil {
		return &Redaction{Type: string(t), Count: addCount, ReplacedWith: replaced}
	}
	existing.Count += addCount
	existing.ReplacedWith = append(existing.ReplacedWith, replaced...)
	return existing
}

func sortRedactions(m map[PatternType]*Redaction) []Redaction {
	out := make([]Redaction, 0, len(m))
	for _, v := range m {
		if v == nil {
			continue
		}
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// stringOrEmpty unwraps a nullable any-string for SQL passthrough.
// NULLIF($,'')::uuid then catches the conversion at the DB layer.
func stringOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
