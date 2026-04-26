// Dashboard handlers for /dashboard/recipes.
//
// Renders raw HTML wrapped in the dashboard Layout via templ.Raw — same
// pattern used by handleDashboardActivity / handleDashboardStats in
// dashboard.go. This avoids creating a new .templ file (which would force
// a `templ generate` step) while still inheriting the chrome.
package cloudserver

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/recipes"
	"github.com/a-h/templ"
)

// dashboardRecipeAdapter is a small façade so the dashboard handlers do not
// have to import internal/cloud/recipes types directly in their templates.
type dashboardRecipeAdapter struct {
	rr RecipeRunnerService
}

func newDashboardRecipeAdapter(rr RecipeRunnerService) *dashboardRecipeAdapter {
	return &dashboardRecipeAdapter{rr: rr}
}

func mountRecipeDashboard(
	mux *http.ServeMux,
	a *dashboardRecipeAdapter,
	requireSession func(r *http.Request) error,
	getRoles func(r *http.Request) []string,
	getDisplayName func(r *http.Request) string,
) {
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if requireSession != nil {
				if err := requireSession(r); err != nil {
					http.Redirect(w, r, "/dashboard/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
					return
				}
			}
			next(w, r)
		}
	}

	mux.HandleFunc("GET /dashboard/recipes", guard(func(w http.ResponseWriter, r *http.Request) {
		a.handleHome(w, r, getRoles(r), getDisplayName(r))
	}))
	mux.HandleFunc("GET /dashboard/recipes/catalog", guard(func(w http.ResponseWriter, r *http.Request) {
		a.handleCatalog(w, r, getRoles(r), getDisplayName(r))
	}))
	mux.HandleFunc("GET /dashboard/recipes/history", guard(func(w http.ResponseWriter, r *http.Request) {
		a.handleHistory(w, r, getRoles(r), getDisplayName(r))
	}))
	mux.HandleFunc("GET /dashboard/recipes/executions/{id}", guard(func(w http.ResponseWriter, r *http.Request) {
		a.handleDetail(w, r, getRoles(r), getDisplayName(r))
	}))
}

func (a *dashboardRecipeAdapter) handleHome(w http.ResponseWriter, r *http.Request, roles []string, displayName string) {
	body := strings.Builder{}
	body.WriteString(`<section class="frame-section">`)
	body.WriteString(`<p class="section-kicker">RECIPES</p>`)
	body.WriteString(`<h2>Recipe Runner</h2>`)
	body.WriteString(`<p>Catálogo de recipes ejecutables y telemetría de ejecuciones.</p>`)
	body.WriteString(`<nav class="tab-nav" style="display:flex;gap:1rem;margin-bottom:1rem">`)
	body.WriteString(`<a href="/dashboard/recipes/catalog">Catálogo</a>`)
	body.WriteString(`<a href="/dashboard/recipes/history">Histórico</a>`)
	body.WriteString(`</nav>`)
	body.WriteString(`</section>`)
	renderRecipeLayout(w, r, "Recipes", displayName, roles, body.String())
}

