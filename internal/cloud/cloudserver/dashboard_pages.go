// Dashboard handlers for inline databases (table/kanban/gallery/list views) +
// comments sidebar. Renderiza HTML directo (mismo patrón que dashboard_recipes.go)
// para evitar agregar nuevos archivos .templ que requieran `templ generate`.
//
// El agente PAGES expone una pagina con `<div id="page-database-mount" data-page-id="X">`;
// nosotros respondemos a /dashboard/databases/{pageID}/render con el componente
// de la view actual.
package cloudserver

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/comments"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/databases"
	"github.com/a-h/templ"
)

// mountPagesDashboard registra los handlers /dashboard/databases/* y
// /dashboard/comments/*. Llamado desde registerHandlers cuando hay servicios
// inyectados.
func (s *CloudServer) mountPagesDashboard() {
	guard := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if err := s.authorizeDashboardRequest(r); err != nil {
				http.Redirect(w, r, "/dashboard/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
				return
			}
			next(w, r)
		}
	}
	if s.pageDB != nil {
		s.mux.HandleFunc("GET /dashboard/databases/{pageID}/render", guard(s.handleDashboardDatabaseRender))
		s.mux.HandleFunc("GET /dashboard/databases/{pageID}/views/{viewType}", guard(s.handleDashboardDatabaseView))
		s.mux.HandleFunc("POST /dashboard/databases/rows/{rowID}/move", guard(s.handleDashboardDatabaseRowMove))
	}
	if s.pageComments != nil {
		s.mux.HandleFunc("GET /dashboard/comments/{pageID}/sidebar", guard(s.handleDashboardCommentsSidebar))
		s.mux.HandleFunc("GET /dashboard/mentions/sidebar-badge", guard(s.handleDashboardMentionsBadge))
	}
}

// ─── Database render ───────────────────────────────────────────────────────

func (s *CloudServer) handleDashboardDatabaseRender(w http.ResponseWriter, r *http.Request) {
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	db, err := s.pageDB.GetByPage(r.Context(), pageID)
	if err != nil {
		http.Error(w, "database not found: "+err.Error(), http.StatusNotFound)
		return
	}
	views, _ := s.pageDB.ListViews(r.Context(), db.ID)
	viewType := strings.TrimSpace(r.URL.Query().Get("view"))
	if viewType == "" {
		viewType = db.DefaultView
	}
	rows, err := s.pageDB.ListRows(r.Context(), db.ID, databases.ListRowsOpts{Limit: 100})
	if err != nil {
		http.Error(w, "list rows: "+err.Error(), http.StatusInternalServerError)
		return
	}

	body := strings.Builder{}
	body.WriteString(`<section class="frame-section page-database">`)
	body.WriteString(`<div class="db-toolbar" style="display:flex;gap:.75rem;align-items:center;flex-wrap:wrap;margin-bottom:1rem">`)
	body.WriteString(`<nav class="db-view-tabs" style="display:flex;gap:.5rem">`)
	for _, vt := range []string{"table", "kanban", "gallery", "list"} {
		active := ""
		if vt == viewType {
			active = ` style="font-weight:bold;text-decoration:underline"`
		}
		fmt.Fprintf(&body,
			`<a hx-get="/dashboard/databases/%s/views/%s" hx-target="#db-view-body" hx-swap="innerHTML"%s>%s</a>`,
			html.EscapeString(pageID), html.EscapeString(vt), active, vtTitle(vt))
	}
	body.WriteString(`</nav>`)
	for _, v := range views {
		fmt.Fprintf(&body,
			`<a hx-get="/dashboard/databases/%s/views/%s?view_id=%s" hx-target="#db-view-body" style="margin-left:.5rem">[%s]</a>`,
			html.EscapeString(pageID), html.EscapeString(v.ViewType), html.EscapeString(v.ID),
			html.EscapeString(v.Name))
	}
	body.WriteString(`<button class="db-new-row" hx-post="/v1/pages/`)
	body.WriteString(html.EscapeString(pageID))
	body.WriteString(`/database/rows" hx-vals='{"props":{}}' hx-headers='{"Content-Type":"application/json"}' style="margin-left:auto">+ New row</button>`)
	body.WriteString(`</div>`)
	body.WriteString(`<div id="db-view-body">`)
	renderDatabaseView(&body, viewType, db, rows)
	body.WriteString(`</div>`)
	body.WriteString(`</section>`)

	renderPagesLayout(s, w, r, "Database — "+truncate(pageID, 8), body.String())
}

