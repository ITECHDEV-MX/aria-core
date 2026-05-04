package conflicts

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// fixture sets up an in-memory SQLite DB with a minimal observations
// table + FTS index + the conflicts table. Returns an open *sql.DB
// the test owns.
func fixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	stmts := []string{
		`CREATE TABLE observations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL DEFAULT 'test',
			type TEXT NOT NULL DEFAULT 'general',
			title TEXT NOT NULL,
			content TEXT NOT NULL,
			project TEXT,
			scope TEXT NOT NULL DEFAULT 'project',
			topic_key TEXT,
			normalized_hash TEXT,
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			deleted_at TEXT
		)`,
		`CREATE VIRTUAL TABLE observations_fts USING fts5(
			title, content, tool_name, type, project, topic_key,
			content='observations', content_rowid='id'
		)`,
		`CREATE TRIGGER obs_fts_ins AFTER INSERT ON observations BEGIN
			INSERT INTO observations_fts(rowid, title, content, type, project, topic_key)
			VALUES (new.id, new.title, new.content, new.type, COALESCE(new.project,''), COALESCE(new.topic_key,''));
		END`,
		`CREATE TABLE aria_memory_conflicts (
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
			resolved_at TEXT
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("setup %q: %v", s[:40], err)
		}
	}
	return db
}

// insertObs is a helper to insert an observation and return its id +
// the synthesized Observation struct.
func insertObs(t *testing.T, db *sql.DB, project, topicKey, title, content, hash string) Observation {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO observations (title, content, project, topic_key, normalized_hash)
		VALUES (?, ?, ?, ?, ?)
	`, title, content, project, topicKey, hash)
	if err != nil {
		t.Fatalf("insert obs: %v", err)
	}
	id, _ := res.LastInsertId()
	return Observation{
		ID:             id,
		Project:        project,
		Scope:          "project",
		Type:           "general",
		Title:          title,
		TopicKey:       topicKey,
		NormalizedHash: hash,
		CreatedAt:      time.Now(),
	}
}

func TestVerdictIsValid(t *testing.T) {
	for _, v := range []Verdict{VerdictSupersedes, VerdictEquivalent, VerdictConflicting, VerdictDismissed, VerdictRelated} {
		if !v.IsValid() {
			t.Errorf("expected %q to be valid", v)
		}
	}
	if Verdict("nonsense").IsValid() {
		t.Error("nonsense should not be valid")
	}
}

func TestDetect_NoCandidates_FreshStore(t *testing.T) {
	db := fixture(t)
	obs := insertObs(t, db, "p1", "topic-x", "First observation", "First body", "hash-A")
	candidates, err := DetectCandidates(context.Background(), db, obs, 5)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(candidates) != 0 {
		t.Errorf("expected no candidates for first save, got %d: %+v", len(candidates), candidates)
	}
}

func TestDetect_TopicKeyHashDiverge(t *testing.T) {
	db := fixture(t)
	_ = insertObs(t, db, "p1", "auth-strategy", "Use Clean Architecture", "Body original", "hash-1")
	newer := insertObs(t, db, "p1", "auth-strategy", "Use Hexagonal Architecture", "Body changed", "hash-2")

	candidates, err := DetectCandidates(context.Background(), db, newer, 5)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	if len(candidates) == 0 {
		t.Fatal("expected at least one candidate for divergent hash on same topic_key")
	}
	c := candidates[0]
	if c.Signal != SignalTopicKeyHashDiverge {
		t.Errorf("expected topic_key signal, got %q", c.Signal)
	}
	if c.Score != 1.0 {
		t.Errorf("expected score 1.0, got %v", c.Score)
	}
}

func TestDetect_TopicKeySameHash_NoFlag(t *testing.T) {
	db := fixture(t)
	_ = insertObs(t, db, "p1", "auth-strategy", "Use Clean Architecture", "Body", "hash-same")
	dup := insertObs(t, db, "p1", "auth-strategy", "Use Clean Architecture", "Body", "hash-same")
	candidates, err := DetectCandidates(context.Background(), db, dup, 5)
	if err != nil {
		t.Fatalf("detect: %v", err)
	}
	for _, c := range candidates {
		if c.Signal == SignalTopicKeyHashDiverge {
			t.Error("should not flag topic_key signal when hashes match")
		}
	}
}

func TestRecordVerdict_HappyPath(t *testing.T) {
	db := fixture(t)
	a := insertObs(t, db, "p1", "auth-strategy", "Old", "...", "h1")
	b := insertObs(t, db, "p1", "auth-strategy", "New", "...", "h2")

	id, err := InsertPending(context.Background(), db, Candidate{
		ObservationAID: b.ID, ObservationBID: a.ID,
		Project: "p1", TopicKey: "auth-strategy",
		Signal: SignalTopicKeyHashDiverge, Score: 1.0,
	})
	if err != nil {
		t.Fatalf("insert pending: %v", err)
	}

	row, err := RecordVerdict(context.Background(), db, RecordVerdictArgs{
		ConflictID: id, Verdict: VerdictSupersedes,
		Reason: "newer info", Confidence: 0.9,
		Model: "claude-opus-4-7", SessionID: "sess-test",
	})
	if err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	if row.Status != StatusResolved {
		t.Errorf("status: want resolved, got %q", row.Status)
	}
	if row.Verdict != VerdictSupersedes {
		t.Errorf("verdict: want supersedes, got %q", row.Verdict)
	}
	if row.VerdictReason != "newer info" || row.VerdictModel != "claude-opus-4-7" {
		t.Errorf("audit fields not populated: %+v", row)
	}
	if row.ResolvedAt.IsZero() {
		t.Error("resolved_at not set")
	}
}

func TestRecordVerdict_AlreadyResolved(t *testing.T) {
	db := fixture(t)
	a := insertObs(t, db, "p1", "k", "A", "", "h1")
	b := insertObs(t, db, "p1", "k", "B", "", "h2")
	id, _ := InsertPending(context.Background(), db, Candidate{
		ObservationAID: b.ID, ObservationBID: a.ID,
		Project: "p1", TopicKey: "k",
		Signal: SignalTopicKeyHashDiverge, Score: 1.0,
	})
	_, _ = RecordVerdict(context.Background(), db, RecordVerdictArgs{ConflictID: id, Verdict: VerdictDismissed})

	_, err := RecordVerdict(context.Background(), db, RecordVerdictArgs{ConflictID: id, Verdict: VerdictSupersedes})
	if err == nil {
		t.Error("expected error on second resolve")
	}
}

func TestRecordCompare_Manual(t *testing.T) {
	db := fixture(t)
	a := insertObs(t, db, "p1", "ka", "A", "", "h1")
	b := insertObs(t, db, "p1", "kb", "B", "", "h2")

	row, err := RecordCompare(context.Background(), db, RecordCompareArgs{
		ObservationAID: a.ID, ObservationBID: b.ID,
		Verdict: VerdictRelated, Reason: "they reference the same module",
		Confidence: 0.7, Model: "agent",
	})
	if err != nil {
		t.Fatalf("record compare: %v", err)
	}
	if row.Status != StatusResolved {
		t.Errorf("compare row should be resolved on insert, got %q", row.Status)
	}
	if row.Verdict != VerdictRelated {
		t.Errorf("verdict: %q", row.Verdict)
	}
	if row.DetectionSignal != "manual_compare" {
		t.Errorf("signal: %q", row.DetectionSignal)
	}
}
