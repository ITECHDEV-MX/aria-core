package dashboard

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/historias"
)

// handleHistoriasIndex renders the list of all historia slugs found
// under HistoriasRoot.
func (h *handlers) handleHistoriasIndex(w http.ResponseWriter, r *http.Request) {
	root := h.cfg.HistoriasRoot
	if strings.TrimSpace(root) == "" {
		http.Error(w, "HistoriasRoot not configured on the dashboard MountConfig", http.StatusInternalServerError)
		return
	}

	slugs, err := historias.ListSlugs(root)
	if err != nil {
		http.Error(w, fmt.Sprintf("list slugs: %v", err), http.StatusInternalServerError)
		return
	}

	rows := make([]HistoriaSummary, 0, len(slugs))
	for _, slug := range slugs {
		m, err := historias.ListChain(root, slug)
		if err != nil {
			rows = append(rows, HistoriaSummary{Slug: slug, Status: "(error)"})
			continue
		}
		rows = append(rows, HistoriaSummary{
			Slug:          slug,
			Status:        string(m.Status),
			StepCount:     len(m.Chain),
			CreatedAt:     m.CreatedAt,
			FinalArtifact: m.FinalArtifact,
		})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := frameLayoutFn(w, r, "Historias", HistoriasIndexPage(rows)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleHistoriaDetail renders one historia's chain.
func (h *handlers) handleHistoriaDetail(w http.ResponseWriter, r *http.Request) {
	slug := strings.TrimSpace(r.PathValue("slug"))
	if slug == "" {
		http.Error(w, "missing slug", http.StatusBadRequest)
		return
	}

	root := h.cfg.HistoriasRoot
	if strings.TrimSpace(root) == "" {
		http.Error(w, "HistoriasRoot not configured", http.StatusInternalServerError)
		return
	}

	m, err := historias.ListChain(root, slug)
	if err != nil {
		http.Error(w, fmt.Sprintf("list chain: %v", err), http.StatusNotFound)
		return
	}

	bodies := make(map[int]string, len(m.Chain))
	for _, e := range m.Chain {
		_, body, gerr := historias.GetArtifact(root, slug, e.Position)
		if gerr == nil {
			// Truncate very long bodies for the dashboard view to keep
			// the page light. Full content is always one click away in
			// the file system or via aria_artifact_get.
			if len(body) > 5000 {
				body = body[:5000] + "\n\n[...truncated; open /Historias/" + slug + "/" + e.Artifact + " for full content]"
			}
			bodies[e.Position] = body
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := frameLayoutFn(w, r, fmt.Sprintf("Historia %s", slug), HistoriaDetailPage(slug, m, bodies)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
