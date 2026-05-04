# Design — F5

## Templ functions

`HistoriasIndexPage(rows []HistoriaSummary)`:
Table with slug, status badge, step count, created_at, final_artifact,
"Ver" button → /dashboard/historias/{slug}.

`HistoriaDetailPage(slug, manifest, bodies)`:
Section header with status/created/finalized info + ordered list of
chain entries with skill, agent_model, hash, duration, inputs, body
(truncated at 5KB).

## Handler shape

| Handler | Path | Behavior |
|---|---|---|
| handleHistoriasIndex | GET /dashboard/historias | List slugs from HistoriasRoot |
| handleHistoriaDetail | GET /dashboard/historias/{slug} | Read chain + bodies |

Both gated by RequireSession (dashboard convention).

## frameLayoutFn

Var hook so tests stub a plain templ render and production wraps with
the full frame. Avoids importing internal frame helpers into
historias_handlers and breaks circular dependency risk.
