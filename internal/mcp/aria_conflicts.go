package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/conflicts"
	"github.com/ITECHDEV-MX/aria-core/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerAriaJudge wires the aria_judge MCP tool — agent records a
// verdict on a pending memory conflict surfaced earlier by aria_save
// (or by inspection of the conflicts table).
func registerAriaJudge(srv *server.MCPServer, s *store.Store) {
	srv.AddTool(mcp.NewTool("aria_judge",
		mcp.WithTitleAnnotation("Judge Memory Conflict"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDescription("Record a verdict on a pending memory conflict. Use after aria_save returns a conflicts[] array. Verdicts: supersedes (A replaces B), equivalent (same), conflicting (both valid), dismissed (false alarm)."),
		mcp.WithString("conflict_id", mcp.Required(), mcp.Description("Conflict row ID")),
		mcp.WithString("verdict", mcp.Required(), mcp.Description("supersedes | equivalent | conflicting | dismissed")),
		mcp.WithString("reason", mcp.Description("Free-text rationale")),
		mcp.WithString("confidence", mcp.Description("0.0-1.0 numeric confidence")),
		mcp.WithString("model", mcp.Description("Model name e.g. claude-opus-4-7")),
		mcp.WithString("session_id", mcp.Description("Active session ID for audit")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		idStr := strings.TrimSpace(asString(args, "conflict_id"))
		if idStr == "" {
			return mcp.NewToolResultError("conflict_id is required"), nil
		}
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("conflict_id must be an integer, got %q", idStr)), nil
		}

		verdict := conflicts.Verdict(strings.TrimSpace(asString(args, "verdict")))
		if !verdict.IsValid() {
			return mcp.NewToolResultError(fmt.Sprintf("invalid verdict %q (want: supersedes|equivalent|conflicting|dismissed)", verdict)), nil
		}

		row, err := conflicts.RecordVerdict(ctx, s.DB(), conflicts.RecordVerdictArgs{
			ConflictID: id,
			Verdict:    verdict,
			Reason:     asString(args, "reason"),
			Confidence: asFloat(args, "confidence"),
			Model:      asString(args, "model"),
			SessionID:  asString(args, "session_id"),
		})
		if err != nil {
			if errors.Is(err, conflicts.ErrNotFound) {
				return mcp.NewToolResultError(fmt.Sprintf("conflict %d not found", id)), nil
			}
			if errors.Is(err, conflicts.ErrAlreadyResolved) {
				return mcp.NewToolResultError(fmt.Sprintf("conflict %d already resolved", id)), nil
			}
			return mcp.NewToolResultError(fmt.Sprintf("record verdict: %v", err)), nil
		}

		return jsonResult(row)
	})
}

// registerAriaCompare wires the aria_compare MCP tool — agent
// records a manual semantic verdict between any two observations,
// even when no conflict was previously surfaced. Useful for proactive
// cleanup or to seed the audit log with relationships the detector
// missed.
func registerAriaCompare(srv *server.MCPServer, s *store.Store) {
	srv.AddTool(mcp.NewTool("aria_compare",
		mcp.WithTitleAnnotation("Compare Two Observations"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDescription("Persist a semantic verdict between two observations. Use proactively when noticing duplicates, supersession, or relationships the detector missed. Verdicts: supersedes, equivalent, conflicting, dismissed, related."),
		mcp.WithString("observation_a_id", mcp.Required()),
		mcp.WithString("observation_b_id", mcp.Required()),
		mcp.WithString("verdict", mcp.Required(), mcp.Description("supersedes | equivalent | conflicting | dismissed | related")),
		mcp.WithString("reason"),
		mcp.WithString("confidence"),
		mcp.WithString("model"),
		mcp.WithString("session_id"),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		aStr := strings.TrimSpace(asString(args, "observation_a_id"))
		bStr := strings.TrimSpace(asString(args, "observation_b_id"))
		if aStr == "" || bStr == "" {
			return mcp.NewToolResultError("both observation_a_id and observation_b_id are required"), nil
		}
		a, err := strconv.ParseInt(aStr, 10, 64)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("observation_a_id not an integer: %q", aStr)), nil
		}
		b, err := strconv.ParseInt(bStr, 10, 64)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("observation_b_id not an integer: %q", bStr)), nil
		}

		verdict := conflicts.Verdict(strings.TrimSpace(asString(args, "verdict")))
		if !verdict.IsValid() {
			return mcp.NewToolResultError(fmt.Sprintf("invalid verdict %q", verdict)), nil
		}

		row, err := conflicts.RecordCompare(ctx, s.DB(), conflicts.RecordCompareArgs{
			ObservationAID: a,
			ObservationBID: b,
			Verdict:        verdict,
			Reason:         asString(args, "reason"),
			Confidence:     asFloat(args, "confidence"),
			Model:          asString(args, "model"),
			SessionID:      asString(args, "session_id"),
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("record compare: %v", err)), nil
		}

		return jsonResult(row)
	})
}

// asString safely extracts a string arg from the MCP tool request.
func asString(args map[string]any, key string) string {
	v, ok := args[key].(string)
	if !ok {
		return ""
	}
	return v
}

// asFloat safely extracts a float arg, accepting either a number or
// a stringified number.
func asFloat(args map[string]any, key string) float64 {
	switch v := args[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f
		}
	}
	return 0
}

// jsonResult marshals any value to a tool result with indented JSON.
func jsonResult(v any) (*mcp.CallToolResult, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("marshal: %v", err)), nil
	}
	return mcp.NewToolResultText(string(data)), nil
}
