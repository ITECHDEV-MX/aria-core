# Exploration — Dashboard /Historias view (F5)

**Date**: 2026-05-04

## Findings

- F2 already shipped /Historias/<slug>/ + MANIFEST.yaml + 4 MCP tools.
- The dashboard uses templ + HTMX. Existing pages: admin_skills,
  cotizador, aria_mem, audit_egress, etc. Convention is one `*.templ`
  + one `*_handlers.go` per surface.
- Existing dashboard.MountConfig has fields like RequireSession,
  ValidateCredentials, KnowledgeBase. Adding HistoriasRoot fits the
  pattern.

## Decisions

- **Read-only view in F5**. No POST/PUT — devs interact via MCP tools,
  the dashboard renders.
- **frameLayoutFn indirection**: keep historias handlers decoupled
  from the rest of dashboard internals. Default impl is minimal HTML
  shell; cloudserver can override to wrap with the standard frame.
- **Truncate body at 5KB** for index display — full body is one click
  away on the file system.
