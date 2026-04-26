package redactor

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"
)

// EgressFilter narrows down rows returned by ListEgress.
type EgressFilter struct {
	From     time.Time
	To       time.Time
	ClientID string // UUID string; empty = no filter
	UserUID  string // UUID string; empty = no filter
	Provider string // exact match (lowercased)
}

// EgressRow is a flattened DB row for the audit/egress dashboard.
type EgressRow struct {
	ID            string
	RequestID     string
	ObservationID string
	LLMProvider   string
	LLMModel      string
	ClientID      string
	UserUID       string
	Scrubbed      bool
	RedactionsRaw string // JSON; UI parses if it wants drill-down
	PayloadHash   string
	PayloadSize   int
	Reason        string
	OccurredAt    time.Time
}

// EgressStats is a small aggregate the CLI / dashboard renders.
type EgressStats struct {
	WindowDays      int
	TotalRequests   int
	TotalScrubbed   int
	TotalBypassed   int
	TotalBlockedConfidential int
	ByProvider      map[string]int
	ByReason        map[string]int
	BytesSent       int64
}

// ListEgress reads rows for the audit dashboard. limit/offset are ints; pass 0
// for default 25 / 0.
func ListEgress(ctx context.Context, db *sql.DB, f EgressFilter, limit, offset int) ([]EgressRow, int, error) {
	if db == nil {
		return nil, 0, nil
	}
	if limit <= 0 {
		limit = 25
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}

	conds := []string{"1=1"}
	args := []any{}
	idx := 1
	if !f.From.IsZero() {
		conds = append(conds, fmt.Sprintf("created_at >= $%d", idx))
		args = append(args, f.From.UTC())
		idx++
	}
	if !f.To.IsZero() {
		conds = append(conds, fmt.Sprintf("created_at <= $%d", idx))
		args = append(args, f.To.UTC())
		idx++
	}
	if v := strings.TrimSpace(f.ClientID); v != "" {
		conds = append(conds, fmt.Sprintf("client_id = $%d::uuid", idx))
		args = append(args, v)
		idx++
	}
	if v := strings.TrimSpace(f.UserUID); v != "" {
		conds = append(conds, fmt.Sprintf("initiated_by_uid = $%d::uuid", idx))
		args = append(args, v)
		idx++
	}
	if v := strings.TrimSpace(strings.ToLower(f.Provider)); v != "" {
		conds = append(conds, fmt.Sprintf("llm_provider = $%d", idx))
		args = append(args, v)
		idx++
	}
	where := strings.Join(conds, " AND ")

	// Count
	var total int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM aria_llm_egress_log WHERE "+where, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("redactor: count egress: %w", err)
	}

	// Page
	args = append(args, limit, offset)
	q := fmt.Sprintf(`
		SELECT id::text, request_id::text, COALESCE(observation_id::text,''),
		       llm_provider, COALESCE(llm_model,''),
		       COALESCE(client_id::text,''), initiated_by_uid::text,
		       scrubbed, redactions::text, COALESCE(payload_hash,''), payload_size,
		       COALESCE(reason,''), created_at
		FROM aria_llm_egress_log
		WHERE %s
		ORDER BY created_at DESC
		LIMIT $%d OFFSET $%d
	`, where, idx, idx+1)
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("redactor: list egress: %w", err)
	}
	defer rows.Close()
	var out []EgressRow
	for rows.Next() {
		var r EgressRow
		if err := rows.Scan(&r.ID, &r.RequestID, &r.ObservationID, &r.LLMProvider, &r.LLMModel,
			&r.ClientID, &r.UserUID, &r.Scrubbed, &r.RedactionsRaw, &r.PayloadHash, &r.PayloadSize,
			&r.Reason, &r.OccurredAt); err != nil {
			return nil, 0, fmt.Errorf("redactor: scan egress: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("redactor: iterate egress: %w", err)
	}
	return out, total, nil
}

// Stats returns a per-window aggregate for the CLI / admin home tile.
func Stats(ctx context.Context, db *sql.DB, days int) (*EgressStats, error) {
	if db == nil {
		return &EgressStats{WindowDays: days, ByProvider: map[string]int{}, ByReason: map[string]int{}}, nil
	}
	if days <= 0 {
		days = 30
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UTC()

	st := &EgressStats{
		WindowDays: days,
		ByProvider: map[string]int{},
		ByReason:   map[string]int{},
	}

	if err := db.QueryRowContext(ctx, `
		SELECT
		  COUNT(*),
		  COUNT(*) FILTER (WHERE scrubbed),
		  COUNT(*) FILTER (WHERE NOT scrubbed),
		  COALESCE(SUM(payload_size), 0)
		FROM aria_llm_egress_log
		WHERE created_at >= $1`, since).
		Scan(&st.TotalRequests, &st.TotalScrubbed, &st.TotalBypassed, &st.BytesSent); err != nil {
		return nil, fmt.Errorf("redactor: stats summary: %w", err)
	}

	rows, err := db.QueryContext(ctx, `
		SELECT llm_provider, COUNT(*) FROM aria_llm_egress_log
		WHERE created_at >= $1 GROUP BY llm_provider
	`, since)
	if err != nil {
		return nil, fmt.Errorf("redactor: stats by provider: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		var c int
		if err := rows.Scan(&p, &c); err != nil {
			return nil, fmt.Errorf("redactor: scan provider stat: %w", err)
		}
		st.ByProvider[p] = c
	}

	rows2, err := db.QueryContext(ctx, `
		SELECT COALESCE(reason,''), COUNT(*) FROM aria_llm_egress_log
		WHERE created_at >= $1 GROUP BY reason
	`, since)
	if err != nil {
		return nil, fmt.Errorf("redactor: stats by reason: %w", err)
	}
	defer rows2.Close()
	for rows2.Next() {
		var r string
		var c int
		if err := rows2.Scan(&r, &c); err != nil {
			return nil, fmt.Errorf("redactor: scan reason stat: %w", err)
		}
		st.ByReason[r] = c
	}
	return st, nil
}

// RevealAlias returns the displayValue for one alias token. Admin-only.
func RevealAlias(ctx context.Context, db *sql.DB, token string) (string, string, error) {
	if db == nil {
		return "", "", fmt.Errorf("redactor: no db wired")
	}
	var entityType, displayValue string
	err := db.QueryRowContext(ctx,
		`SELECT entity_type, display_value FROM aria_redaction_aliases WHERE alias_token = $1`,
		token).Scan(&entityType, &displayValue)
	if err == sql.ErrNoRows {
		return "", "", fmt.Errorf("redactor: alias %q not found", token)
	}
	if err != nil {
		return "", "", fmt.Errorf("redactor: reveal alias: %w", err)
	}
	return entityType, displayValue, nil
}
