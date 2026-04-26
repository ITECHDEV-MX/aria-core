package dashboard

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (h *handlers) handleAriaMemList(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	projects := []string{}
	if h.cfg.AriaMem != nil {
		projects, _ = h.cfg.AriaMem.ListProjects(r.Context())
	}
	component := AriaMemListPage(projects)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Memoria ARIA", p.DisplayName(), "memorias", p.Roles(), component))
}

func (h *handlers) handleAriaMemListPartial(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		renderComponent(w, r, AriaMemListPartial(nil, "Memoria ARIA no configurada"))
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	project := strings.TrimSpace(r.URL.Query().Get("project"))
	scope := strings.TrimSpace(r.URL.Query().Get("scope"))
	obsType := strings.TrimSpace(r.URL.Query().Get("type"))
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 {
			limit = n
		}
	}
	rs, err := h.cfg.AriaMem.Search(r.Context(), q, project, scope, obsType, limit)
	if err != nil {
		renderComponent(w, r, AriaMemListPartial(nil, fmt.Sprintf("error: %v", err)))
		return
	}
	renderComponent(w, r, AriaMemListPartial(rs, ""))
}

func (h *handlers) handleAriaMemDetail(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	o, err := h.cfg.AriaMem.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("memoria no encontrada: %v", err), http.StatusNotFound)
		return
	}
	p := h.principalFromRequest(r)
	component := AriaMemDetail(o)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Memoria — "+o.Title, p.DisplayName(), "memorias", p.Roles(), component))
}

func (h *handlers) handleAriaMemPromoteCanon(w http.ResponseWriter, r *http.Request) {
	if h.cfg.AriaMem == nil {
		http.Error(w, "memoria no configurada", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := h.cfg.AriaMem.PromoteCanon(r.Context(), id, ""); err != nil {
		http.Error(w, fmt.Sprintf("promote: %v", err), http.StatusBadRequest)
		return
	}
	o, err := h.cfg.AriaMem.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "memoria no encontrada", http.StatusNotFound)
		return
	}
	renderComponent(w, r, AriaMemDetail(o))
}
