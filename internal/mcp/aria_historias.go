package mcp

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/historias"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// historiasRoot resolves the on-disk root directory for /Historias.
// Defaults to <cwd>/Historias. Override via env HISTORIAS_ROOT.
func historiasRoot() string {
	if root := strings.TrimSpace(getEnv("HISTORIAS_ROOT")); root != "" {
		return root
	}
	abs, err := filepath.Abs("Historias")
	if err != nil {
		return "Historias"
	}
	return abs
}

// registerAriaHistorias wires the four artifact MCP tools.
//
// aria_artifact_save:   write artifact + update MANIFEST
// aria_artifact_get:    read artifact + entry metadata
// aria_artifact_list:   list slugs OR list chain entries for one slug
// aria_artifact_complete: mark chain as completed (sets final_artifact)
func registerAriaHistorias(srv *server.MCPServer) {
	srv.AddTool(mcp.NewTool("aria_artifact_save",
		mcp.WithTitleAnnotation("Save Historia Artifact"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDescription("Persist one artifact in a multi-agent chain under /Historias/<slug>/. Updates MANIFEST.yaml. Idempotent only with overwrite=true."),
		mcp.WithString("slug", mcp.Required()),
		mcp.WithString("position", mcp.Required(), mcp.Description("0-based position")),
		mcp.WithString("filename", mcp.Required(), mcp.Description("e.g. 0-office-hours.md")),
		mcp.WithString("skill", mcp.Required(), mcp.Description("skill-name@version, e.g. office-hours@1.0.0")),
		mcp.WithString("agent_model", mcp.Description("e.g. claude-opus-4-7")),
		mcp.WithString("content", mcp.Required(), mcp.Description("Markdown body of the artifact")),
		mcp.WithString("inputs", mcp.Description("Comma-separated previous artifact filenames consumed")),
		mcp.WithString("duration_ms"),
		mcp.WithString("created_by"),
		mcp.WithString("overwrite", mcp.Description("'true' to allow replacing existing position")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		pos, err := strconv.Atoi(strings.TrimSpace(asString(args, "position")))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("position must be int: %v", err)), nil
		}
		dur, _ := strconv.ParseInt(strings.TrimSpace(asString(args, "duration_ms")), 10, 64)
		var inputs []string
		if raw := strings.TrimSpace(asString(args, "inputs")); raw != "" {
			for _, p := range strings.Split(raw, ",") {
				if p = strings.TrimSpace(p); p != "" {
					inputs = append(inputs, p)
				}
			}
		}

		entry, err := historias.SaveArtifact(historias.SaveArtifactArgs{
			Root:       historiasRoot(),
			Slug:       asString(args, "slug"),
			Position:   pos,
			Filename:   asString(args, "filename"),
			Skill:      asString(args, "skill"),
			AgentModel: asString(args, "agent_model"),
			Content:    asString(args, "content"),
			Inputs:     inputs,
			DurationMs: dur,
			CreatedBy:  asString(args, "created_by"),
			Overwrite:  asString(args, "overwrite") == "true",
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("save artifact: %v", err)), nil
		}
		return jsonResult(entry)
	})

	srv.AddTool(mcp.NewTool("aria_artifact_get",
		mcp.WithTitleAnnotation("Get Historia Artifact"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("Read one artifact from a chain. Returns entry metadata + body."),
		mcp.WithString("slug", mcp.Required()),
		mcp.WithString("position", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		pos, err := strconv.Atoi(strings.TrimSpace(asString(args, "position")))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("position int: %v", err)), nil
		}
		entry, body, err := historias.GetArtifact(historiasRoot(), asString(args, "slug"), pos)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("get: %v", err)), nil
		}
		return jsonResult(map[string]any{"entry": entry, "body": body})
	})

	srv.AddTool(mcp.NewTool("aria_artifact_list",
		mcp.WithTitleAnnotation("List Historia Slugs / Chain"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDescription("If slug omitted, lists all slugs under /Historias/. If slug given, lists chain entries for that slug."),
		mcp.WithString("slug", mcp.Description("Optional. Omit to list all slugs.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		slug := strings.TrimSpace(asString(args, "slug"))
		if slug == "" {
			slugs, err := historias.ListSlugs(historiasRoot())
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("list slugs: %v", err)), nil
			}
			return jsonResult(map[string]any{"slugs": slugs})
		}
		m, err := historias.ListChain(historiasRoot(), slug)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("list chain: %v", err)), nil
		}
		return jsonResult(m)
	})

	srv.AddTool(mcp.NewTool("aria_artifact_complete",
		mcp.WithTitleAnnotation("Mark Chain Completed"),
		mcp.WithReadOnlyHintAnnotation(false),
		mcp.WithDescription("Mark a /Historias chain as completed and record its final artifact filename."),
		mcp.WithString("slug", mcp.Required()),
		mcp.WithString("final_artifact", mcp.Required()),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		err := historias.CompleteChain(historiasRoot(), asString(args, "slug"), asString(args, "final_artifact"))
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("complete: %v", err)), nil
		}
		return jsonResult(map[string]any{"slug": asString(args, "slug"), "status": "completed"})
	})
}

// getEnv is a tiny indirection so tests can stub.
var getEnv = func(key string) string {
	return strings.TrimSpace(envOrEmpty(key))
}
