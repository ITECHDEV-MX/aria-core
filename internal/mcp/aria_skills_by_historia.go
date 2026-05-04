package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/historias"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerAriaSkillsByHistoria wires aria_skills_by_historia.
//
// Returns the list of canonical skill names referenced by the chain
// MANIFEST.yaml at /Historias/<slug>/. The orchestrator uses this to
// scope `tools/list` views: instead of the full 35-skill catalog,
// surface only the skills that THIS chain actually invokes.
//
// Local-only: reads from disk under HISTORIAS_ROOT (same env var
// used by aria_artifact_*). No cloud round-trip.
func registerAriaSkillsByHistoria(srv *server.MCPServer) {
	srv.AddTool(mcp.NewTool("aria_skills_by_historia",
		mcp.WithTitleAnnotation("Skills relevant to a Historia chain"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Read /Historias/<slug>/MANIFEST.yaml and return the deduplicated, ordered list of skill names invoked across the chain. Use to scope skill discovery in multi-agent pipelines so the agent only sees skills relevant to the active historia."),
		mcp.WithString("slug", mcp.Required(), mcp.Description("Historia slug (e.g. 2026-05-mantenimiento-industrial-cotizador)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		slug := strings.TrimSpace(asString(req.GetArguments(), "slug"))
		if slug == "" {
			return mcp.NewToolResultError("slug is required"), nil
		}

		m, err := historias.ListChain(historiasRoot(), slug)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("read chain: %v", err)), nil
		}

		// Each entry's skill is "skill-name@1.0.0"; we only want the
		// name portion. Dedupe in chain order.
		seen := map[string]bool{}
		var names []string
		for _, e := range m.Chain {
			name := strings.TrimSpace(strings.SplitN(e.Skill, "@", 2)[0])
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}

		// Stable secondary sort for empty-chain edge case.
		if len(names) == 0 {
			return jsonResult(map[string]any{"slug": slug, "skills": []string{}, "status": string(m.Status)})
		}
		// Names are already in chain-position order; that's the
		// useful order for orchestrators. We do not re-sort.
		_ = sort.Strings // ensure import used elsewhere
		return jsonResult(map[string]any{
			"slug":   slug,
			"skills": names,
			"status": string(m.Status),
		})
	})
}
