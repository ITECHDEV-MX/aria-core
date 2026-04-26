// Code generated manually for knowledge_base.templ — DO NOT EDIT directly.
// To regenerate: run `go tool templ generate` once templ tooling is wired.
// This hand-written version uses templ.ComponentFunc + io.WriteString for
// simplicity (avoids the boilerplate the official generator emits).

package dashboard

import (
	"context"
	"fmt"
	"html"
	"io"

	"github.com/a-h/templ"
)

// KnowledgeBasePage es el shell admin del módulo wave 8.
func KnowledgeBasePage(degraded bool) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		if _, err := io.WriteString(w, `<div class="main-header">`+
			`<div><p class="section-kicker">WAVE 8 / KNOWLEDGE BASE</p>`+
			`<h1>📚 Knowledge base sync</h1>`+
			`<p class="lead">PRDs, historias y cotizaciones del equipo se sincronizan automáticamente al repo central <code>ITECHDEV-MX/team-knowledge-base</code>. Esta vista muestra el estado del tracking + permite re-sync manual.</p>`+
			`</div></div>`); err != nil {
			return err
		}
		if degraded {
			if _, err := io.WriteString(w,
				`<div class="card" style="border-left:4px solid #d35;background:#fff8f8">`+
					`<strong>Modo degradado:</strong> El servicio knowledge-base no tiene un cliente GitHub configurado. Sólo se mostrará tracking histórico; los re-syncs fallarán hasta que se inyecte un GitHub client al cloudserver.`+
					`</div>`); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w,
			`<div style="margin-top:1rem;display:flex;gap:0.5rem;flex-wrap:wrap">`+
				`<form hx-post="/dashboard/knowledge-base/resync-failed" hx-target="#kb-content" hx-swap="innerHTML" hx-confirm="¿Re-sincear todas las entidades fallidas?">`+
				`<button type="submit" class="shell-button" style="background:#d35;color:#fff">↻ Re-sync failed</button>`+
				`</form>`+
				`<form hx-post="/dashboard/knowledge-base/refresh-index" hx-target="#kb-content" hx-swap="innerHTML">`+
				`<button type="submit" class="shell-button">🗂 Refresh root index</button>`+
				`</form>`+
				`</div>`+
				`<div style="margin-top:1rem">`+
				`<form id="kb-filters" hx-get="/dashboard/knowledge-base/list" hx-target="#kb-content" hx-swap="innerHTML" hx-trigger="change">`+
				`<div style="display:grid;grid-template-columns:1fr 1fr 1fr 1fr;gap:0.5rem">`+
				`<label style="margin:0"><span class="muted">Tipo</span>`+
				`<select name="entity_type"><option value="">(todos)</option>`+
				`<option value="prd">PRD</option><option value="historia">Historia</option>`+
				`<option value="cotizacion">Cotización</option>`+
				`<option value="project_readme">Project README</option>`+
				`<option value="root_index">Root index</option></select></label>`+
				`<label style="margin:0"><span class="muted">Status</span>`+
				`<select name="status"><option value="">(todos)</option>`+
				`<option value="ok">ok</option><option value="pending">pending</option>`+
				`<option value="failed">failed</option><option value="skipped">skipped</option></select></label>`+
				`<label style="margin:0"><span class="muted">Project ID</span><input type="text" name="project_id" placeholder="UUID opcional"/></label>`+
				`<label style="margin:0"><span class="muted">Limit</span><input type="number" name="limit" value="100" min="1" max="500"/></label>`+
				`</div></form></div>`+
				`<div id="kb-content" hx-get="/dashboard/knowledge-base/list" hx-trigger="load" hx-swap="innerHTML">`+
				`<p class="muted" style="margin-top:1rem">Cargando entidades sincronizadas...</p>`+
				`</div>`)
		return err
	})
}

// KnowledgeBaseListPartial pinta la tabla del estado actual + status badges.
func KnowledgeBaseListPartial(entries []KBSyncedEntityView, stats KBStatusView) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		if err := KnowledgeBaseStatusBadge(stats).Render(ctx, w); err != nil {
			return err
		}
		if len(entries) == 0 {
			return EmptyState("Sin sync activo", "Aún no se ha sincronizado ninguna entidad al repo central.").Render(ctx, w)
		}
		if _, err := io.WriteString(w, `<table class="data-table" style="margin-top:0.75rem"><thead><tr>`+
			`<th>Tipo</th><th>Path en GitHub</th><th>Project</th><th>Status</th><th>Último sync</th><th>Acciones</th>`+
			`</tr></thead><tbody>`); err != nil {
			return err
		}
		for _, e := range entries {
			if _, err := io.WriteString(w, `<tr><td><span class="badge badge-muted">`+html.EscapeString(e.EntityType)+`</span></td><td>`); err != nil {
				return err
			}
			if e.GitHubURL != "" {
				if _, err := io.WriteString(w,
					`<a href="`+html.EscapeString(e.GitHubURL)+`" target="_blank" rel="noopener"><code>`+html.EscapeString(e.RepoPath)+`</code></a>`); err != nil {
					return err
				}
			} else {
				if _, err := io.WriteString(w, `<code>`+html.EscapeString(e.RepoPath)+`</code>`); err != nil {
					return err
				}
			}
			if e.EntityID != "" {
				if _, err := io.WriteString(w, `<br/><small class="muted">`+html.EscapeString(e.EntityID)+`</small>`); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, `</td><td>`); err != nil {
				return err
			}
			if e.ProjectID != "" {
				if _, err := io.WriteString(w, `<small>`+html.EscapeString(e.ProjectID)+`</small>`); err != nil {
					return err
				}
			} else {
				if _, err := io.WriteString(w, `<small class="muted">—</small>`); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, `</td><td>`); err != nil {
				return err
			}
			if err := kbStatusBadgeHTML(w, e.SyncStatus); err != nil {
				return err
			}
			if e.LastError != "" {
				if _, err := io.WriteString(w, `<br/><small style="color:#a22">`+html.EscapeString(truncateString(e.LastError, 80))+`</small>`); err != nil {
					return err
				}
			}
			ts := e.LastSyncedAt.Format("2006-01-02 15:04")
			if _, err := io.WriteString(w, `</td><td><small>`+html.EscapeString(ts)+`</small></td><td>`); err != nil {
				return err
			}
			if e.EntityType == "cotizacion" && e.EntityID != "" {
				if _, err := io.WriteString(w,
					`<form hx-post="/dashboard/knowledge-base/quotes/`+html.EscapeString(e.EntityID)+`/sync" hx-target="#kb-content" hx-swap="innerHTML" style="display:inline">`+
						`<button type="submit" class="shell-button">↻ Re-sync</button></form>`); err != nil {
					return err
				}
			}
			if e.ProjectID != "" {
				if _, err := io.WriteString(w,
					`<form hx-post="/dashboard/knowledge-base/projects/`+html.EscapeString(e.ProjectID)+`/resync" hx-target="#kb-content" hx-swap="innerHTML" style="display:inline;margin-left:4px">`+
						`<button type="submit" class="shell-button">↻ proyecto</button></form>`); err != nil {
					return err
				}
			}
			if _, err := io.WriteString(w, `</td></tr>`); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w, `</tbody></table>`)
		return err
	})
}

