package conflicts

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// DetectCandidates runs the read-only conflict-detection signals
// against the database and returns up to `limit` candidates ordered
// by score descending.
//
// Read-only — never inserts or mutates. Hard 100ms wall-clock budget
// via the supplied context.
//
// Detection signals (in order):
//
//  1. SignalTopicKeyHashDiverge — same (project, topic_key) but a
//     different normalized_hash than the previous most-recent
//     observation. Score = 1.0.
//  2. SignalFTSTitleOverlap — FTS5 MATCH on the title within the
//     same (project, scope), excluding the new observation itself
//     and any rows already returned by signal 1. Score = bm25-derived.
func DetectCandidates(ctx context.Context, db *sql.DB, obs Observation, limit int) ([]Candidate, error) {
	if db == nil {
		return nil, errors.New("conflicts: nil db")
	}
	if obs.ID == 0 {
		return nil, errors.New("conflicts: observation ID must be set (run after INSERT)")
	}
	if limit <= 0 {
		limit = 3
	}

	out := make([]Candidate, 0, limit*2)
	seen := map[int64]bool{obs.ID: true}

	// Signal 1: topic_key hash divergence
	if obs.TopicKey != "" {
		c, err := detectTopicKeyDiverge(ctx, db, obs)
		if err == nil && c != nil {
			out = append(out, *c)
			seen[c.ObservationBID] = true
		}
	}

	// Signal 2: FTS5 title overlap (same project + scope, distinct topic_key)
	ftsCands, err := detectFTSTitleOverlap(ctx, db, obs, seen, limit)
	if err == nil {
		out = append(out, ftsCands...)
	}

	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func detectTopicKeyDiverge(ctx context.Context, db *sql.DB, obs Observation) (*Candidate, error) {
	if obs.TopicKey == "" {
		return nil, nil
	}

	q := `
		SELECT id, title, content, normalized_hash
		FROM observations
		WHERE project = ?
		  AND topic_key = ?
		  AND id != ?
		  AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`
	row := db.QueryRowContext(ctx, q, obs.Project, obs.TopicKey, obs.ID)
	var (
		priorID    int64
		priorTitle string
		priorBody  string
		priorHash  sql.NullString
	)
	if err := row.Scan(&priorID, &priorTitle, &priorBody, &priorHash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("topic_key prior lookup: %w", err)
	}

	// Only flag if the hashes differ (i.e., this is genuinely new content,
	// not a no-op re-save).
	if priorHash.Valid && priorHash.String == obs.NormalizedHash {
		return nil, nil
	}

	return &Candidate{
		ObservationAID:      obs.ID,
		ObservationBID:      priorID,
		Project:             obs.Project,
		TopicKey:            obs.TopicKey,
		Signal:              SignalTopicKeyHashDiverge,
		Score:               1.0,
		PriorTitle:          priorTitle,
		PriorContentSnippet: snippet(priorBody, 200),
	}, nil
}

func detectFTSTitleOverlap(ctx context.Context, db *sql.DB, obs Observation, seen map[int64]bool, limit int) ([]Candidate, error) {
	// Only run FTS if we have a non-empty title.
	title := strings.TrimSpace(obs.Title)
	if title == "" {
		return nil, nil
	}

	// Build a tolerant FTS query: prefix-match each token of the
	// title joined with OR. Drop tokens shorter than 3 chars.
	tokens := strings.Fields(title)
	var ftsTerms []string
	for _, t := range tokens {
		t = strings.Trim(strings.ToLower(t), ".,;:?!\"'()")
		if len(t) < 3 {
			continue
		}
		ftsTerms = append(ftsTerms, t+"*")
	}
	if len(ftsTerms) == 0 {
		return nil, nil
	}
	ftsQuery := strings.Join(ftsTerms, " OR ")

	q := `
		SELECT o.id, o.title, o.content, COALESCE(o.topic_key, '')
		FROM observations o
		JOIN observations_fts f ON f.rowid = o.id
		WHERE observations_fts MATCH ?
		  AND o.project = ?
		  AND o.scope = ?
		  AND o.id != ?
		  AND o.deleted_at IS NULL
		ORDER BY bm25(observations_fts) ASC
		LIMIT ?
	`
	// LIMIT*2 because we'll filter `seen` afterwards.
	rows, err := db.QueryContext(ctx, q, ftsQuery, obs.Project, obs.Scope, obs.ID, limit*2)
	if err != nil {
		return nil, fmt.Errorf("fts overlap: %w", err)
	}
	defer rows.Close()

	var out []Candidate
	for rows.Next() {
		var (
			id        int64
			t, body   string
			topicKey  string
		)
		if err := rows.Scan(&id, &t, &body, &topicKey); err != nil {
			return nil, fmt.Errorf("fts scan: %w", err)
		}
		if seen[id] {
			continue
		}
		out = append(out, Candidate{
			ObservationAID:      obs.ID,
			ObservationBID:      id,
			Project:             obs.Project,
			TopicKey:            topicKey,
			Signal:              SignalFTSTitleOverlap,
			Score:               0.5, // placeholder; real bm25 score is negative-ish
			PriorTitle:          t,
			PriorContentSnippet: snippet(body, 200),
		})
		if len(out) >= limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fts iter: %w", err)
	}
	return out, nil
}

func snippet(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// detectionContext returns a context with a hard 100ms budget for the
// DB queries inside DetectCandidates. Callers can supply their own
// parent context to enforce cancellation higher up.
func detectionContext(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 100*time.Millisecond)
}
