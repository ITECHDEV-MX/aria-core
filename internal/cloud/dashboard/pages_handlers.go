package dashboard

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"
)

// goldmarkPages — instancia compartida con extensiones GFM (tables, strikethrough,
// task lists, autolink). Sin raw HTML por defecto (sanitize). Hardened para
// editor compartido.
var goldmarkPages = goldmark.New(
	goldmark.WithExtensions(
		extension.GFM,
		extension.Strikethrough,
		extension.Table,
		extension.TaskList,
		extension.Linkify,
	),
	goldmark.WithParserOptions(
		parser.WithAutoHeadingID(),
	),
	goldmark.WithRendererOptions(
		html.WithHardWraps(),
		html.WithXHTML(),
		// NOT html.WithUnsafe — bloqueamos raw HTML para evitar XSS desde wiki content.
	),
)

// renderMarkdownPagesHTML convierte markdown a HTML usando la config GFM.
// Llamado desde pages.templ vía templ.Raw — el body queda inline.
func renderMarkdownPagesHTML(md string) string {
	if strings.TrimSpace(md) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := goldmarkPages.Convert([]byte(md), &buf); err != nil {
		return `<p class="login-error">error renderizando markdown</p>`
	}
	return buf.String()
}

// pagesTreeURL construye la URL inicial del tree partial preservando filtros.
func pagesTreeURL(project, scope string) string {
	v := url.Values{}
	if p := strings.TrimSpace(project); p != "" {
		v.Set("project", p)
	}
	if s := strings.TrimSpace(scope); s != "" {
		v.Set("scope", s)
	}
	if encoded := v.Encode(); encoded != "" {
		return "/dashboard/pages/tree?" + encoded
	}
	return "/dashboard/pages/tree"
}

// pagesFilterByParent retorna los hijos directos de parentID (vacío = root).
// El consumidor llama recursivamente desde PagesTreeNode.
func pagesFilterByParent(all []PageView, parentID string) []PageView {
	out := []PageView{}
	for _, p := range all {
		if p.ParentID == parentID {
			out = append(out, p)
		}
	}
	return out
}

func pagesTreeNodeClass(nodeID, activeID string) string {
	if nodeID == activeID {
		return "pages-tree-row pages-tree-row-active"
	}
	return "pages-tree-row"
}

// ─── Handlers ───────────────────────────────────────────────────────────────

func (h *handlers) handlePagesHome(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	activeID := strings.TrimSpace(r.URL.Query().Get("id"))
	projectFilter := strings.TrimSpace(r.URL.Query().Get("project"))
	scopeFilter := strings.TrimSpace(r.URL.Query().Get("scope"))
	templates := []PageTemplateView{}
	if h.cfg.Pages != nil {
		templates = h.cfg.Pages.ListTemplates()
	}
	component := PagesHome(activeID, projectFilter, scopeFilter, templates)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Wiki", p.DisplayName(), "pages", p.Roles(), component))
}

func (h *handlers) handlePagesTreePartial(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		renderComponent(w, r, PagesTreePartial(nil, ""))
		return
	}
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	activeID := strings.TrimSpace(r.URL.Query().Get("id"))

	pages, err := h.cfg.Pages.Tree(r.Context(), project, scope)
	if err != nil {
		http.Error(w, "tree: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if q != "" {
		// Filtro client-side simple: matchea title o icon (case-insensitive).
		filtered := []PageView{}
		needle := strings.ToLower(q)
		// Cuando filtramos, mostramos también ancestros para preservar el path.
		matched := map[string]bool{}
		for _, p := range pages {
			if strings.Contains(strings.ToLower(p.Title), needle) {
				matched[p.ID] = true
			}
		}
		// Subir desde cada match a la raíz para incluir ancestros.
		byID := map[string]PageView{}
		for _, p := range pages {
			byID[p.ID] = p
		}
		for id := range matched {
			cur := byID[id]
			for cur.ParentID != "" {
				matched[cur.ParentID] = true
				cur = byID[cur.ParentID]
			}
		}
		for _, p := range pages {
			if matched[p.ID] {
				filtered = append(filtered, p)
			}
		}
		pages = filtered
	}
	renderComponent(w, r, PagesTreePartial(pages, activeID))
}

func (h *handlers) handlePagesEditor(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	templates := h.cfg.Pages.ListTemplates()

	if r.URL.Query().Get("new") == "1" {
		// Vacío: pre-poblar con parent_id si viene.
		parentID := strings.TrimSpace(r.URL.Query().Get("parent_id"))
		page := PageView{
			ParentID:    parentID,
			Title:       "",
			ContentMD:   "",
			Scope:       "team",
			Sensitivity: "internal",
			PageType:    "doc",
		}
		renderComponent(w, r, PagesEditor(page, true, templates))
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		renderComponent(w, r, PagesEmptyEditor(templates))
		return
	}
	pg, err := h.cfg.Pages.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "page not found: "+err.Error(), http.StatusNotFound)
		return
	}
	renderComponent(w, r, PagesEditor(*pg, false, templates))
}

func (h *handlers) handlePagesPreview(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	md := r.PostForm.Get("content_md")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderMarkdownPagesHTML(md)))
}

