package dashboard

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// === RFPs ===

func (h *handlers) handleCotizadorRFPCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	in := CreateRFPInput{
		LeadID:        leadID,
		SourceType:    strings.TrimSpace(r.PostForm.Get("source_type")),
		SourceContent: r.PostForm.Get("source_content"),
		AnalysisJSON:  strings.TrimSpace(r.PostForm.Get("analysis_json")),
	}
	if in.SourceType == "" {
		in.SourceType = "text"
	}
	if in.AnalysisJSON == "" {
		in.AnalysisJSON = "{}"
	}
	if _, err := h.cfg.Cotizador.CreateRFP(r.Context(), in); err != nil {
		http.Error(w, fmt.Sprintf("create rfp: %v", err), http.StatusBadRequest)
		return
	}
	// Re-render del detalle del lead completo
	h.renderLeadDetail(w, r, leadID)
}

func (h *handlers) handleCotizadorRFPDetail(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	rfpID := r.PathValue("rfpID")
	rfp, err := h.cfg.Cotizador.GetRFP(r.Context(), rfpID)
	if err != nil {
		http.Error(w, fmt.Sprintf("rfp not found: %v", err), http.StatusNotFound)
		return
	}
	p := h.principalFromRequest(r)
	component := CotizadorRFPDetail(rfp)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("RFP", p.DisplayName(), "cotizador", p.Roles(), component))
}

func (h *handlers) handleCotizadorRFPAnalysisUpdate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	rfpID := r.PathValue("rfpID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	analysis := strings.TrimSpace(r.PostForm.Get("analysis_json"))
	if analysis == "" {
		analysis = "{}"
	}
	if err := h.cfg.Cotizador.UpdateRFPAnalysis(r.Context(), rfpID, analysis); err != nil {
		http.Error(w, fmt.Sprintf("update analysis: %v", err), http.StatusBadRequest)
		return
	}
	rfp, err := h.cfg.Cotizador.GetRFP(r.Context(), rfpID)
	if err != nil {
		http.Error(w, "rfp not found after update", http.StatusNotFound)
		return
	}
	renderComponent(w, r, CotizadorRFPDetail(rfp))
}

// === Quotes ===

func (h *handlers) handleCotizadorQuoteNewForm(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	lead, err := h.cfg.Cotizador.GetLead(r.Context(), leadID)
	if err != nil {
		http.Error(w, "lead not found", http.StatusNotFound)
		return
	}
	rfps, _ := h.cfg.Cotizador.ListRFPsByLead(r.Context(), leadID)
	p := h.principalFromRequest(r)
	component := CotizadorQuoteNewForm(lead, rfps)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Nueva cotización", p.DisplayName(), "cotizador", p.Roles(), component))
}

func (h *handlers) handleCotizadorQuoteCreate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	leadID := r.PathValue("id")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	role := pickRole(r, h.cfg.GetRoles)
	in := CreateQuoteInput{
		LeadID:        leadID,
		RFPID:         strings.TrimSpace(r.PostForm.Get("rfp_id")),
		Currency:      strings.TrimSpace(r.PostForm.Get("currency")),
		Terms:         r.PostForm.Get("terms"),
		Justification: r.PostForm.Get("justification"),
		Role:          role,
	}
	if v := strings.TrimSpace(r.PostForm.Get("valid_until")); v != "" {
		if t, err := time.Parse("2006-01-02", v); err == nil {
			in.ValidUntil = &t
		}
	}
	// Items en arrays paralelos: item_description[], item_qty[], item_unit_price[], item_sku[]
	descs := r.PostForm["item_description"]
	qtys := r.PostForm["item_qty"]
	prices := r.PostForm["item_unit_price"]
	skus := r.PostForm["item_sku"]
	for i := range descs {
		desc := strings.TrimSpace(descs[i])
		if desc == "" {
			continue
		}
		qty := 1.0
		if i < len(qtys) {
			if v, err := strconv.ParseFloat(strings.TrimSpace(qtys[i]), 64); err == nil {
				qty = v
			}
		}
		price := 0.0
		if i < len(prices) {
			if v, err := strconv.ParseFloat(strings.TrimSpace(prices[i]), 64); err == nil {
				price = v
			}
		}
		sku := ""
		if i < len(skus) {
			sku = strings.TrimSpace(skus[i])
		}
		in.Items = append(in.Items, CreateQuoteItemInput{
			SKU: sku, Description: desc, Qty: qty, UnitPrice: price,
		})
	}
	q, err := h.cfg.Cotizador.CreateQuote(r.Context(), in)
	if err != nil {
		http.Error(w, fmt.Sprintf("create quote: %v", err), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard/cotizador/quotes/"+q.ID, http.StatusSeeOther)
}

func (h *handlers) handleCotizadorQuoteDetail(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	q, err := h.cfg.Cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, "quote not found", http.StatusNotFound)
		return
	}
	items, _ := h.cfg.Cotizador.ListQuoteItems(r.Context(), quoteID)
	history, _ := h.cfg.Cotizador.QuoteHistory(r.Context(), quoteID, 50)
	p := h.principalFromRequest(r)
	component := CotizadorQuoteDetail(q, items, history)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	title := fmt.Sprintf("Cotización v%d", q.Version)
	renderComponent(w, r, Layout(title, p.DisplayName(), "cotizador", p.Roles(), component))
}

func (h *handlers) handleCotizadorQuoteStatusChange(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	newStatus := strings.TrimSpace(r.PostForm.Get("status"))
	notes := strings.TrimSpace(r.PostForm.Get("notes"))
	if err := h.cfg.Cotizador.UpdateQuoteStatus(r.Context(), quoteID, newStatus, "", notes); err != nil {
		http.Error(w, fmt.Sprintf("update status: %v", err), http.StatusBadRequest)
		return
	}
	q, err := h.cfg.Cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, "quote not found after update", http.StatusNotFound)
		return
	}
	items, _ := h.cfg.Cotizador.ListQuoteItems(r.Context(), quoteID)
	history, _ := h.cfg.Cotizador.QuoteHistory(r.Context(), quoteID, 50)
	renderComponent(w, r, CotizadorQuoteDetail(q, items, history))
}

// renderLeadDetail re-renderea el detail completo del lead (usado tras crear RFP).
func (h *handlers) renderLeadDetail(w http.ResponseWriter, r *http.Request, leadID string) {
	lead, err := h.cfg.Cotizador.GetLead(r.Context(), leadID)
	if err != nil {
		http.Error(w, "lead not found", http.StatusNotFound)
		return
	}
	history, _ := h.cfg.Cotizador.LeadHistory(r.Context(), leadID, 50)
	rfps, _ := h.cfg.Cotizador.ListRFPsByLead(r.Context(), leadID)
	quotes, _ := h.cfg.Cotizador.ListQuotesByLead(r.Context(), leadID)
	renderComponent(w, r, CotizadorLeadDetailFull(lead, history, rfps, quotes))
}

// pickRole lee los roles del JWT y elige el role para etiquetar created_by_role.
// Si tiene 'cotizador', prefiere ese (más restrictivo). Si solo admin, usa admin.
func pickRole(r *http.Request, getRoles func(*http.Request) []string) string {
	if getRoles == nil {
		return "cotizador"
	}
	roles := getRoles(r)
	for _, ro := range roles {
		if ro == "cotizador" {
			return "cotizador"
		}
	}
	for _, ro := range roles {
		if ro == "admin" {
			return "admin"
		}
	}
	return "cotizador"
}

var _ = log.Println
