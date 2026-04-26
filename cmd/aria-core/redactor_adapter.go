package main

import (
	"context"
	"database/sql"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/redactor"
)

// redactorDashboardAdapter conecta el módulo redactor al contrato del dashboard.
// Mantiene el dashboard libre de la dependencia directa con redactor (necesario
// porque dashboard ya tiene muchas tools y no queremos colas de import).
type redactorDashboardAdapter struct {
	db  *sql.DB
	svc redactor.Service
}

func newRedactorDashboardAdapter(db *sql.DB, svc redactor.Service) *redactorDashboardAdapter {
	return &redactorDashboardAdapter{db: db, svc: svc}
}

func (a *redactorDashboardAdapter) ListEgress(ctx context.Context, filter dashboard.EgressFilter, limit, offset int) ([]dashboard.EgressRow, int, error) {
	rs, total, err := redactor.ListEgress(ctx, a.db, redactor.EgressFilter{
		From:     filter.From,
		To:       filter.To,
		ClientID: filter.ClientID,
		UserUID:  filter.UserUID,
		Provider: filter.Provider,
	}, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	out := make([]dashboard.EgressRow, 0, len(rs))
	for _, r := range rs {
		out = append(out, dashboard.EgressRow{
			ID: r.ID, RequestID: r.RequestID, ObservationID: r.ObservationID,
			LLMProvider: r.LLMProvider, LLMModel: r.LLMModel,
			ClientID: r.ClientID, UserUID: r.UserUID,
			Scrubbed: r.Scrubbed, RedactionsRaw: r.RedactionsRaw,
			PayloadHash: r.PayloadHash, PayloadSize: r.PayloadSize,
			Reason: r.Reason, OccurredAt: r.OccurredAt,
		})
	}
	return out, total, nil
}

func (a *redactorDashboardAdapter) StatsLastDays(ctx context.Context, days int) (*dashboard.EgressStatsView, error) {
	st, err := redactor.Stats(ctx, a.db, days)
	if err != nil {
		return nil, err
	}
	return &dashboard.EgressStatsView{
		WindowDays:    st.WindowDays,
		TotalRequests: st.TotalRequests,
		TotalScrubbed: st.TotalScrubbed,
		TotalBypassed: st.TotalBypassed,
		BytesSent:     st.BytesSent,
		ByProvider:    st.ByProvider,
		ByReason:      st.ByReason,
	}, nil
}

func (a *redactorDashboardAdapter) RevealAlias(ctx context.Context, token string) (string, string, error) {
	return redactor.RevealAlias(ctx, a.db, token)
}

// ─── ScrubGate adapter (runtime, used by cloudserver v1_memory) ─────────────

// scrubGateAdapter wires redactor.Service into the cloudserver.ScrubGate
// contract so cloudserver does not import the redactor package directly.
type scrubGateAdapter struct {
	svc redactor.Service
}

func newScrubGateAdapter(svc redactor.Service) *scrubGateAdapter {
	return &scrubGateAdapter{svc: svc}
}

func (a *scrubGateAdapter) CanSendToLLM(sensitivity, provider string) bool {
	type stringPolicy interface {
		CanSendToLLMString(sensitivity, provider string) bool
	}
	if sp, ok := a.svc.(stringPolicy); ok {
		return sp.CanSendToLLMString(sensitivity, provider)
	}
	return a.svc.CanSendToLLM(redactor.Sensitivity(sensitivity), provider)
}

func (a *scrubGateAdapter) ScrubString(ctx context.Context, text string) (string, string) {
	type stringScrub interface {
		ScrubString(ctx context.Context, text string) (string, string)
	}
	if ss, ok := a.svc.(stringScrub); ok {
		return ss.ScrubString(ctx, text)
	}
	return text, "[]"
}

func (a *scrubGateAdapter) LogEgress(
	ctx context.Context,
	requestID, observationID, provider, model, clientID, userUID, reason, payloadHash string,
	payloadSize int,
	scrubbed bool,
	redactionsJSON string,
) error {
	type stringLogger interface {
		LogEgressString(ctx context.Context, requestID, observationID, provider, model, clientID, userUID, reason, payloadHash string, payloadSize int, scrubbed bool, redactionsJSON string) error
	}
	if sl, ok := a.svc.(stringLogger); ok {
		return sl.LogEgressString(ctx, requestID, observationID, provider, model, clientID, userUID, reason, payloadHash, payloadSize, scrubbed, redactionsJSON)
	}
	return nil
}
