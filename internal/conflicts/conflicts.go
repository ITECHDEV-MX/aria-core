// Package conflicts implements memory-conflict surfacing for ARIA Core.
//
// When a new observation is saved that contradicts a prior one, the
// detector flags the candidates so the agent (or human via dashboard)
// can record a verdict. Resolutions are persisted in the
// aria_memory_conflicts table with full audit trail (who, when,
// model, confidence, reason).
//
// Inspired by engram v1.14.0 mem_judge / mem_compare pattern, adapted
// to ARIA's naming (aria_judge / aria_compare) and storage (Postgres
// cloud + SQLite local).
package conflicts

import "time"

// Status describes the lifecycle of a conflict row.
type Status string

const (
	StatusPending   Status = "pending"
	StatusResolved  Status = "resolved"
	StatusDismissed Status = "dismissed"
)

// Verdict is the agent's (or human's) judgment between two
// observations once a conflict has been resolved.
type Verdict string

const (
	VerdictSupersedes  Verdict = "supersedes"  // A replaces B (or B replaces A — see direction in row)
	VerdictEquivalent  Verdict = "equivalent"  // A and B are essentially the same memory
	VerdictConflicting Verdict = "conflicting" // A and B contradict and both are valid in different contexts
	VerdictDismissed   Verdict = "dismissed"   // Surfaced incorrectly; not a real conflict
	VerdictRelated     Verdict = "related"     // Used by aria_compare for non-conflicting links
)

// IsValid reports whether v is one of the canonical verdicts.
func (v Verdict) IsValid() bool {
	switch v {
	case VerdictSupersedes, VerdictEquivalent, VerdictConflicting, VerdictDismissed, VerdictRelated:
		return true
	}
	return false
}

// DetectionSignal labels how a candidate was identified.
type DetectionSignal string

const (
	SignalTopicKeyHashDiverge DetectionSignal = "topic_key_hash_diverge"
	SignalFTSTitleOverlap     DetectionSignal = "fts_title_overlap"
	SignalManualCompare       DetectionSignal = "manual_compare"
)

// Candidate is a single conflict candidate produced by DetectCandidates.
// It is purely a transport struct — persistence is the caller's job.
type Candidate struct {
	ObservationAID      int64           // the just-saved observation
	ObservationBID      int64           // the candidate predecessor
	Project             string
	TopicKey            string
	Signal              DetectionSignal
	Score               float64
	PriorTitle          string
	PriorContentSnippet string
}

// Row mirrors the aria_memory_conflicts table.
type Row struct {
	ID                int64
	ObservationAID    int64
	ObservationBID    int64
	Project           string
	TopicKey          string
	DetectionSignal   string
	DetectionScore    float64
	Status            Status
	Verdict           Verdict
	VerdictReason     string
	VerdictConfidence float64
	VerdictModel      string
	VerdictSessionID  string
	CreatedAt         time.Time
	ResolvedAt        time.Time // zero if not resolved
}

// Observation is the minimal shape DetectCandidates needs from a
// just-saved observation. Defined here to avoid pulling the full
// store package (and its CGO-free SQLite driver) into conflicts'
// import graph for tests that only exercise the detector.
type Observation struct {
	ID             int64
	Project        string
	Scope          string
	Type           string
	Title          string
	TopicKey       string
	NormalizedHash string
	CreatedAt      time.Time
}
