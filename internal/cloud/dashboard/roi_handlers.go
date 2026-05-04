package dashboard

import (
	"github.com/ITECHDEV-MX/aria-core/internal/obs"
	"encoding/csv"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ROIPageVM es el ViewModel renderizado por /dashboard/roi.
type ROIPageVM struct {
	HasService   bool
	Notice       string
	IsAdmin      bool
	PeriodCode   string // "7d" | "30d" | "90d"
	PeriodLabel  string
	DevFilter    string
	Project      string
	Savings      ROISavingsView
	Weekly       []ROIWeeklyPoint
	Contributors []ROIContributorScore
	PerClient    []ROIClientBreakdown
}

// handleROIPage GET /dashboard/roi.
func (h *handlers) handleROIPage(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	vm := h.buildROIVM(r)
	component := ROIPage(vm)
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("ROI & Ahorro", p.DisplayName(), "roi", p.Roles(), component))
}

// handleROIData GET /dashboard/roi/data — partial JSON-friendly fragment opcional.
func (h *handlers) handleROIData(w http.ResponseWriter, r *http.Request) {
	vm := h.buildROIVM(r)
	renderComponent(w, r, ROICharts(vm))
}

// handleROIExportCSV GET /dashboard/roi/export.csv — exporta el breakdown
// per-cliente + savings totales para reporte mensual.
func (h *handlers) handleROIExportCSV(w http.ResponseWriter, r *http.Request) {
	vm := h.buildROIVM(r)
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="aria-core-roi.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"section", "key", "value", "unit"})
	_ = cw.Write([]string{"summary", "period", vm.PeriodCode, ""})
	_ = cw.Write([]string{"summary", "saved_minutes", fmt.Sprintf("%.2f", vm.Savings.TotalSavedMinutes), "min"})
	_ = cw.Write([]string{"summary", "saved_mxn", fmt.Sprintf("%.2f", vm.Savings.TotalSavedMXN), "MXN"})
	_ = cw.Write([]string{"summary", "ttc", fmt.Sprintf("%.2f", vm.Savings.TTC), "min"})
	_ = cw.Write([]string{"summary", "rdr", fmt.Sprintf("%.4f", vm.Savings.RDR), "ratio"})
	_ = cw.Write([]string{"summary", "cwr", fmt.Sprintf("%.4f", vm.Savings.CWR), "ratio"})
	_ = cw.Write([]string{"summary", "svr", fmt.Sprintf("%.4f", vm.Savings.SVR), "ratio"})
	_ = cw.Write([]string{"summary", "dtt", fmt.Sprintf("%.2f", vm.Savings.DTT), "min"})
	for k, v := range vm.Savings.ByPillar {
		_ = cw.Write([]string{"pillar", k, fmt.Sprintf("%.2f", v), "min"})
	}
	for _, c := range vm.PerClient {
		_ = cw.Write([]string{"client", c.ClientID, fmt.Sprintf("%d", c.ObservationCount), "observations"})
		_ = cw.Write([]string{"client_canon", c.ClientID, fmt.Sprintf("%d", c.CanonCount), "canon_obs"})
	}
	for _, c := range vm.Contributors {
		_ = cw.Write([]string{"contributor", c.DeveloperEmail, fmt.Sprintf("%d", c.CanonCount), "canon_obs"})
	}
	cw.Flush()
}

// buildROIVM consulta el ROI service y arma el viewmodel completo.
func (h *handlers) buildROIVM(r *http.Request) ROIPageVM {
	ctx := r.Context()
	p := h.principalFromRequest(r)
	period := strings.TrimSpace(r.URL.Query().Get("period"))
	if period != "7d" && period != "30d" && period != "90d" {
		period = "7d"
	}
	devFilter := strings.TrimSpace(r.URL.Query().Get("dev"))
	project := strings.TrimSpace(r.URL.Query().Get("project"))

	now := time.Now().UTC()
	var since time.Time
	periodLabel := ""
	switch period {
	case "30d":
		since = now.AddDate(0, 0, -30)
		periodLabel = "Últimos 30 días"
	case "90d":
		since = now.AddDate(0, 0, -90)
		periodLabel = "Últimos 90 días"
	default:
		since = now.AddDate(0, 0, -7)
		periodLabel = "Últimos 7 días"
	}

	vm := ROIPageVM{
		HasService:  h.cfg.ROI != nil,
		IsAdmin:     p.IsAdmin(),
		PeriodCode:  period,
		PeriodLabel: periodLabel,
		Project:     project,
		Weekly:      []ROIWeeklyPoint{},
	}
	if h.cfg.ROI == nil {
		vm.Notice = "ROI service no configurado en el servidor — instalá el adapter en cmd/aria-core/cloud.go."
		return vm
	}

	// Devs no-admin sólo ven su propio ROI; admins pueden filtrar por dev.
	effectiveDev := strings.TrimSpace(p.UID())
	if p.IsAdmin() {
		if devFilter != "" {
			effectiveDev = devFilter
			vm.DevFilter = devFilter
		} else {
			effectiveDev = "" // admin global view
		}
	}

	if savings, err := h.cfg.ROI.CalculateSavings(ctx, effectiveDev, since, now); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: roi calculate-savings error: %v", err))
	} else if savings != nil {
		vm.Savings = *savings
	}

	if weekly, err := h.cfg.ROI.WeeklyTimeline(ctx, effectiveDev, 12, h.cfg.ROI.CostMXNPerMin()); err != nil {
		obs.L().Info(fmt.Sprintf("dashboard: roi weekly timeline error: %v", err))
	} else {
		vm.Weekly = weekly
	}

	if vm.IsAdmin {
		if contrib, err := h.cfg.ROI.TopContributors(ctx, since, 5); err != nil {
			obs.L().Info(fmt.Sprintf("dashboard: roi top contributors error: %v", err))
		} else {
			vm.Contributors = contrib
		}
		if perClient, err := h.cfg.ROI.PerClientBreakdown(ctx, since); err != nil {
			obs.L().Info(fmt.Sprintf("dashboard: roi per-client breakdown error: %v", err))
		} else {
			vm.PerClient = perClient
		}
	}
	return vm
}