// KnowledgeBaseStatusBadge pinta los counts globales arriba de la tabla.
func KnowledgeBaseStatusBadge(stats KBStatusView) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, fmt.Sprintf(
			`<div style="display:flex;gap:0.75rem;margin-top:0.75rem;flex-wrap:wrap">`+
				`<span class="badge" style="background:#3a5;color:#fff">ok: %d</span>`+
				`<span class="badge" style="background:#f5c842;color:#222">pending: %d</span>`+
				`<span class="badge" style="background:#d35;color:#fff">failed: %d</span>`+
				`<span class="badge badge-muted">skipped: %d</span>`+
				`<span class="badge badge-muted">total: %d</span>`+
				`</div>`,
			stats.OK, stats.Pending, stats.Failed, stats.Skipped, stats.Total,
		))
		return err
	})
}

// KnowledgeBaseSyncResult pinta el resultado de un sync manual de cotización.
func KnowledgeBaseSyncResult(commit, path string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w,
			`<div class="card" style="background:#eaf6ea;border-left:4px solid #3a5;margin-top:0.5rem">`+
				`<strong>✓ Sincronizada</strong><br/>`+
				`<small>commit: <code>`+html.EscapeString(commit)+`</code></small><br/>`+
				`<small>path: <code>`+html.EscapeString(path)+`</code></small>`+
				`</div>`)
		return err
	})
}

// KnowledgeBaseQuoteCard se inserta en la página de cotización para mostrar
// botones export DOCX + sync to KB + estado actual.
func KnowledgeBaseQuoteCard(quoteID string, synced bool, commitSHA, repoPath string) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		if _, err := io.WriteString(w,
			`<div class="card" style="margin-top:1rem">`+
				`<h3 style="margin-top:0">📚 Knowledge base</h3>`); err != nil {
			return err
		}
		if synced {
			if _, err := io.WriteString(w,
				`<p class="muted">Última sincronización: commit <code>`+html.EscapeString(commitSHA)+
					`</code><br/>Path: <code>`+html.EscapeString(repoPath)+`</code></p>`); err != nil {
				return err
			}
		} else {
			if _, err := io.WriteString(w, `<p class="muted">Esta cotización aún no fue sincronizada al repo central.</p>`); err != nil {
				return err
			}
		}
		_, err := io.WriteString(w,
			`<div style="display:flex;gap:0.5rem;flex-wrap:wrap">`+
				`<a href="/v1/cotizador/quotes/`+html.EscapeString(quoteID)+`/export/docx" class="shell-button" download="propuesta.docx">📄 Descargar .docx</a>`+
				`<a href="/v1/cotizador/quotes/`+html.EscapeString(quoteID)+`/export/markdown" class="shell-button" download="propuesta.md">📝 Descargar .md</a>`+
				`<form hx-post="/dashboard/knowledge-base/quotes/`+html.EscapeString(quoteID)+`/sync" hx-target="this" hx-swap="outerHTML" style="display:inline">`+
				`<button type="submit" class="shell-button" style="background:#3265ae;color:#fff">☁ Sincronizar a knowledge base</button>`+
				`</form></div></div>`)
		return err
	})
}

func kbStatusBadgeHTML(w io.Writer, status string) error {
	switch status {
	case "ok":
		_, err := io.WriteString(w, `<span class="badge" style="background:#3a5;color:#fff">`+html.EscapeString(status)+`</span>`)
		return err
	case "failed":
		_, err := io.WriteString(w, `<span class="badge" style="background:#d35;color:#fff">`+html.EscapeString(status)+`</span>`)
		return err
	case "pending":
		_, err := io.WriteString(w, `<span class="badge" style="background:#f5c842;color:#222">`+html.EscapeString(status)+`</span>`)
		return err
	default:
		_, err := io.WriteString(w, `<span class="badge badge-muted">`+html.EscapeString(status)+`</span>`)
		return err
	}
}
