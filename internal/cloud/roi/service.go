// Package roi calcula y persiste métricas de Return-On-Investment de ARIA Core
// (TTC, RDR, CWR, SVR, DTT) y consolida ahorro tiempo+MXN para el dashboard
// /dashboard/roi y el comando aria-core roi.
package roi

import (
	"context"
	"time"
)

// Service es el contrato que cloudserver/dashboard/CLI consumen.
//
// La implementación de referencia está en roi.MetricsStore + roi.Service{}
// (un thin wrapper). Para tests se mockea con un struct que cumpla esta interface.
type Service interface {
	CalcTTC(ctx context.Context, devUID string, since, until time.Time) (avgMinutes float64, samples int, err error)
	CalcRDR(ctx context.Context, devUID string, since, until time.Time) (pct float64, total int, err error)
	CalcCWR(ctx context.Context, devUID string, since, until time.Time) (pct float64, err error)
	CalcSVR(ctx context.Context, devUID string, since, until time.Time) (pct float64, err error)
	CalcDTT(ctx context.Context, devUID string, since, until time.Time) (avgMinutes float64, err error)
	CalculateSavings(ctx context.Context, devUID string, since, until time.Time) (*SavingsView, error)
	TopContributors(ctx context.Context, since time.Time, limit int) ([]ContributorScore, error)
	PerClientBreakdown(ctx context.Context, since time.Time) ([]ClientROI, error)
	WeeklyTimeline(ctx context.Context, devUID string, weeks int, costPerMin float64) ([]WeeklyPoint, error)
	LogSearch(ctx context.Context, p LogSearchParams) error
}

// Compile-time assertion: MetricsStore satisface Service.
var _ Service = (*MetricsStore)(nil)
