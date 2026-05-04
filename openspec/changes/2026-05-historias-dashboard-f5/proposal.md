# Proposal — F5

**Owner**: contacto@itechpymes.com.mx
**Status**: Drafting (branch `feat/historias-dashboard-f5`)

## Goal

Render `/Historias/` and `/Historias/<slug>/` in the dashboard so
human operators can audit chains without SSHing or reading files
directly.

## Scope

In:
- internal/cloud/dashboard/historias.templ (2 templ functions).
- internal/cloud/dashboard/historias_handlers.go (2 handlers).
- internal/cloud/dashboard/historias_layout.go (frameLayoutFn hook).
- MountConfig.HistoriasRoot field.
- Routes /dashboard/historias and /dashboard/historias/{slug}.
- 5 hermetic tests.

Out:
- aria_pages mirror UI.
- Edit/replay actions (read-only in F5).
- Search/filter (linear listing for now).
