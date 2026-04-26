package dashboard

import (
	"fmt"
	"log"
	"net/http"
	"strings"
)

// handleCotizadorHome — shell + tabs internas (Leads). Loads list via HTMX.
func (h *handlers) handleCotizadorHome(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	component := CotizadorHomePage()
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Cotizaciones", p.DisplayName(), "cotizador", p.Roles(), component))
}

// handleCotizadorLeadsList — partial: tabla de leads con filter status.
func (h *handlers) handleCotizadorLeadsList(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		renderComponent(w, r, CotizadorLeadsPartial(nil, "cotizador module not configured", ""))
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	leads, err := h.cfg.Cotizador.ListLeads(r.Context(), status)
	if err != nil {
		log.Printf("dashboard: cotizador list leads: %v", err)
		renderComponent(w, r, CotizadorLeadsPartial(nil, fmt.Sprintf("error: %v", err), status))
		return
	}
	renderComponent(w, r, CotizadorLeadsPartial(leads, "", status))
}

// handleCotizadorLeadCreate — POST: crea lead, retorna lista actualizada.
func (h *handlers) handleCotizadorLeadCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	roles := []string{}
	if h.cfg.GetRoles != nil {
		roles = h.cfg.GetRoles(r)
	}
	role := "cotizador"
	for _, ro := range roles {
		if ro == "cotizador" {
			role = "cotizador"
			break
		}
		if ro == "admin" {
			role = "admin"
		}
	}
	input := CreateLeadInput{
		Name:    strings.TrimSpace(r.PostForm.Get("name")),
		Company: strings.TrimSpace(r.PostForm.Get("company")),
		Email:   strings.TrimSpace(r.PostForm.Get("email")),
		Phone:   strings.TrimSpace(r.PostForm.Get("phone")),
		Source:  strings.TrimSpace(r.PostForm.Get("source")),
		Notes:   strings.TrimSpace(r.PostForm.Get("notes")),
		Role:    role,
	}
	if _, err := h.cfg.Cotizador.CreateLead(r.Context(), input); err != nil {
		renderComponent(w, r, CotizadorLeadsPartial(nil, fmt.Sprintf("error: %v", err), ""))
		return
	}
	leads, err := h.cfg.Cotizador.ListLeads(r.Context(), "")
	if err != nil {
		renderComponent(w, r, CotizadorLeadsPartial(nil, "lead creado pero no pudo recargar lista", ""))
		return
	}
	renderComponent(w, r, CotizadorLeadsPartial(leads, "", ""))
}

// handleCotizadorLeadDetail — GET: detalle completo del lead (info + RFPs + quotes + history).
func (h *handlers) handleCotizadorLeadDetail(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	lead, err := h.cfg.Cotizador.GetLead(r.Context(), id)
	if err != nil {
		http.Error(w, fmt.Sprintf("lead not found: %v", err), http.StatusNotFound)
		return
	}
	history, _ := h.cfg.Cotizador.LeadHistory(r.Context(), id, 50)
	rfps, _ := h.cfg.Cotizador.ListRFPsByLead(r.Context(), id)
	quotes, _ := h.cfg.Cotizador.ListQuotesByLead(r.Context(), id)
	p := h.principalFromRequest(r)
	component := CotizadorLeadDetailFull(lead, history, rfps, quotes)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Lead — "+lead.Name, p.DisplayName(), "cotizador", p.Roles(), component))
}

// handleCotizadorLeadStatusChange — POST: cambia estado del lead.
func (h *handlers) handleCotizadorLeadStatusChange(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	newStatus := strings.TrimSpace(r.PostForm.Get("status"))
	notes := strings.TrimSpace(r.PostForm.Get("notes"))
	// byUID = uid del JWT del visor (TODO: exponer del request); por ahora ""
	if err := h.cfg.Cotizador.UpdateLeadStatus(r.Context(), id, newStatus, "", notes); err != nil {
		http.Error(w, fmt.Sprintf("update status: %v", err), http.StatusBadRequest)
		return
	}
	// Re-render del detalle
	lead, err := h.cfg.Cotizador.GetLead(r.Context(), id)
	if err != nil {
		http.Error(w, "lead not found after update", http.StatusNotFound)
		return
	}
	history, _ := h.cfg.Cotizador.LeadHistory(r.Context(), id, 50)
	rfps, _ := h.cfg.Cotizador.ListRFPsByLead(r.Context(), id)
	quotes, _ := h.cfg.Cotizador.ListQuotesByLead(r.Context(), id)
	renderComponent(w, r, CotizadorLeadDetailFull(lead, history, rfps, quotes))
}

// handleCotizadorLeadUpdate — POST: edita campos del lead (no status).
func (h *handlers) handleCotizadorLeadUpdate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	id := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	if err := h.cfg.Cotizador.UpdateLead(r.Context(), id,
		r.PostForm.Get("name"), r.PostForm.Get("company"), r.PostForm.Get("email"),
		r.PostForm.Get("phone"), r.PostForm.Get("source"), r.PostForm.Get("notes")); err != nil {
		http.Error(w, fmt.Sprintf("update lead: %v", err), http.StatusBadRequest)
		return
	}
	lead, err := h.cfg.Cotizador.GetLead(r.Context(), id)
	if err != nil {
		http.Error(w, "lead not found after update", http.StatusNotFound)
		return
	}
	history, _ := h.cfg.Cotizador.LeadHistory(r.Context(), id, 50)
	rfps, _ := h.cfg.Cotizador.ListRFPsByLead(r.Context(), id)
	quotes, _ := h.cfg.Cotizador.ListQuotesByLead(r.Context(), id)
	renderComponent(w, r, CotizadorLeadDetailFull(lead, history, rfps, quotes))
}
