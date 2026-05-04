# ARIA Core — MCP Subsystem Spec

**Path**: `internal/mcp/`

## Purpose

Expose ARIA Core's behavior over the Model Context Protocol (stdio
transport) so any MCP-compatible agent (Claude Code, Cursor, VS Code,
Codex, Gemini CLI, OpenCode, Windsurf) can save/retrieve memory,
manage sessions, and invoke quote/kb pipelines.

## Surface

15 tools today, organized into 3 profiles:

- **`agent`** (11 tools) — read-heavy + curated writes for normal use.
- **`admin`** (4 tools) — destructive curation tools.
- **`all`** (15 tools) — superset.

Tool naming is `aria_*` (not `mem_*`). The `aria-mcp-protocol` skill
documents the naming and call patterns expected of agents.

## Invariants

1. **Stdio transport only**. No HTTP MCP server. Cloud access goes
   through `aria-core mcp --profile=aria` which calls REST after JWT
   login.
2. **Project resolution**: cwd-detected from git remote → repo root →
   directory basename, OR overridden by `.aria-core/config.json` at the
   repo root with a `project_name`.
3. **Ambiguous-project recovery** matches engram's pattern: if the MCP
   process starts in a parent dir with multiple repos, write tools
   return `ambiguous_project` and require explicit user-selected
   `project` + `project_choice_reason: user_selected_after_ambiguous_project`.
4. **No raw tool-call firehose**. Auto-capture is opt-in per `aria_save`
   call (`capture_prompt: true` default).

## Out of scope

- Real-time streaming
- Server-Sent Events
- Multi-agent broadcast (handled at cloud server, not MCP)
