package roi

import (
	"context"
	"database/sql"
	"fmt"
)

// SchemaSQL define la tabla aria_search_log usada por la métrica RDR
// (Re-Discovery Rate). Cada call a /v1/memory/search registra una fila.
//
// Esta migración se ejecuta desde cloudstore.Migrate vía la sección marcada con
// // BEGIN ROI MIGRATIONS / END. Es idempotente.
const SchemaSQL = `
CREATE TABLE IF NOT EXISTS aria_search_log (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  query TEXT NOT NULL,
  result_count INT NOT NULL DEFAULT 0,
  canon_hit_count INT NOT NULL DEFAULT 0,
  total_tokens INT,
  truncated_count INT,
  developer_uid UUID,
  project TEXT,
  scope TEXT,
  client_id UUID,
  duration_ms INT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_search_log_dev ON aria_search_log(developer_uid, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_search_log_recent ON aria_search_log(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_search_log_project ON aria_search_log(project, created_at DESC);
`

// Migrate ejecuta SchemaSQL contra el *sql.DB. Idempotente.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("roi: nil db")
	}
	if _, err := db.ExecContext(ctx, SchemaSQL); err != nil {
		return fmt.Errorf("roi: migrate: %w", err)
	}
	return nil
}