// handleDashboardDatabaseView renderiza sólo el body del view activo (HTMX swap).
func (s *CloudServer) handleDashboardDatabaseView(w http.ResponseWriter, r *http.Request) {
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	viewType := strings.TrimSpace(r.PathValue("viewType"))
	db, err := s.pageDB.GetByPage(r.Context(), pageID)
	if err != nil {
		http.Error(w, "database not found", http.StatusNotFound)
		return
	}
	rows, err := s.pageDB.ListRows(r.Context(), db.ID, databases.ListRowsOpts{Limit: 200})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	b := strings.Builder{}
	renderDatabaseView(&b, viewType, db, rows)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// handleDashboardDatabaseRowMove maneja drag-drop entre kanban columns:
// updatea el prop group_by_key con el nuevo valor + sort_order.
func (s *CloudServer) handleDashboardDatabaseRowMove(w http.ResponseWriter, r *http.Request) {
	rowID := strings.TrimSpace(r.PathValue("rowID"))
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	groupKey := strings.TrimSpace(r.FormValue("group_key"))
	groupValue := strings.TrimSpace(r.FormValue("group_value"))
	if groupKey == "" {
		http.Error(w, "group_key required", http.StatusBadRequest)
		return
	}
	patch := map[string]any{groupKey: groupValue}
	if _, err := s.pageDB.UpdateRowProps(r.Context(), rowID, patch); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// renderDatabaseView dispatcha al renderer adecuado por viewType.
func renderDatabaseView(b *strings.Builder, viewType string, db *databases.Database, rows []*databases.Row) {
	switch viewType {
	case "kanban":
		renderKanbanView(b, db, rows)
	case "gallery":
		renderGalleryView(b, db, rows)
	case "list":
		renderListView(b, db, rows)
	default:
		renderTableView(b, db, rows)
	}
}

func renderTableView(b *strings.Builder, db *databases.Database, rows []*databases.Row) {
	b.WriteString(`<table class="db-table data-table">`)
	b.WriteString(`<thead><tr>`)
	for _, def := range db.Schema {
		fmt.Fprintf(b, `<th>%s</th>`, html.EscapeString(def.Name))
	}
	b.WriteString(`<th>Actions</th></tr></thead><tbody>`)
	for _, row := range rows {
		fmt.Fprintf(b, `<tr data-row-id="%s">`, html.EscapeString(row.ID))
		for _, def := range db.Schema {
			fmt.Fprintf(b, `<td>%s</td>`, formatPropValue(def, row.Props[def.Key]))
		}
		fmt.Fprintf(b,
			`<td><button hx-delete="/v1/database-rows/%s" hx-confirm="Delete row?" hx-target="closest tr" hx-swap="outerHTML">×</button></td>`,
			html.EscapeString(row.ID))
		b.WriteString(`</tr>`)
	}
	if len(rows) == 0 {
		fmt.Fprintf(b, `<tr><td colspan="%d" class="empty-state">No rows yet.</td></tr>`, len(db.Schema)+1)
	}
	b.WriteString(`</tbody></table>`)
}

func renderKanbanView(b *strings.Builder, db *databases.Database, rows []*databases.Row) {
	groupKey := firstSelectKey(db.Schema)
	if groupKey == "" {
		b.WriteString(`<div class="empty-state"><p>Kanban requires at least one <code>select</code> property in the schema.</p></div>`)
		return
	}
	groups := databases.GroupRowsByKey(rows, groupKey)
	options := optionsForKey(db.Schema, groupKey)
	if len(options) == 0 {
		options = []string{""}
	}
	titleKey := firstTextKey(db.Schema)

	b.WriteString(`<div class="db-kanban" style="display:flex;gap:1rem;overflow-x:auto">`)
	for _, opt := range options {
		bucket := groups[opt]
		fmt.Fprintf(b, `<div class="kanban-col" data-group-value="%s" style="min-width:240px;background:#1b1b1b;padding:.5rem;border-radius:6px">`, html.EscapeString(opt))
		fmt.Fprintf(b, `<h4 style="margin:0 0 .5rem 0">%s <small style="color:#888">(%d)</small></h4>`, html.EscapeString(opt), len(bucket))
		for _, r := range bucket {
			title := ""
			if titleKey != "" {
				if v, ok := r.Props[titleKey].(string); ok {
					title = v
				}
			}
			if title == "" {
				title = "(untitled)"
			}
			fmt.Fprintf(b,
				`<div class="kanban-card" data-row-id="%s" draggable="true" style="background:#2a2a2a;padding:.5rem;margin-bottom:.5rem;border-radius:4px;cursor:grab">`,
				html.EscapeString(r.ID))
			fmt.Fprintf(b, `<strong>%s</strong>`, html.EscapeString(title))
			b.WriteString(`</div>`)
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	// JS minimal para drag-drop con HTMX trigger
	fmt.Fprintf(b, `<script>(function(){
	const cols = document.querySelectorAll('.kanban-col');
	const groupKey = %q;
	cols.forEach(col => {
		col.addEventListener('dragover', e => e.preventDefault());
		col.addEventListener('drop', e => {
			e.preventDefault();
			const rowID = e.dataTransfer.getData('text/plain');
			const groupValue = col.dataset.groupValue;
			fetch('/dashboard/databases/rows/' + rowID + '/move', {
				method: 'POST',
				headers: {'Content-Type': 'application/x-www-form-urlencoded'},
				body: 'group_key=' + encodeURIComponent(groupKey) + '&group_value=' + encodeURIComponent(groupValue)
			}).then(() => location.reload());
		});
	});
	document.querySelectorAll('.kanban-card').forEach(card => {
		card.addEventListener('dragstart', e => e.dataTransfer.setData('text/plain', card.dataset.rowId));
	});
})();</script>`, groupKey)
}

func renderGalleryView(b *strings.Builder, db *databases.Database, rows []*databases.Row) {
	titleKey := firstTextKey(db.Schema)
	b.WriteString(`<div class="db-gallery" style="display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:1rem">`)
	for _, r := range rows {
		title := "(untitled)"
		if titleKey != "" {
			if v, ok := r.Props[titleKey].(string); ok && v != "" {
				title = v
			}
		}
		fmt.Fprintf(b,
			`<div class="gallery-card" data-row-id="%s" style="background:#1b1b1b;padding:1rem;border-radius:6px">`,
			html.EscapeString(r.ID))
		fmt.Fprintf(b, `<h4>%s</h4>`, html.EscapeString(title))
		for _, def := range db.Schema {
			if def.Key == titleKey {
				continue
			}
			if v, ok := r.Props[def.Key]; ok && v != nil {
				fmt.Fprintf(b, `<p style="margin:.25rem 0;color:#aaa"><small>%s:</small> %s</p>`,
					html.EscapeString(def.Name), formatPropValue(def, v))
			}
		}
		b.WriteString(`</div>`)
	}
	b.WriteString(`</div>`)
	if len(rows) == 0 {
		b.WriteString(`<div class="empty-state"><p>No rows yet.</p></div>`)
	}
}

func renderListView(b *strings.Builder, db *databases.Database, rows []*databases.Row) {
	titleKey := firstTextKey(db.Schema)
	b.WriteString(`<ul class="db-list" style="list-style:none;padding:0">`)
	for _, r := range rows {
		title := "(untitled)"
		if titleKey != "" {
			if v, ok := r.Props[titleKey].(string); ok && v != "" {
				title = v
			}
		}
		fmt.Fprintf(b, `<li style="padding:.5rem 0;border-bottom:1px solid #2a2a2a"><strong>%s</strong>`,
			html.EscapeString(title))
		for _, def := range db.Schema[:min(2, len(db.Schema))] {
			if def.Key == titleKey {
				continue
			}
			if v, ok := r.Props[def.Key]; ok && v != nil {
				fmt.Fprintf(b, ` <small style="color:#888">— %s: %s</small>`,
					html.EscapeString(def.Name), formatPropValue(def, v))
			}
		}
		b.WriteString(`</li>`)
	}
	b.WriteString(`</ul>`)
}

// ─── Comments sidebar ──────────────────────────────────────────────────────

func (s *CloudServer) handleDashboardCommentsSidebar(w http.ResponseWriter, r *http.Request) {
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	q := r.URL.Query()
	opts := comments.ListPageOpts{Limit: 100}
	switch strings.ToLower(strings.TrimSpace(q.Get("filter"))) {
	case "unresolved":
		opts.OnlyUnresolved = true
	case "resolved":
		opts.OnlyResolved = true
	case "mentions":
		// Filtrar por mentions del usuario actual.
		opts.IncludeReplies = true
	}
	cs, err := s.pageComments.ListPage(r.Context(), pageID, opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	unresolved, _ := s.pageComments.CountUnresolved(r.Context(), pageID)

	b := strings.Builder{}
	b.WriteString(`<aside class="comments-sidebar" id="comments-sidebar" style="border-left:1px solid #2a2a2a;padding:1rem;width:340px">`)
	fmt.Fprintf(&b, `<h3>Comments <small style="color:#888">(%d unresolved)</small></h3>`, unresolved)
	b.WriteString(`<nav class="comment-filters" style="display:flex;gap:.5rem;margin-bottom:.5rem;font-size:.85rem">`)
	for _, f := range []string{"all", "unresolved", "resolved", "mentions"} {
		fmt.Fprintf(&b,
			`<a hx-get="/dashboard/comments/%s/sidebar?filter=%s" hx-target="#comments-sidebar" hx-swap="outerHTML">%s</a>`,
			html.EscapeString(pageID), f, f)
	}
	b.WriteString(`</nav>`)

	if len(cs) == 0 {
		b.WriteString(`<p class="empty-state" style="color:#777">No comments yet.</p>`)
	}
	for _, c := range cs {
		fmt.Fprintf(&b, `<div class="comment" id="comment-%s" style="padding:.5rem 0;border-bottom:1px solid #2a2a2a">`, html.EscapeString(c.ID))
		fmt.Fprintf(&b, `<div style="display:flex;justify-content:space-between"><strong>%s</strong><small style="color:#888">%s</small></div>`,
			html.EscapeString(truncate(c.AuthorUID, 8)),
			html.EscapeString(c.CreatedAt.Format("2006-01-02 15:04")))
		fmt.Fprintf(&b, `<p>%s</p>`, html.EscapeString(c.ContentMD))
		if c.ReplyCount > 0 {
			fmt.Fprintf(&b, `<small><a hx-get="/v1/comments/%s/replies" hx-target="#comment-%s">[%d replies]</a></small>`,
				html.EscapeString(c.ID), html.EscapeString(c.ID), c.ReplyCount)
		}
		b.WriteString(`<div class="comment-actions" style="display:flex;gap:.5rem;margin-top:.25rem">`)
		if !c.IsResolved {
			fmt.Fprintf(&b,
				`<button hx-post="/v1/comments/%s/resolve" hx-target="#comments-sidebar" hx-swap="outerHTML" hx-trigger="click">Resolve</button>`,
				html.EscapeString(c.ID))
		} else {
			fmt.Fprintf(&b,
				`<button hx-post="/v1/comments/%s/unresolve" hx-target="#comments-sidebar" hx-swap="outerHTML">Reopen</button>`,
				html.EscapeString(c.ID))
		}
		fmt.Fprintf(&b,
			`<button hx-delete="/v1/comments/%s" hx-target="#comment-%s" hx-swap="outerHTML" hx-confirm="Delete?">Delete</button>`,
			html.EscapeString(c.ID), html.EscapeString(c.ID))
		b.WriteString(`</div></div>`)
	}

	// Form al final.
	fmt.Fprintf(&b, `<form hx-post="/v1/pages/%s/comments" hx-headers='{"Content-Type":"application/json"}'
		hx-target="#comments-sidebar" hx-swap="outerHTML"
		hx-vals='js:{"content_md": document.getElementById("comment-input").value}'>`,
		html.EscapeString(pageID))
	b.WriteString(`<textarea id="comment-input" name="content_md" placeholder="Add comment, use @user to mention" style="width:100%;min-height:60px"></textarea>`)
	b.WriteString(`<button type="submit">Post</button>`)
	b.WriteString(`</form>`)
	b.WriteString(`</aside>`)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(b.String()))
}

// handleDashboardMentionsBadge retorna sólo el count (HTMX OOB-style).
func (s *CloudServer) handleDashboardMentionsBadge(w http.ResponseWriter, r *http.Request) {
	claims, _ := s.dashboardClaimsFromRequest(r)
	if claims == nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	n, err := s.pageComments.CountUnreadMentions(r.Context(), claims.UID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int{"unread_mentions": n})
}

// ─── helpers ───────────────────────────────────────────────────────────────

func renderPagesLayout(s *CloudServer, w http.ResponseWriter, r *http.Request, title, body string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if isHTMXLikeRequest(r) {
		_, _ = w.Write([]byte(body))
		return
	}
	roles := s.dashboardRolesFromRequest(r)
	displayName := s.displayNameFor(r)
	component := dashboard.Layout(title, displayName, "pages", roles, templ.Raw(body))
	if err := component.Render(r.Context(), w); err != nil {
		_, _ = w.Write([]byte(body))
	}
}

func vtTitle(vt string) string {
	switch vt {
	case "kanban":
		return "Kanban"
	case "gallery":
		return "Gallery"
	case "list":
		return "List"
	case "calendar":
		return "Calendar"
	}
	return "Table"
}

func formatPropValue(def databases.PropDef, v any) string {
	if v == nil {
		return `<span style="color:#555">—</span>`
	}
	switch def.Type {
	case databases.PropCheckbox:
		if b, ok := v.(bool); ok && b {
			return "✓"
		}
		return ""
	case databases.PropMultiSelect:
		if arr, ok := v.([]any); ok {
			parts := make([]string, len(arr))
			for i, x := range arr {
				if s, ok := x.(string); ok {
					parts[i] = `<span style="background:#2a2a2a;padding:.1rem .4rem;border-radius:3px;margin-right:.25rem">` + html.EscapeString(s) + `</span>`
				}
			}
			return strings.Join(parts, "")
		}
	case databases.PropURL:
		if s, ok := v.(string); ok && s != "" {
			return fmt.Sprintf(`<a href="%s">%s</a>`, html.EscapeString(s), html.EscapeString(s))
		}
	}
	if s, ok := v.(string); ok {
		return html.EscapeString(s)
	}
	b, _ := json.Marshal(v)
	return html.EscapeString(string(b))
}

func firstSelectKey(schema []databases.PropDef) string {
	for _, d := range schema {
		if d.Type == databases.PropSelect {
			return d.Key
		}
	}
	return ""
}

func firstTextKey(schema []databases.PropDef) string {
	for _, d := range schema {
		if d.Type == databases.PropText || d.Type == databases.PropRichText {
			return d.Key
		}
	}
	if len(schema) > 0 {
		return schema[0].Key
	}
	return ""
}

func optionsForKey(schema []databases.PropDef, key string) []string {
	for _, d := range schema {
		if d.Key == key {
			return d.Options
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
