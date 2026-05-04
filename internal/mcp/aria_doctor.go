package mcp

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ITECHDEV-MX/aria-core/internal/doctor"
	"github.com/ITECHDEV-MX/aria-core/internal/store"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerAriaDoctor wires the aria_doctor tool into the MCP server.
//
// Read-only: never writes to the store. Returns the same JSON Report
// as `aria-core doctor --json` so agents and CI share a parser.
func registerAriaDoctor(srv *server.MCPServer, s *store.Store, version string) {
	srv.AddTool(mcp.NewTool("aria_doctor",
		mcp.WithTitleAnnotation("Run Doctor Diagnostics"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Read-only operational diagnostics for the local aria-core store. Returns a Report with 8 health checks: config_dir, db_file, schema_version, core_tables, fts5_index, disk_space, recent_activity, session. Always safe to call."),
		mcp.WithString("format", mcp.Description(`"json" (default) or "text"`)),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		report := doctor.Diagnose(s.DB(), s.Cfg(), version).SortedByName()

		format := "json"
		if v, ok := req.GetArguments()["format"].(string); ok && v != "" {
			format = v
		}

		switch format {
		case "json":
			data, err := json.MarshalIndent(report, "", "  ")
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("marshal report: %v", err)), nil
			}
			return mcp.NewToolResultText(string(data)), nil

		case "text":
			return mcp.NewToolResultText(renderTextReport(report)), nil

		default:
			return mcp.NewToolResultError(fmt.Sprintf("unknown format %q (want json or text)", format)), nil
		}
	})
}

// renderTextReport returns a CLI-style text rendering of a Report,
// kept here so MCP responses with format=text match the CLI shape.
func renderTextReport(r doctor.Report) string {
	out := "ARIA Core Doctor — diagnostic report\n\n"
	out += fmt.Sprintf("  Status: %s\n\n", r.OverallStatus)

	maxName := 0
	for _, c := range r.Checks {
		if len(c.Name) > maxName {
			maxName = len(c.Name)
		}
	}
	for _, c := range r.Checks {
		out += fmt.Sprintf("  [%s] %-*s  %s\n", c.Status, maxName, c.Name, c.Detail)
	}

	totals := map[doctor.Status]int{}
	var totalMs int64
	for _, c := range r.Checks {
		totals[c.Status]++
		totalMs += c.DurationMs
	}
	out += fmt.Sprintf("\n  Issues: %d critical, %d warnings, %d informational\n",
		totals[doctor.StatusError], totals[doctor.StatusWarn], totals[doctor.StatusInfo])
	out += fmt.Sprintf("  Took: %dms\n", totalMs)
	return out
}
