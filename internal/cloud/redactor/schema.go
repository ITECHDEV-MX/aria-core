package redactor

// Migrations holds the redactor-specific DDL applied on top of cloudstore. They
// are appended to the cloudstore migrate() loop so that bootstrap creates the
// new columns/tables in one shot.
//
// Each statement is idempotent (uses IF NOT EXISTS / IF EXISTS guards) so it is
// safe to run on every aria-core boot.
var Migrations = []string{
	// Sensitivity tag en aria_observations.
	`ALTER TABLE aria_observations ADD COLUMN IF NOT EXISTS sensitivity TEXT NOT NULL DEFAULT 'internal'`,
	`DO $$ BEGIN
		IF NOT EXISTS (
			SELECT 1 FROM pg_constraint WHERE conname = 'aria_obs_sensitivity_check'
		) THEN
			ALTER TABLE aria_observations
			  ADD CONSTRAINT aria_obs_sensitivity_check
			  CHECK (sensitivity IN ('public','internal','client','confidential'));
		END IF;
	END $$`,
	`CREATE INDEX IF NOT EXISTS idx_aria_obs_sensitivity ON aria_observations(sensitivity)`,

	// Audit log inmutable de envíos a LLMs externos.
	`CREATE TABLE IF NOT EXISTS aria_llm_egress_log (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		request_id UUID NOT NULL,
		observation_id UUID,
		llm_provider TEXT NOT NULL,
		llm_model TEXT,
		client_id UUID,
		scrubbed BOOLEAN NOT NULL,
		redactions JSONB NOT NULL DEFAULT '[]'::jsonb,
		payload_hash TEXT,
		payload_size INT NOT NULL DEFAULT 0,
		initiated_by_uid UUID NOT NULL,
		reason TEXT,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_egress_client ON aria_llm_egress_log(client_id, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_egress_user ON aria_llm_egress_log(initiated_by_uid, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_egress_provider ON aria_llm_egress_log(llm_provider, created_at DESC)`,
	`CREATE INDEX IF NOT EXISTS idx_egress_created_at ON aria_llm_egress_log(created_at DESC)`,

	// Aliases de tokens determinísticos (cache token <-> displayValue).
	`CREATE TABLE IF NOT EXISTS aria_redaction_aliases (
		id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		alias_token TEXT NOT NULL UNIQUE,
		entity_type TEXT NOT NULL,
		entity_id UUID,
		display_value TEXT NOT NULL,
		created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
	)`,
	`CREATE INDEX IF NOT EXISTS idx_aliases_entity ON aria_redaction_aliases(entity_type, entity_id)`,
}
