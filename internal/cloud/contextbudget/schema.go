package contextbudget

import (
	"context"
	"database/sql"
	"fmt"
)

// === SQL ===
//
// Estas migraciones se ejecutan desde cloudstore.Migrate vía la sección
// // BEGIN CONTEXT MIGRATIONS / END. La función Migrate es idempotente; usa
// CREATE TABLE IF NOT EXISTS / ALTER TABLE ADD COLUMN IF NOT EXISTS.
//
// Esquema:
//
//	aria_skill_usage — telemetría de retrieval+feedback de skills.
//	aria_mcp_config  — defaults configurables por endpoint MCP.
const SchemaSQL = `
-- Telemetría de skills (cada vez que aria_get_skills retorna uno).
CREATE TABLE IF NOT EXISTS aria_skill_usage (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  skill_id TEXT NOT NULL REFERENCES aria_skills(id) ON DELETE CASCADE,
  session_id TEXT,
  developer_uid UUID,
  project TEXT,
  task_description TEXT,
  position_in_results INT,
  did_help BOOLEAN,
  feedback_signal TEXT,
  feedback_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_skill_usage_skill ON aria_skill_usage(skill_id, did_help);
CREATE INDEX IF NOT EXISTS idx_skill_usage_recent ON aria_skill_usage(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_skill_usage_dev ON aria_skill_usage(developer_uid);

-- Token budget config por endpoint MCP (override opcional vs defaults código).
CREATE TABLE IF NOT EXISTS aria_mcp_config (
  tool_name TEXT PRIMARY KEY,
  default_token_budget INT NOT NULL DEFAULT 4000,
  max_results INT NOT NULL DEFAULT 20,
  rerank_strategy TEXT NOT NULL DEFAULT 'canon-first',
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO aria_mcp_config (tool_name) VALUES
  ('aria_search'),
  ('aria_get_skills'),
  ('aria_get_recipes')
ON CONFLICT (tool_name) DO NOTHING;
`

// Migrate ejecuta SchemaSQL contra el *sql.DB provisto. Idempotente.
//
// IMPORTANTE: Esta migración asume que aria_skills ya existe. cloudstore.go la
// crea antes en su slice de queries; encadenar Migrate después de
// CloudStore.New garantiza el orden correcto.
func Migrate(ctx context.Context, db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("contextbudget: nil db")
	}
	// Ejecutamos statements separados; SchemaSQL los une con ; — pgx soporta
	// multi-statement Exec.
	if _, err := db.ExecContext(ctx, SchemaSQL); err != nil {
		return fmt.Errorf("contextbudget: migrate: %w", err)
	}
	return nil
}

// MCPConfig representa los overrides configurables (también consumibles desde
// el dashboard de admin).
type MCPConfig struct {
	ToolName           string
	DefaultTokenBudget int
	MaxResults         int
	RerankStrategy     RankStrategy
}

// LoadMCPConfig retorna el config persistido para una tool, con defaults si la
// fila no existe.
func LoadMCPConfig(ctx context.Context, db *sql.DB, tool string) (MCPConfig, error) {
	cfg := MCPConfig{
		ToolName:           tool,
		DefaultTokenBudget: 4000,
		MaxResults:         20,
		RerankStrategy:     StrategyCanonFirst,
	}
	if db == nil {
		return cfg, nil
	}
	var strat string
	err := db.QueryRowContext(ctx, `
		SELECT default_token_budget, max_results, rerank_strategy
		FROM aria_mcp_config WHERE tool_name = $1
	`, tool).Scan(&cfg.DefaultTokenBudget, &cfg.MaxResults, &strat)
	if err != nil && err != sql.ErrNoRows {
		return cfg, err
	}
	if strat != "" {
		cfg.RerankStrategy = RankStrategy(strat)
	}
	return cfg, nil
}
