package main

import (
	"context"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/roi"
)

// roiAdapter wraps roi.MetricsStore exponiendo:
//
//   - cloudserver.ROIService (LogSearch hook desde /v1/memory/search)
//   - dashboard.ROIService    (dashboard /dashboard/roi)
//
// Mantiene los structs de la dashboard package decoupled del paquete roi.
type roiAdapter struct {
	store *roi.MetricsStore
}

// newROIAdapter construye un adapter atado al *sql.DB del cloudstore.
func newROIAdapter(cs *cloudstore.CloudStore) *roiAdapter {
	if cs == nil {
		return &roiAdapter{}
	}
	return &roiAdapter{store: roi.NewMetricsStore(cs.DB())}
}

// === cloudserver.ROIService ===

// LogSearch es el hook desde handleV1MemorySearch.
func (a *roiAdapter) LogSearch(ctx context.Context, p cloudserver.ROILogSearchParams) error {
	if a == nil || a.store == nil {
		return nil
	}
	return a.store.LogSearch(ctx, roi.LogSearchParams{
		Query:          p.Query,
		ResultCount:    p.ResultCount,
		CanonHitCount:  p.CanonHitCount,
		TotalTokens:    p.TotalTokens,
		TruncatedCount: p.TruncatedCount,
		DeveloperUID:   p.DeveloperUID,
		Project:        p.Project,
		Scope:          p.Scope,
		ClientID:       p.ClientID,
		DurationMs:     p.DurationMs,
	})
}

// Compile-time guard.
var _ cloudserver.ROIService = (*roiAdapter)(nil)

// === dashboard.ROIService ===

// roiDashboardAdapter sólo conserva el store y traduce structs al equivalente
// dashboard.* (evita ciclos dashboard→roi).
type roiDashboardAdapter struct {
	store *roi.MetricsStore
}

func newROIDashboardAdapter(cs *cloudstore.CloudStore) *roiDashboardAdapter {
	if cs == nil {
		return &roiDashboardAdapter{}
	}
	return &roiDashboardAdapter{store: roi.NewMetricsStore(cs.DB())}
}

func (a *roiDashboardAdapter) CalculateSavings(ctx context.Context, devUID string, since, until time.Time) (*dashboard.ROISavingsView, error) {
	if a == nil || a.store == nil {
		return &dashboard.ROISavingsView{}, nil
	}
	v, err := a.store.CalculateSavings(ctx, devUID, since, until)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return &dashboard.ROISavingsView{}, nil
	}
	return &dashboard.ROISavingsView{
		WindowDays:        v.WindowDays,
		TotalSavedMinutes: v.TotalSavedMinutes,
		TotalSavedMXN:     v.TotalSavedMXN,
		WorkdayPctSaved:   v.WorkdayPctSaved,
		ByPillar:          v.ByPillar,
		ByPillarMXN:       v.ByPillarMXN,
		Compared: dashboard.ROISavingsCompared{
			PreviousMinutes: v.Compared.PreviousMinutes,
			PreviousMXN:     v.Compared.PreviousMXN,
			DeltaMinutes:    v.Compared.DeltaMinutes,
			DeltaMXN:        v.Compared.DeltaMXN,
			DeltaPct:        v.Compared.DeltaPct,
		},
		RDR:           v.RDR,
		CWR:           v.CWR,
		SVR:           v.SVR,
		TTC:           v.TTC,
		DTT:           v.DTT,
		CostMXNPerMin: v.CostMXNPerMin,
	}, nil
}

func (a *roiDashboardAdapter) TopContributors(ctx context.Context, since time.Time, limit int) ([]dashboard.ROIContributorScore, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	rows, err := a.store.TopContributors(ctx, since, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.ROIContributorScore, 0, len(rows))
	for _, r := range rows {
		out = append(out, dashboard.ROIContributorScore{
			DeveloperUID:   r.DeveloperUID,
			DeveloperEmail: r.DeveloperEmail,
			CanonCount:     r.CanonCount,
			RelevanceTotal: r.RelevanceTotal,
		})
	}
	return out, nil
}

func (a *roiDashboardAdapter) PerClientBreakdown(ctx context.Context, since time.Time) ([]dashboard.ROIClientBreakdown, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	rows, err := a.store.PerClientBreakdown(ctx, since)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.ROIClientBreakdown, 0, len(rows))
	for _, r := range rows {
		out = append(out, dashboard.ROIClientBreakdown{
			ClientID:         r.ClientID,
			ObservationCount: r.ObservationCount,
			CanonCount:       r.CanonCount,
			VaultAccess:      r.VaultAccess,
			VaultUseInCmd:    r.VaultUseInCmd,
		})
	}
	return out, nil
}

func (a *roiDashboardAdapter) WeeklyTimeline(ctx context.Context, devUID string, weeks int, costPerMin float64) ([]dashboard.ROIWeeklyPoint, error) {
	if a == nil || a.store == nil {
		return nil, nil
	}
	rows, err := a.store.WeeklyTimeline(ctx, devUID, weeks, costPerMin)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.ROIWeeklyPoint, 0, len(rows))
	for _, r := range rows {
		out = append(out, dashboard.ROIWeeklyPoint{
			WeekStart:    r.WeekStart,
			SavedMinutes: r.SavedMinutes,
			SavedMXN:     r.SavedMXN,
		})
	}
	return out, nil
}

func (a *roiDashboardAdapter) CostMXNPerMin() float64 {
	return roi.DevCostMXNPerMin()
}

// Compile-time guard.
var _ dashboard.ROIService = (*roiDashboardAdapter)(nil)
