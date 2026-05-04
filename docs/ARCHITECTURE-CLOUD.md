[← Back to README](../README.md)

# ARIA Core — Cloud architecture (v2026.04+)

The original [`ARCHITECTURE.md`](ARCHITECTURE.md) describes the engram-era
**memory pipeline** (mem_save / mem_search / sessions). This doc covers the
**cloud platform** that aria-core is today: a Postgres-backed dashboard,
multi-agent /Historias chain, vault, cotizador, and skills governance.

- [Component map](#component-map)
- [Data flow — write path](#data-flow--write-path)
- [Data flow — read path](#data-flow--read-path)
- [Auth model](#auth-model)
- [Skills governance pipeline](#skills-governance-pipeline)
- [Multi-agent /Historias chain](#multi-agent-historias-chain)
- [Observability](#observability)
- [Deployment topology](#deployment-topology)

---

## Component map

```
┌──────────────────────────────────────────────────────────────────────────┐
│                        Clients (per dev)                                 │
│                                                                          │
│   Claude Desktop · Code · CLI · OpenCode plugin · Cursor · VS Code      │
│                            │                                             │
│                            │ MCP (stdio) or HTTP REST                    │
│                            ▼                                             │
│                ┌───────────────────────────┐                             │
│                │   aria-core binary        │                             │
│                │   (35 MB, pure Go)        │                             │
│                │                           │                             │
│                │   • mcp                  │                             │
│                │   • setup <agent>         │                             │
│                │   • cloud serve           │                             │
│                │   • skills validate/lock  │                             │
│                │   • doctor                │                             │
│                │   • admin skill-maintainer│                             │
│                │   • vault create/get/...  │                             │
│                └───────────────────────────┘                             │
└──────────────────────────────────────────────────────────────────────────┘
                            │
                            │  HTTPS (Cloudflare → Nginx → 127.0.0.1:18080)
                            ▼
┌──────────────────────────────────────────────────────────────────────────┐
│                        VPS (root@77.68.72.141)                           │
│                                                                          │
│  ┌─────────────────────────────────────────────────────────────┐        │
│  │  cloudserver.CloudServer                                     │        │
│  │                                                              │        │
│  │  ┌──────────────┐  ┌──────────────┐  ┌──────────────────┐  │        │
│  │  │ /sync/*      │  │ /v1/*        │  │ /dashboard/*     │  │        │
│  │  │  pull/push   │  │  REST API    │  │  templ + HTMX    │  │        │
│  │  └──────────────┘  └──────────────┘  └──────────────────┘  │        │
│  │       │                  │                    │             │        │
│  │       │                  ▼                    ▼             │        │
│  │       │          ┌───────────────┐    ┌─────────────────┐  │        │
│  │       │          │ MCP gateway   │    │ Dashboard       │  │        │
│  │       │          │ (aria_*)      │    │ handlers        │  │        │
│  │       │          └───────────────┘    └─────────────────┘  │        │
│  │       │                  │                    │             │        │
│  │       └──────────────────┼────────────────────┘             │        │
│  │                          ▼                                   │        │
│  │                  ┌──────────────────┐                       │        │
│  │                  │ Domain services  │                       │        │
│  │                  │                  │                       │        │
│  │                  │ • cotizador      │                       │        │
│  │                  │ • teamprojects   │                       │        │
│  │                  │ • pages + dbs    │                       │        │
│  │                  │ • knowledgebase  │                       │        │
│  │                  │ • vault          │                       │        │
│  │                  │ • recipes        │                       │        │
│  │                  │ • skills/historias│                      │        │
│  │                  │ • cloudusers     │                       │        │
│  │                  └──────────────────┘                       │        │
│  └────────────────────────┬─────────────────────────────────────┘       │
│                           ▼                                              │
│                ┌────────────────────┐                                    │
│                │ Postgres 17        │                                    │
│                │  aria_core_cloud   │                                    │
│                └────────────────────┘                                    │
│                                                                          │
│  ┌──────────────┐    ┌──────────────┐    ┌─────────────────┐            │
│  │ Gotenberg    │    │ Ollama       │    │ Backups (cron)  │            │
│  │  PDF render  │    │  Gemma local │    │  /var/backups   │            │
│  └──────────────┘    └──────────────┘    └─────────────────┘            │
└──────────────────────────────────────────────────────────────────────────┘
```

---

## Data flow — write path

The canonical example: a developer running Claude Code captures a decision.

```
1. Claude finishes work and decides to call aria_save (skill protocol).
2. The MCP plugin (OpenCode/Claude Code) invokes the aria-core binary
   with the tool call.
3. The binary (running locally as `aria-core mcp`) authenticates the
   request against ~/.aria-core/jwt and forwards to cloud over HTTPS.
4. cloudserver routes /v1/memory/save → aria_mem service → Postgres.
5. The same handler runs scrub gates: PII redaction, sensitivity tagging,
   and (for client_knowledge scope) extra placeholders before any
   downstream LLM egress.
6. Postgres INSERT returns the observation_id; the response goes back
   to the agent so it can reference the artifact in subsequent calls.
```

Three things happen automatically at the cloud edge:

- **Audit log**: every `aria_save` call lands in `aria_observations` with
  `created_by_uid + created_at + project + scope`.
- **Egress logging**: any subsequent LLM call that reads this row is
  logged in `aria_llm_egress_log` (channel router writes the row before
  the LLM sees the data).
- **Mutation queue**: outbound mutations to other devs are queued in
  `aria_sync_mutations` and pulled by their local clients (eventual
  consistency).

## Data flow — read path

```
1. Claude calls aria_search "checkout flow refactor".
2. MCP gateway forwards to /v1/memory/search.
3. Postgres FTS5 (Spanish dictionary) returns ranked observations,
   filtered by the caller's scope/role + project authorizer.
4. The handler applies a token budget (default 4096) and progressive
   disclosure: titles + topic_keys first, full bodies only if requested.
5. Returns to the agent, which decides whether to expand a row via
   aria_get.
```

For the dashboard:

```
1. Browser GET /dashboard/projects?auth=ok → cloudserver
2. RequireSession middleware validates the dashboard JWT cookie.
3. handler calls Store.ListProjects(query)  → Postgres
4. templ renders ProjectsPage component → HTML
5. Layout wraps in app-shell > sidebar > main, with HTMX wiring
   for tab swaps.
```

---

## Auth model

Three credentials, three contexts:

| Credential | Issued by | Used for | Stored as |
|---|---|---|---|
| `bearer_token` | `aria-core admin create-token` | Sync/REST API auth | Header `Authorization: Bearer …` |
| Dashboard JWT | `dashboardsession.NewCodec` | Browser session cookie | `aria_session` cookie, HS256, 8h TTL |
| Admin recovery token | `WithDashboardAdminToken` | Last-resort dashboard login | POST `token=…` to `/dashboard/login` |

Permissive paths:

- `WithInsecureMode()` — single-tenant sandbox / local dev only. Disables
  all dashboard auth. NEVER used in production.
- Public share links — `/p/<token>` route, gated by `aria_page_share_links`
  table (token + optional expiry + optional password).

5 roles in `cloud_users.roles` (a user can have several):

| Role | User CRUD | Cotizador | Vault | Projects | Memory |
|---|---|---|---|---|---|
| `admin` | ✓ | ✓ | ✓ | ✓ | ✓ |
| `agent` | ✗ | ✓ | read | ✓ | ✓ + cross-personal read |
| `cotizador` | ✗ | ✓ | read | ✗ | ✓ |
| `project_admin` | ✗ | ✗ | scope project | ✓ (asignados) | ✓ |
| `dev` | ✗ | ✗ | scope personal | member | ✓ |

---

## Skills governance pipeline

```
Author → SKILL.md edit → local validate → PR → CI gate → CODEOWNERS review
                                                                    │
                                                                    ▼
                          MANIFEST.yaml lockfile (sha256 per skill)
                                                                    │
                                                                    ▼
                                                Branch protection on `main`
                                                                    │
                                                                    ▼
                                                  Merged → catalog rebuild
```

Five enforcement layers (introduced incrementally, all live):

1. **JSON Schema 2020-12** at `skills/schema/aria-skill-v1.schema.json` —
   validates frontmatter shape (name, version semver, agent flag, etc.).
2. **`aria-core skills validate`** (CI gated) — runs the schema check
   plus body-section soft rules.
3. **`aria-core skills lock` + `check-drift`** — `MANIFEST.yaml` records
   sha256 of every SKILL.md; CI rejects unlocked changes.
4. **`.github/CODEOWNERS`** — `skills/**` requires review from a
   maintainer registered in `aria_skill_maintainers` (Postgres table,
   managed via `aria-core admin skill-maintainer-add/list/revoke`).
5. **Branch protection on `main`** with `Skills Validate` + `Snapshot Build`
   as required status checks + 1 approving review.

## Multi-agent /Historias chain

The /Historias protocol composes multiple agent-skills as a sub-agent
chain, persisting each step's artifact under `/Historias/<slug>/<N>-<short>.md`.

```
slug=checkout-rewrite

Position 0  →  office-hours       writes  0-office-hours.md
Position 1  →  ceo-plan-review    writes  1-ceo-plan.md
Position 2  →  eng-plan-review    writes  2-eng-plan.md
Position 3  →  story-writer       writes  3-story.md
Position 4  →  retro              writes  4-retro.md
```

Each artifact is mirrored to `aria_pages` for search + dashboard rendering.
Source of truth stays on disk so the chain is audited via git.

See [AGENT-SKILL-AUTHORING.md](AGENT-SKILL-AUTHORING.md) for the full
contract and [CONTRIBUTING-SKILLS.md](CONTRIBUTING-SKILLS.md) for the
contributor workflow.

---

## Observability

- **Structured logs**: `internal/obs.L()` returns the package slog logger
  (JSON or text via `ARIA_LOG_FORMAT`). Every HTTP request gets a
  `request_id` via `obs.WithRequestID` middleware that propagates into
  context-bound loggers.
- **No metrics endpoint yet** — Prometheus / OpenTelemetry traces are
  on the roadmap. For now, debugging relies on `journalctl -u aria-core -f`
  on the VPS.
- **Audit dashboards**: `/dashboard/audit/egress` (every LLM call) and
  `/dashboard/admin/audit-log` (admin actions).

## Deployment topology

```
Cloudflare (Zone itechdev.com.mx)
   │
   │ ariacore.itechdev.com.mx  →  77.68.72.141:443
   │
   ▼
Nginx (TLS termination)  →  127.0.0.1:18080  →  aria-core systemd unit
                                                       │
                                                       ▼
                                       Postgres (127.0.0.1:5432)
                                       Gotenberg (127.0.0.1:3001, docker)
                                       Ollama (127.0.0.1:11434, optional)
```

Backups: nightly `pg_dump` to `/var/backups/aria-core`, retained 30 days.
Master vault key in `/etc/systemd/system/aria-core.service.d/override.conf`
(chmod 600, not in repo, backed up to 1Password).

Deploy command (from any dev box with SSH access):

```bash
ssh root@77.68.72.141 'cd /root/aria-core && git pull origin main && \
  /root/go/bin/templ generate && \
  go build -o /tmp/aria-core-new ./cmd/aria-core && \
  systemctl stop aria-core && \
  cp /tmp/aria-core-new /usr/local/bin/aria-core && \
  systemctl start aria-core'
```

For end-user installs: `brew install ITECHDEV-MX/homebrew-tap/aria-core`
or `go install github.com/ITECHDEV-MX/aria-core/cmd/aria-core@latest`.
