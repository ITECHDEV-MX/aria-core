package dashboard

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ─── Audit egress handlers (redactor module) ────────────────────────────────
//
// Three endpoints:
//   GET /dashboard/audit/egress       -> shell page, filter UI + initial fetch
//   GET /dashboard/audit/egress/list  -> HTMX partial with filtered rows
//   GET /dashboard/audit/egress.csv   -> CSV export for compliance reports
//
// All admin-gated. Filter params: from / to / client_id / user_uid / provider.

func (h *handlers) handleAuditEgress(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	filter, ferr := parseEgressFilter(r)
	if ferr != "" {
		http.Error(w, ferr, http.StatusBadRequest)
		return
	}
	var stats *EgressStatsView
	if h.cfg.Redactor != nil {
		stats, _ = h.cfg.Redactor.StatsLastDays(r.Context(), 30)
	}
	component := AuditEgressPage(p.DisplayName(), filter, stats)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Egress LLM — Auditoría", p.DisplayName(), "audit-egress", p.Roles(), component))
}

func (h *handlers) handleAuditEgressList(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Redactor == nil {
		renderComponent(w, r, EmptyState("No configurado", "El módulo redactor no está cargado en esta instancia."))
		return
	}
	filter, ferr := parseEgressFilter(r)
	if ferr != "" {
		http.Error(w, ferr, http.StatusBadRequest)
		return
	}
	page, pageSize := parsePaginationRaw(r)
	rows, total, err := h.cfg.Redactor.ListEgress(r.Context(), filter, pageSize, (page-1)*pageSize)
	if err != nil {
		http.Error(w, fmt.Sprintf("egress list: %v", err), http.StatusInternalServerError)
		return
	}
	pg, _ := reclampPagination(page, pageSize, total)
	renderComponent(w, r, AuditEgressListPartial(rows, pg, filter))
}

func (h *handlers) handleAuditEgressCSV(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if !p.IsAdmin() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if h.cfg.Redactor == nil {
		http.Error(w, "redactor not configured", http.StatusServiceUnavailable)
		return
	}
	filter, ferr := parseEgressFilter(r)
	if ferr != "" {
		http.Error(w, ferr, http.StatusBadRequest)
		return
	}
	// Compliance export uses a hard cap of 5000 rows. Filter the dataset by
	// from/to to keep things sensible.
	rows, _, err := h.cfg.Redactor.ListEgress(r.Context(), filter, 5000, 0)
	if err != nil {
		http.Error(w, fmt.Sprintf("egress csv: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="aria-egress-audit.csv"`)
	cw := csv.NewWriter(w)
	defer cw.Flush()
	_ = cw.Write([]string{
		"occurred_at", "request_id", "user_uid", "llm_provider", "llm_model",
		"client_id", "observation_id", "scrubbed", "payload_size", "payload_hash",
		"reason", "redactions_json",
	})
	for _, row := range rows {
		_ = cw.Write([]string{
			row.OccurredAt.UTC().Format(time.RFC3339),
			row.RequestID,
			row.UserUID,
			row.LLMProvider,
			row.LLMModel,
			row.ClientID,
			row.ObservationID,
			boolStr(row.Scrubbed),
			strconv.Itoa(row.PayloadSize),
			row.PayloadHash,
			row.Reason,
			row.RedactionsRaw,
		})
	}
}

func parseEgressFilter(r *http.Request) (EgressFilter, string) {
	q := r.URL.Query()
	f := EgressFilter{
		ClientID: strings.TrimSpace(q.Get("client_id")),
		UserUID:  strings.TrimSpace(q.Get("user_uid")),
		Provider: strings.TrimSpace(q.Get("provider")),
	}
	if v := strings.TrimSpace(q.Get("from")); v != "" {
		t, err := parseAuditTime(v)
		if err != nil {
			return EgressFilter{}, err.Error()
		}
		f.From = t
	}
	if v := strings.TrimSpace(q.Get("to")); v != "" {
		t, err := parseAuditTime(v)
		if err != nil {
			return EgressFilter{}, err.Error()
		}
		f.To = t
	}
	return f, ""
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
