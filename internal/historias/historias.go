// Package historias implements the file-backed multi-agent artifact
// chain that ARIA Core uses for story-creation pipelines (and any
// other gstack-style sprint flow).
//
// An "historia" is a slug-named directory under <repo>/Historias/
// (or any caller-supplied root) containing numbered Markdown
// artifacts produced by chained agents. Each artifact is named
// "<position>-<slug>.md" — for example:
//
//   /Historias/2026-05-mantenimiento-industrial-cotizador/
//     0-office-hours.md
//     1-ceo-plan.md
//     2-eng-plan.md
//     3-story.md
//     MANIFEST.yaml
//
// MANIFEST.yaml is the per-historia audit log: which skill produced
// which artifact, which agent model, what previous artifacts it
// consumed, content hash, duration. The orchestrator updates it with
// each SaveArtifact call.
package historias

import "time"

// ChainStatus tracks where the historia is in its lifecycle.
type ChainStatus string

const (
	StatusInProgress ChainStatus = "in_progress"
	StatusCompleted  ChainStatus = "completed"
	StatusAbandoned  ChainStatus = "abandoned"
)

// ChainEntry is one artifact in the chain.
type ChainEntry struct {
	Position       int      `yaml:"position"        json:"position"`
	Artifact       string   `yaml:"artifact"        json:"artifact"`        // filename
	Skill          string   `yaml:"skill"           json:"skill"`           // skill@version
	AgentModel     string   `yaml:"agent_model"     json:"agent_model"`
	ContentSHA256  string   `yaml:"content_sha256"  json:"content_sha256"`
	Inputs         []string `yaml:"inputs"          json:"inputs"`          // filenames consumed
	DurationMs     int64    `yaml:"duration_ms"     json:"duration_ms"`
	AriaPageID     string   `yaml:"aria_page_id,omitempty" json:"aria_page_id,omitempty"`
	CreatedAt      string   `yaml:"created_at"      json:"created_at"`
}

// ChainManifest is the per-slug MANIFEST.yaml shape.
type ChainManifest struct {
	Slug          string       `yaml:"slug"           json:"slug"`
	CreatedAt     string       `yaml:"created_at"     json:"created_at"`
	CreatedBy     string       `yaml:"created_by"     json:"created_by"`
	Status        ChainStatus  `yaml:"status"         json:"status"`
	Chain         []ChainEntry `yaml:"chain"          json:"chain"`
	FinalArtifact string       `yaml:"final_artifact,omitempty" json:"final_artifact,omitempty"`
}

// nowUTC returns the current UTC RFC3339 timestamp. Var hook for test.
var nowUTC = func() string { return time.Now().UTC().Format(time.RFC3339) }
