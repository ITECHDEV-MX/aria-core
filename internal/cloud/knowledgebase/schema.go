package knowledgebase

import (
	"context"
	"database/sql"
	"fmt"
)

// EntityType labels usados en aria_kb_synced_entities.entity_type.
const (
	EntityTypePRD           = "prd"
	EntityTypeHistoria      = "historia"
	EntityTypeCotizacion    = "cotizacion"
	EntityTypeProjectReadme = "project_readme"
	EntityTypePlantilla     = "plantilla"
	EntityTypeRootIndex     = "root_index"
)

// SyncStatus labels usados en aria_kb_synced_entities.sync_status.
const (
	SyncStatusOK      = "ok"
	SyncStatusPending = "pending"
	SyncStatusFailed  = "failed"
	SyncStatusSkipped = "skipped"
)

// Migrate aplica las migraciones del módulo knowledgebase. Es idempotente
// (todas las queries usan IF NOT EXISTS) y por eso es seguro ejecutarlo en
// cada arranque desde cloudstore.go.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("knowledgebase: nil db")
	}
	queries := []string{
		`CREATE TABLE IF NOT EXISTS aria_kb_synced_entities (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			entity_type TEXT NOT NULL CHECK (entity_type IN ('prd','historia','cotizacion','project_readme','plantilla','root_index')),
			entity_id UUID,
			project_id UUID,
			repo_path TEXT NOT NULL,
			last_commit_sha TEXT,
			last_content_hash TEXT,
			last_synced_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			sync_status TEXT NOT NULL DEFAULT 'ok' CHECK (sync_status IN ('ok','pending','failed','skipped')),
			last_error TEXT
		)`,
		// El UNIQUE necesita un índice expression-based porque entity_id puede
		// ser NULL para root_index/plantillas y queremos que solo haya UNA fila
		// por (entity_type, entity_id) tomando NULL como un valor singular.
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_kb_synced_entity
			ON aria_kb_synced_entities(entity_type, COALESCE(entity_id::text, '___index___'))`,
		`CREATE INDEX IF NOT EXISTS idx_kb_synced_project ON aria_kb_synced_entities(project_id)`,
		`CREATE INDEX IF NOT EXISTS idx_kb_synced_status ON aria_kb_synced_entities(sync_status)`,
	}
	for _, q := range queries {
		if _, err := db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("knowledgebase: migrate: %w", err)
		}
	}
	return nil
}

// SyncedEntity es la fila pública del tracking de sync por entidad.
type SyncedEntity struct {
	ID              string
	EntityType      string
	EntityID        string // "" si NULL
	ProjectID       string // "" si NULL
	RepoPath        string
	LastCommitSHA   string
	LastContentHash string
	LastSyncedAt    string // ISO8601
	SyncStatus      string
	LastError       string
}

// SyncStats agrega counts por estado.
type SyncStats struct {
	Total   int            `json:"total"`
	ByState map[string]int `json:"by_state"`
}