func (h *handlers) handlePagesCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Error(w, "session has no uid", http.StatusUnauthorized)
		return
	}
	in := CreatePageInput{
		ParentID:     strings.TrimSpace(r.PostForm.Get("parent_id")),
		Title:        strings.TrimSpace(r.PostForm.Get("title")),
		ContentMD:    r.PostForm.Get("content_md"),
		Icon:         strings.TrimSpace(r.PostForm.Get("icon")),
		Project:      strings.TrimSpace(r.PostForm.Get("project")),
		Scope:        strings.TrimSpace(r.PostForm.Get("scope")),
		Sensitivity:  strings.TrimSpace(r.PostForm.Get("sensitivity")),
		TemplateKey:  strings.TrimSpace(r.PostForm.Get("template_key")),
		CreatedByUID: p.UID(),
	}
	pg, err := h.cfg.Pages.Create(r.Context(), in)
	if err != nil {
		http.Error(w, "create: "+err.Error(), http.StatusBadRequest)
		return
	}
	templates := h.cfg.Pages.ListTemplates()
	if isHTMXRequest(r) {
		// Re-render editor con la nueva página + tree refresh OOB.
		w.Header().Set("HX-Trigger", "pages:tree-changed")
		renderComponent(w, r, PagesEditor(*pg, false, templates))
		return
	}
	http.Redirect(w, r, "/dashboard/pages?id="+pg.ID, http.StatusSeeOther)
}

func (h *handlers) handlePagesUpdate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Error(w, "session has no uid", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	title := strings.TrimSpace(r.PostForm.Get("title"))
	content := r.PostForm.Get("content_md")
	icon := strings.TrimSpace(r.PostForm.Get("icon"))
	in := UpdatePageInput{
		Title:        &title,
		ContentMD:    &content,
		Icon:         &icon,
		UpdatedByUID: p.UID(),
		EditSummary:  strings.TrimSpace(r.PostForm.Get("edit_summary")),
	}
	pg, err := h.cfg.Pages.Update(r.Context(), id, in)
	if err != nil {
		http.Error(w, "update: "+err.Error(), http.StatusBadRequest)
		return
	}
	templates := h.cfg.Pages.ListTemplates()
	if isHTMXRequest(r) {
		w.Header().Set("HX-Trigger", "pages:tree-changed")
		renderWithToast(w, r, PagesEditor(*pg, false, templates), "Página guardada", "success")
		return
	}
	http.Redirect(w, r, "/dashboard/pages?id="+pg.ID, http.StatusSeeOther)
}

func (h *handlers) handlePagesMove(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	newParent := strings.TrimSpace(r.PostForm.Get("new_parent_id"))
	sortOrder, _ := strconv.Atoi(strings.TrimSpace(r.PostForm.Get("sort_order")))
	if err := h.cfg.Pages.Move(r.Context(), id, newParent, sortOrder); err != nil {
		http.Error(w, "move: "+err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("HX-Trigger", "pages:tree-changed")
	w.WriteHeader(http.StatusNoContent)
}

func (h *handlers) handlePagesArchive(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := h.cfg.Pages.Archive(r.Context(), id); err != nil {
		http.Error(w, "archive: "+err.Error(), http.StatusBadRequest)
		return
	}
	if isHTMXRequest(r) {
		// Refresh tree + clean editor.
		templates := h.cfg.Pages.ListTemplates()
		renderComponent(w, r, PagesEmptyEditor(templates))
		return
	}
	http.Redirect(w, r, "/dashboard/pages", http.StatusSeeOther)
}

func (h *handlers) handlePagesRestore(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := h.cfg.Pages.Restore(r.Context(), id); err != nil {
		http.Error(w, "restore: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/pages?id="+id, http.StatusSeeOther)
}

func (h *handlers) handlePagesRevisions(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	pg, err := h.cfg.Pages.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "get: "+err.Error(), http.StatusNotFound)
		return
	}
	revs, err := h.cfg.Pages.ListRevisions(r.Context(), id, 100)
	if err != nil {
		http.Error(w, "revisions: "+err.Error(), http.StatusInternalServerError)
		return
	}
	renderComponent(w, r, PagesRevisionsList(*pg, revs))
}

func (h *handlers) handlePagesRevert(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Pages == nil {
		http.Error(w, "pages module not configured", http.StatusServiceUnavailable)
		return
	}
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Error(w, "session has no uid", http.StatusUnauthorized)
		return
	}
	id := r.PathValue("id")
	rev := r.PathValue("rev")
	if err := h.cfg.Pages.RevertToRevision(r.Context(), id, rev, p.UID()); err != nil {
		http.Error(w, "revert: "+err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/pages?id="+id, http.StatusSeeOther)
}

// handleQuickSearch — endpoint del Cmd+K modal. Combina 6 fuentes vía
// pages.QuickSearchAll. Retorna fragment HTML; query "" muestra hint.
func (h *handlers) handleQuickSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if h.cfg.Pages == nil {
		renderComponent(w, r, QuickSearchResults(q, &QuickSearchView{}))
		return
	}
	limit := 5
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 && n <= 20 {
			limit = n
		}
	}
	if q == "" {
		renderComponent(w, r, QuickSearchResults("", &QuickSearchView{}))
		return
	}
	res, err := h.cfg.Pages.QuickSearchAll(r.Context(), q, limit)
	if err != nil {
		http.Error(w, fmt.Sprintf("quick-search: %v", err), http.StatusInternalServerError)
		return
	}
	renderComponent(w, r, QuickSearchResults(q, res))
}
