# ARIA Core

> Motor de memoria persistente multi-tenant para agentes de IA.
> Fork de [engram](https://github.com/Gentleman-Programming/engram) — adaptado a la plataforma ARIA de ITECHDEV-MX.

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

ARIA Core es el motor central de memoria y conocimiento detrás de la plataforma ARIA. Sucesor técnico de `aria-global`, construido sobre la base agent-agnostic de engram con extensiones de ITECHDEV-MX:

- **Multi-tenant nativo**: clientes, usuarios, roles, scopes (`project` / `team` / `global` / `client_knowledge`).
- **Conocimiento canónico**: promoción explícita de observaciones a verdad curada (`promote_canon`).
- **Reasoning traces estructurados**: `conclusion` / `insight` / `rejected_alternatives`.
- **Skills de negocio**: CRM, Cotizador, RFP, Corpus, PRD.
- **Agent-agnostic**: Claude Code, OpenCode, Gemini, Codex, Cursor, Windsurf vía MCP.
- **Single binary, sin CGO**: SQLite local + Postgres opcional cloud.

## Estado

`v0.1.0` — fork inicial. Re-branded desde engram, en proceso de migración del modelo de datos y features de `aria-global` (multi-tenancy, canon, recipes, channels, skills CRM).

## Instalación rápida (dev local)

```bash
go build -o /usr/local/bin/aria-core ./cmd/aria-core
aria-core setup claude   # registra MCP server en Claude Code
aria-core tui            # explorador interactivo
```

## Cloud

Servidor central productivo: **`https://ariacore.itechdev.com.mx`** (en aprovisionamiento).

```bash
docker compose -f docker-compose.cloud.yml up
```

ARIA Core es **local-first**: la fuente primaria es el `local SQLite` por proyecto, y la cloud
actúa como capa de **replication/shared access** entre miembros del equipo. Los comandos
de migración cuando alguien rota la cloud:

```bash
aria-core cloud upgrade doctor --project <name>     # diagnostica drift cloud↔local
aria-core cloud upgrade repair --project <name>     # remedia el drift detectado
aria-core cloud upgrade bootstrap --project <name>  # inicializa un proyecto en cloud
aria-core cloud upgrade status --project <name>     # imprime estado del enrolment
```

## Documentación

- [`DOCS.md`](DOCS.md) — referencia técnica completa (heredada de engram, en proceso de actualización)
- [`AGENTS.md`](AGENTS.md) — índice de skills
- [`CONTRIBUTING.md`](CONTRIBUTING.md) — flujo issue-first
- [`docs/ARCHITECTURE.md`](docs/ARCHITECTURE.md) — arquitectura del pipeline de memoria (engram-era)
- [`docs/ARCHITECTURE-CLOUD.md`](docs/ARCHITECTURE-CLOUD.md) — arquitectura cloud actual (v2026.04+)
- [`docs/AGENT-SETUP.md`](docs/AGENT-SETUP.md) — configuración por agente
- [`docs/ARIA-CORE-CLOUD.md`](docs/ARIA-CORE-CLOUD.md) — modo cloud
- [`docs/AGENT-SKILL-AUTHORING.md`](docs/AGENT-SKILL-AUTHORING.md) — protocolo agent-skill (chains)
- [`docs/CONTRIBUTING-SKILLS.md`](docs/CONTRIBUTING-SKILLS.md) — workflow para autorar/modificar skills

## Licencia

MIT — incluye copyright original del proyecto engram (Alan Buscaglia / Gentleman Programming) y modificaciones de ITECHDEV-MX.
