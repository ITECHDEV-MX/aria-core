package conflicts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrNotFound is returned by RecordVerdict / Get when the conflict id
// does not exist.
var ErrNotFound = errors.New("conflicts: not found")

// ErrAlreadyResolved is returned by RecordVerdict when the conflict
// is not in 'pending' status.
var ErrAlreadyResolved = errors.New("conflicts: already resolved")

// InsertPending persists a candidate as a pending conflict row and
// returns its id.
func InsertPending(ctx context.Context, db *sql.DB, c Candidate) (int64, error) {
	q := `
		INSERT INTO aria_memory_conflicts
		(observation_a_id, observation_b_id, project, topic_key,
		 detection_signal, detection_score, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, 'pending', datetime('now'))
	`
	res, err := db.ExecContext(ctx, q,
		c.ObservationAID, c.ObservationBID,
		c.Project, c.TopicKey,
		string(c.Signal), c.Score)
	if err != nil {
		return 0, fmt.Errorf("insert pending conflict: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("conflict last insert id: %w", err)
	}
	return id, nil
}

// RecordVerdictArgs is the input for resolving a conflict via aria_judge.
type RecordVerdictArgs struct {
	ConflictID int64
	Verdict    Verdict
	Reason     string
	Confidence float64
	Model      string
	SessionID  string
}

// RecordVerdict moves a pending conflict to status='resolved' with
// the supplied verdict + audit fields.
func RecordVerdict(ctx context.Context, db *sql.DB, args RecordVerdictArgs) (Row, error) {
	if !args.Verdict.IsValid() {
		return Row{}, fmt.Errorf("invalid verdict %q", args.Verdict)
	}

	// Tx so the read + update are consistent.
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Row{}, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var status string
	if err := tx.QueryRowContext(ctx,
		`SELECT status FROM aria_memory_conflicts WHERE id = ?`, args.ConflictID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Row{}, ErrNotFound
		}
		return Row{}, fmt.Errorf("read conflict %d: %w", args.ConflictID, err)
	}
	if Status(status) != StatusPending {
		return Row{}, ErrAlreadyResolved
	}

	_, err = tx.ExecContext(ctx, `
		UPDATE aria_memory_conflicts
		SET status              = 'resolved',
		    verdict             = ?,
		    verdict_reason      = ?,
		    verdict_confidence  = ?,
		    verdict_model       = ?,
		    verdict_session_id  = ?,
		    resolved_at         = datetime('now')
		WHERE id = ?
	`, string(args.Verdict), args.Reason, args.Confidence, args.Model, args.SessionID, args.ConflictID)
	if err != nil {
		return Row{}, fmt.Errorf("update conflict %d: %w", args.ConflictID, err)
	}
	if err := tx.Commit(); err != nil {
		return Row{}, fmt.Errorf("commit: %w", err)
	}

	return Get(ctx, db, args.ConflictID)
}

// RecordCompareArgs is the input for aria_compare — a manual,
// agent-asserted comparison between any two observations.
type RecordCompareArgs struct {
	ObservationAID int64
	ObservationBID int64
	Verdict        Verdict
	Reason         string
	Confidence     float64
	Model          string
	SessionID      string
}

// RecordCompare inserts an already-resolved conflict row representing
// an agent's manual comparison between two observations.
func RecordCompare(ctx context.Context, db *sql.DB, args RecordCompareArgs) (Row, error) {
	if !args.Verdict.IsValid() {
		return Row{}, fmt.Errorf("invalid verdict %q", args.Verdict)
	}
	if args.ObservationAID == 0 || args.ObservationBID == 0 {
		return Row{}, errors.New("both observation ids required")
	}

	q := `
		INSERT INTO aria_memory_conflicts
		(observation_a_id, observation_b_id, detection_signal,
		 status, verdict, verdict_reason, verdict_confidence,
		 verdict_model, verdict_session_id,
		 created_at, resolved_at)
		VALUES (?, ?, 'manual_compare',
		        'resolved', ?, ?, ?, ?, ?,
		        datetime('now'), datetime('now'))
	`
	res, err := db.ExecContext(ctx, q,
		args.ObservationAID, args.ObservationBID,
		string(args.Verdict), args.Reason, args.Confidence,
		args.Model, args.SessionID)
	if err != nil {
		return Row{}, fmt.Errorf("insert manual compare: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return Row{}, fmt.Errorf("manual compare last insert id: %w", err)
	}
	return Get(ctx, db, id)
}

// Get fetches a single conflict row by id.
func Get(ctx context.Context, db *sql.DB, id int64) (Row, error) {
	q := `
		SELECT id, observation_a_id, observation_b_id,
		       COALESCE(project, ''), COALESCE(topic_key, ''),
		       detection_signal, COALESCE(detection_score, 0),
		       status,
		       COALESCE(verdict, ''), COALESCE(verdict_reason, ''),
		       COALESCE(verdict_confidence, 0),
		       COALESCE(verdict_model, ''), COALESCE(verdict_session_id, ''),
		       created_at, COALESCE(resolved_at, '')
		FROM aria_memory_conflicts
		WHERE id = ?
	`
	row := db.QueryRowContext(ctx, q, id)
	var (
		r          Row
		statusStr  string
		verdictStr string
		createdAt  string
		resolvedAt string
		signal     string
	)
	if err := row.Scan(
		&r.ID, &r.ObservationAID, &r.ObservationBID,
		&r.Project, &r.TopicKey,
		&signal, &r.DetectionScore,
		&statusStr,
		&verdictStr, &r.VerdictReason,
		&r.VerdictConfidence,
		&r.VerdictModel, &r.VerdictSessionID,
		&createdAt, &resolvedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Row{}, ErrNotFound
		}
		return Row{}, fmt.Errorf("scan conflict %d: %w", id, err)
	}
	r.Status = Status(statusStr)
	r.Verdict = Verdict(verdictStr)
	r.DetectionSignal = signal
	if t, err := parseTime(createdAt); err == nil {
		r.CreatedAt = t
	}
	if strings.TrimSpace(resolvedAt) != "" {
		if t, err := parseTime(resolvedAt); err == nil {
			r.ResolvedAt = t
		}
	}
	return r, nil
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("no time layout matches %q", s)
}
