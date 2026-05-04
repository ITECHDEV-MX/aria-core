# ARIA Core — Cloud Subsystem Spec

**Path**: `internal/cloud/`

## Purpose

Multi-tenant, multi-user shared memory + skills + dashboard. Postgres
as source of truth. Web dashboard with templ + HTMX. JWT auth with
session.json on client.

## Subpackages

- `cloudstore` — Postgres data layer (tables: aria_observations,
  aria_skills, aria_pages, aria_secrets, cotizador_*, etc.).
- `cloudserver` — HTTP API, auth middleware, route handlers.
- `dashboard` — server-rendered HTML with templ + HTMX.
- `autosync` — background sync local SQLite ↔ cloud Postgres.
- `redactor` — PII redaction before any external LLM call.
- `roi` — TTC/RDR/CWR/SVR/DTT savings metrics.
- `capture` — passive capture from Slack/Hermes/etc.

## Invariants

1. **Postgres is source of truth** for team-shared data; SQLite local is
   a personal mirror.
2. **JWT HS256, 8h TTL**. Session lives in `~/.aria-core/session.json`.
3. **Vault master key never crosses the wire** — secrets are decrypted
   on the cloud server, never returned to clients.
4. **Redactor runs unconditionally** before any LLM call.
5. **Tenant isolation in HTTP**: every handler validates tenant scope
   before reading or writing.

## Public URL

`https://ariacore.itechdev.com.mx` (Cloudflare-fronted, Nginx →
`127.0.0.1:18080` `aria-core cloud serve`).
