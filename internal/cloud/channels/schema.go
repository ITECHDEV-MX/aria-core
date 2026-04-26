package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CallRecord persisted into aria_channel_calls for telemetry/audit.
type CallRecord struct {
	ID                   string
	Channel              string
	Model                string
	PromptSize           int
	ResponseSize         int
	TokensIn             int
	TokensOut            int
	DurationMs           int
	Sensitivity          string
	InitiatedByUID       string
	RelatedChatSessionID string
	CostEstimateUSD      float64
	Error                string
	CreatedAt            time.Time
}

// LogCall inserts a row into aria_channel_calls. Returns the generated UUID.
// Pass db=nil to skip persistence (used by tests).
func LogCall(ctx context.Context, db *sql.DB, rec CallRecord) (string, error) {
	if db == nil {
		return "", nil
	}
	if strings.TrimSpace(rec.InitiatedByUID) == "" {
		// fall-back sentinel — schema requires NOT NULL
		rec.InitiatedByUID = "00000000-0000-0000-0000-000000000000"
	}
	var sessID any
	if v := strings.TrimSpace(rec.RelatedChatSessionID); v != "" {
		sessID = v
	}
	row := db.QueryRowContext(ctx, `
		INSERT INTO aria_channel_calls (
			channel, model, prompt_size, response_size, tokens_in, tokens_out,
			duration_ms, sensitivity, initiated_by_uid, related_chat_session_id,
			cost_estimate_usd, error
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9::uuid, NULLIF($10,'')::uuid, $11, NULLIF($12,'')
		) RETURNING id::text
	`,
		rec.Channel, rec.Model, rec.PromptSize, rec.ResponseSize, rec.TokensIn, rec.TokensOut,
		rec.DurationMs, rec.Sensitivity, rec.InitiatedByUID,
		nullableString(sessID), rec.CostEstimateUSD, rec.Error,
	)
	var id string
	if err := row.Scan(&id); err != nil {
		return "", fmt.Errorf("channels: log call: %w", err)
	}
	return id, nil
}

// nullableString converts an `any` into a string suitable for use with
// $-placeholder NULLIF($,'')::uuid casts.
func nullableString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// EstimateCostUSD returns a rough cost in USD for the call. claude-max-vps is
// effectively free under Max plan but we keep a sentinel of $0.000003/token
// (Sonnet-ish input price) to surface usage curves; gemma-local is free.
func EstimateCostUSD(channel string, tokensIn, tokensOut int) float64 {
	switch channel {
	case ChannelClaudeMax:
		// free under Max plan but surface a synthetic value so the UI can
		// show "what we'd be paying without Max".
		return float64(tokensIn)*0.000003 + float64(tokensOut)*0.000015
	default:
		return 0
	}
}

// ErrLogFailed is returned when a call log row could not be inserted but the
// caller should not treat it as fatal.
var ErrLogFailed = errors.New("channels: log call failed")