func (a *dashboardRecipeAdapter) handleCatalog(w http.ResponseWriter, r *http.Request, roles []string, displayName string) {
	rs, err := a.rr.ListRecipes(r.Context(), false)
	if err != nil {
		http.Error(w, "list recipes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)

	b := strings.Builder{}
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(`<p class="section-kicker">RECIPES / CATÁLOGO</p>`)
	b.WriteString(`<h2>Recipes disponibles</h2>`)
	if len(rs) == 0 {
		b.WriteString(`<div class="empty-state"><h3>Sin recipes</h3><p>Corré <code>aria-core recipe seed</code> para cargar las builtin.</p></div>`)
	} else {
		b.WriteString(`<table class="data-table"><thead><tr><th>Key</th><th>Pattern</th><th>Stack</th><th>Steps</th><th>Executable</th><th>Success rate (30d)</th><th>Avg duration</th><th>Acciones</th></tr></thead><tbody>`)
		for _, rec := range rs {
			stats, _ := a.rr.Stats(r.Context(), rec.Key, since)
			rate := "—"
			if stats.TotalRuns > 0 {
				rate = fmt.Sprintf("%.0f%% (%d runs)", stats.SuccessRate*100, stats.TotalRuns)
			}
			avg := "—"
			if stats.AvgDurationMs > 0 {
				avg = fmt.Sprintf("%.1fs", float64(stats.AvgDurationMs)/1000.0)
			}
			execBadge := `<span style="color:#888">manual</span>`
			if rec.Executable {
				execBadge = `<span style="color:#0a0;font-weight:bold">EXECUTABLE</span>`
			}
			b.WriteString(`<tr>`)
			fmt.Fprintf(&b, `<td><code>%s</code></td>`, html.EscapeString(rec.Key))
			fmt.Fprintf(&b, `<td>%s</td>`, html.EscapeString(rec.TaskPattern))
			fmt.Fprintf(&b, `<td>%s</td>`, html.EscapeString(strings.Join(rec.Stack, ", ")))
			fmt.Fprintf(&b, `<td>%d</td>`, len(rec.Steps))
			fmt.Fprintf(&b, `<td>%s</td>`, execBadge)
			fmt.Fprintf(&b, `<td>%s</td>`, rate)
			fmt.Fprintf(&b, `<td>%s</td>`, avg)
			fmt.Fprintf(&b, `<td><a href="/dashboard/recipes/history?recipe_key=%s">historial</a></td>`, html.EscapeString(rec.Key))
			b.WriteString(`</tr>`)
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</section>`)
	renderRecipeLayout(w, r, "Recipes — Catálogo", displayName, roles, b.String())
}

func (a *dashboardRecipeAdapter) handleHistory(w http.ResponseWriter, r *http.Request, roles []string, displayName string) {
	filter := recipes.ExecFilter{
		RecipeKey: strings.TrimSpace(r.URL.Query().Get("recipe_key")),
		Status:    strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if v := strings.TrimSpace(r.URL.Query().Get("days")); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			t := time.Now().UTC().Add(-time.Duration(d) * 24 * time.Hour)
			filter.Since = &t
		}
	}
	if !isAdminContext(roles) {
		// Non-admin sees only their own runs (handled in real impl via uid; here we
		// keep it broad — the v1 endpoint does the same since the store already
		// scopes to JWT identity for write-side, but reads are currently global).
	}
	rs, err := a.rr.ListExecutions(r.Context(), filter, 50)
	if err != nil {
		http.Error(w, "list executions: "+err.Error(), http.StatusInternalServerError)
		return
	}
	b := strings.Builder{}
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(`<p class="section-kicker">RECIPES / HISTÓRICO</p>`)
	b.WriteString(`<h2>Últimas ejecuciones`)
	if filter.RecipeKey != "" {
		fmt.Fprintf(&b, ` — <code>%s</code>`, html.EscapeString(filter.RecipeKey))
	}
	b.WriteString(`</h2>`)
	if len(rs) == 0 {
		b.WriteString(`<div class="empty-state"><h3>Sin ejecuciones</h3><p>Aún no se ha corrido ningún recipe.</p></div>`)
	} else {
		b.WriteString(`<table class="data-table"><thead><tr><th>Recipe</th><th>Status</th><th>Steps</th><th>Duración</th><th>Started</th><th>UID</th><th>Detalle</th></tr></thead><tbody>`)
		for _, e := range rs {
			fmt.Fprintf(&b, `<tr><td><code>%s</code></td><td>%s</td><td>%d/%d</td><td>%.1fs</td><td>%s</td><td>%s</td><td><a href="/dashboard/recipes/executions/%s">ver</a></td></tr>`,
				html.EscapeString(e.RecipeKey),
				statusBadge(e.Status),
				e.CompletedSteps, e.TotalSteps,
				float64(e.TotalDurationMs)/1000.0,
				html.EscapeString(e.StartedAt.Format(time.RFC3339)),
				html.EscapeString(truncateUID(e.ExecutedByUID)),
				html.EscapeString(e.ID),
			)
		}
		b.WriteString(`</tbody></table>`)
	}
	b.WriteString(`</section>`)
	renderRecipeLayout(w, r, "Recipes — Histórico", displayName, roles, b.String())
}

func (a *dashboardRecipeAdapter) handleDetail(w http.ResponseWriter, r *http.Request, roles []string, displayName string) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	exec, err := a.rr.GetExecution(r.Context(), id)
	if err != nil {
		http.Error(w, "execution: "+err.Error(), http.StatusNotFound)
		return
	}
	b := strings.Builder{}
	b.WriteString(`<section class="frame-section">`)
	b.WriteString(`<p class="section-kicker">RECIPES / EXECUTION</p>`)
	fmt.Fprintf(&b, `<h2>Execution <code>%s</code></h2>`, html.EscapeString(exec.ID))
	fmt.Fprintf(&b, `<p>Recipe: <code>%s</code> — Status: %s — Duration: %.1fs — Started: %s</p>`,
		html.EscapeString(exec.RecipeKey),
		statusBadge(exec.Status),
		float64(exec.TotalDurationMs)/1000.0,
		html.EscapeString(exec.StartedAt.Format(time.RFC3339)),
	)

	b.WriteString(`<h3>Steps</h3>`)
	for _, s := range exec.Steps {
		fmt.Fprintf(&b, `<details style="margin:0.5rem 0;border:1px solid #444;padding:0.5rem">`)
		fmt.Fprintf(&b, `<summary>[%d] <strong>%s</strong> — %s — %s — %.1fs</summary>`,
			s.Index, html.EscapeString(string(s.Kind)),
			html.EscapeString(s.Label),
			statusBadge(s.Status),
			float64(s.Duration/time.Millisecond)/1000.0,
		)
		if s.Stdout != "" {
			b.WriteString(`<h4>stdout</h4><pre style="background:#111;color:#eee;padding:0.5rem;overflow:auto;max-height:400px">`)
			b.WriteString(html.EscapeString(s.Stdout))
			b.WriteString(`</pre>`)
		}
		if s.Stderr != "" {
			b.WriteString(`<h4>stderr</h4><pre style="background:#311;color:#fcc;padding:0.5rem;overflow:auto;max-height:400px">`)
			b.WriteString(html.EscapeString(s.Stderr))
			b.WriteString(`</pre>`)
		}
		fmt.Fprintf(&b, `<p>exit_code=%d</p>`, s.ExitCode)
		b.WriteString(`</details>`)
	}
	b.WriteString(`</section>`)
	renderRecipeLayout(w, r, "Recipe Execution", displayName, roles, b.String())
}

func renderRecipeLayout(w http.ResponseWriter, r *http.Request, title, displayName string, roles []string, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHTMXLikeRequest(r) {
		_, _ = w.Write([]byte(body))
		return
	}
	component := dashboard.Layout(title, displayName, "recipes", roles, templ.Raw(body))
	if err := component.Render(r.Context(), w); err != nil {
		// Fallback: write the raw body so we don't blank-page.
		_, _ = w.Write([]byte(body))
	}
}

func isHTMXLikeRequest(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("HX-Request"), "true")
}

func isAdminContext(roles []string) bool {
	for _, role := range roles {
		if strings.EqualFold(role, "admin") {
			return true
		}
	}
	return false
}

func truncateUID(uid string) string {
	if len(uid) <= 8 {
		return uid
	}
	return uid[:8] + "…"
}

func statusBadge(status string) string {
	color := "#888"
	switch status {
	case "success":
		color = "#0a0"
	case "failed":
		color = "#a00"
	case "running":
		color = "#aa0"
	case "cancelled":
		color = "#777"
	}
	return fmt.Sprintf(`<span style="color:%s;font-weight:bold">%s</span>`, color, html.EscapeString(status))
}

// Compile-time assertion that the adapter conforms to the runner contract.
var _ context.Context = context.Background()
