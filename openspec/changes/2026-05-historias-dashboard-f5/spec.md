# Spec — F5

## Routes

- `GET /dashboard/historias` — index. Empty state when no slugs.
  Returns 500 if HistoriasRoot is not configured on MountConfig.
- `GET /dashboard/historias/{slug}` — chain view. 404 when slug not
  found, 400 when slug missing, 500 when HistoriasRoot unconfigured.

## Auth

Both routes are session-gated via h.requireSession.

## Body display

Bodies > 5KB are truncated for the dashboard. Full content lives in
`/Historias/<slug>/<file>.md` and via `aria_artifact_get`.
