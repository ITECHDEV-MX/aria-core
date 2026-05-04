package store

// migrateConflictsTable creates the aria_memory_conflicts table and
// indexes if they don't already exist. Idempotent — safe to call on
// every store startup.
func (s *Store) migrateConflictsTable() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS aria_memory_conflicts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			observation_a_id INTEGER NOT NULL,
			observation_b_id INTEGER NOT NULL,
			project TEXT,
			topic_key TEXT,
			detection_signal TEXT NOT NULL,
			detection_score REAL,
			status TEXT NOT NULL DEFAULT 'pending',
			verdict TEXT,
			verdict_reason TEXT,
			verdict_confidence REAL,
			verdict_model TEXT,
			verdict_session_id TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			resolved_at TEXT,
			FOREIGN KEY (observation_a_id) REFERENCES observations(id),
			FOREIGN KEY (observation_b_id) REFERENCES observations(id)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_conflicts_status   ON aria_memory_conflicts(status, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_conflicts_obs_a    ON aria_memory_conflicts(observation_a_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conflicts_obs_b    ON aria_memory_conflicts(observation_b_id)`,
		`CREATE INDEX IF NOT EXISTS idx_conflicts_project  ON aria_memory_conflicts(project, status)`,
	}
	for _, q := range stmts {
		if _, err := s.execHook(s.db, q); err != nil {
			return err
		}
	}
	return nil
}
