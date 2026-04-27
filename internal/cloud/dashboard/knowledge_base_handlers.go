package dashboard

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

func (h *handlers) handleKnowledgeBasePage(w http.ResponseWriter, r *http.Request) {
	if h.cfg.KnowledgeBase == nil {
		http.Error(w, "knowledge-base no configurado", http.StatusServiceUnavailable)
		return
	}
	p := h.principalFromRequest(r)
	component := KnowledgeBasePage(!h.cfg.KnowledgeBase.Available())
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Knowledge base", p.DisplayName(), "knowledge-base", p.Roles(), component))
}

func (h *handlers) handleKnowledgeBaseList(w http.ResponseWriter, r *http.Request) {
	if h.cfg.KnowledgeBase == nil {
		http.Error(w, "knowledge-base no configurado", http.StatusServiceUnavailable)
		return
	}
	filter := KBListFilterView{
		EntityType: strings.TrimSpace(r.URL.Query().Get("entity_type")),
		ProjectID:  strings.TrimSpace(r.URL.Query().Get("project_id")),
		Status:     strings.TrimSpace(r.URL.Query().Get("status")),
	}
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	rows, err := h.cfg.KnowledgeBase.List(r.Context(), filter)
	if err != nil {
		http.Error(w, fmt.Sprintf("list: %v", err), http.StatusInternalServerError)
		return
	}
	stats, _ := h.cfg.KnowledgeBase.Status(r.Context())
	renderComponent(w, r, KnowledgeBaseListPartial(rows, stats))
}

func (h *handlers) handleKnowledgeBaseStatus(w http.ResponseWriter, r *http.Request) {
	if h.cfg.KnowledgeBase == nil {
		http.Error(w, "knowledge-base no configurado", http.StatusServiceUnavailable)
		return
	}
	stats, err := h.cfg.KnowledgeBase.Status(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("status: %v", err), http.StatusInternalServerError)
		return
	}
	renderComponent(w, r, KnowledgeBaseStatusBadge(stats))
}

func (h *handlers) handleKnowledgeBaseResyncFailed(w http.ResponseWriter, r *http.Request) {
	if h.cfg.KnowledgeBase == nil {
		http.Error(w, "knowledge-base no configurado", http.StatusServiceUnavailable)
		return
	}
	count, err := h.cfg.KnowledgeBase.ResyncFailed(r.Context())
	if err != nil {
		http.Error(w, fmt.Sprintf("resync: %v", err), http.StatusInternalServerError)
		return
	}
	rows, _ := h.cfg.KnowledgeBase.List(r.Context(), KBListFilterView{})
	stats, _ := h.cfg.KnowledgeBase.Status(r.Context())
	w.Header().Set("X-KB-Resynced", strconv.Itoa(count))
	renderComponent(w, r, KnowledgeBaseListPartial(rows, stats))
}

func (h *handlers) handleKnowledgeBaseResyncProject(w http.ResponseWriter, r *http.Request) {
	if h.cfg.KnowledgeBase == nil {
		http.Error(w, "knowledge-base no configurado", http.StatusServiceUnavailable)
		return
	}
	pid := strings.TrimSpace(r.PathValue("projectID"))
	if pid == "" {
		http.Error(w, "project_id requerido", http.StatusBadRequest)
		return
	}
	count, err := h.cfg.KnowledgeBase.ResyncProject(r.Context(), pid)
	if err != nil {
		http.Error(w, fmt.Sprintf("resync: %v", err), http.StatusInternalServerError)
		return
	}
	rows, _ := h.cfg.KnowledgeBase.List(r.Context(), KBListFilterView{ProjectID: pid})
	stats, _ := h.cfg.KnowledgeBase.Status(r.Context())
	w.Header().Set("X-KB-Resynced", strconv.Itoa(count))
	renderComponent(w, r, KnowledgeBaseListPartial(rows, stats))
}

func (h *handlers) handleKnowledgeBaseRefreshIndex(w http.ResponseWriter, r *http.Request) {
	if h.cfg.KnowledgeBase == nil {
		http.Error(w, "knowledge-base no configurado", http.StatusServiceUnavailable)
		return
	}
	if err := h.cfg.KnowledgeBase.RefreshIndex(r.Context()); err != nil {
		http.Error(w, fmt.Sprintf("refresh: %v", err), http.StatusInternalServerError)
		return
	}
	rows, _ := h.cfg.KnowledgeBase.List(r.Context(), KBListFilterView{})
	stats, _ := h.cfg.KnowledgeBase.Status(r.Context())
	renderComponent(w, r, KnowledgeBaseListPartial(rows, stats))
}

func (h *handlers) handleKnowledgeBaseSyncQuote(w http.ResponseWriter, r *http.Request) {
	if h.cfg.KnowledgeBase == nil {
		http.Error(w, "knowledge-base no configurado", http.StatusServiceUnavailable)
		return
	}
	qid := strings.TrimSpace(r.PathValue("quoteID"))
	if qid == "" {
		http.Error(w, "quote_id requerido", http.StatusBadRequest)
		return
	}
	commit, path, err := h.cfg.KnowledgeBase.SyncQuote(r.Context(), qid)
	if err != nil {
		http.Error(w, fmt.Sprintf("sync quote: %v", err), http.StatusInternalServerError)
		return
	}
	renderComponent(w, r, KnowledgeBaseSyncResult(commit, path))
}
