package main

import (
	"context"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

func (a *cotizadorAdapter) CloseQuoteWithOutcome(ctx context.Context, quoteID, newStatus, byUID, reason, lessonText string, lessonTags []string) error {
	fromStatus := ""
	if q, err := a.store.GetQuote(ctx, quoteID); err == nil && q != nil {
		fromStatus = q.Status
	}
	if err := a.store.CloseQuoteWithOutcome(ctx, quoteID, newStatus, byUID, reason, lessonText, lessonTags); err != nil {
		return err
	}
	if a.notifier != nil {
		a.notifier.NotifyQuoteClose(ctx, quoteID, fromStatus, newStatus, byUID, reason)
	}
	return nil
}

func (a *cotizadorAdapter) SearchSimilarItems(ctx context.Context, query string, limit int) ([]dashboard.CotizadorSimilarItemView, error) {
	hits, err := a.store.SearchSimilarItems(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorSimilarItemView, 0, len(hits))
	for _, h := range hits {
		out = append(out, dashboard.CotizadorSimilarItemView{
			QuoteID: h.QuoteID, Version: h.Version, QuoteStatus: h.QuoteStatus,
			LeadID: h.LeadID, LeadName: h.LeadName, LeadCompany: h.LeadCompany,
			SKU: h.SKU, Description: h.Description, Qty: h.Qty,
			UnitPrice: h.UnitPrice, Subtotal: h.Subtotal, Currency: h.Currency,
			QuoteCreated: h.QuoteCreated, Rank: h.Rank,
		})
	}
	return out, nil
}

func (a *cotizadorAdapter) GetDashboardStats(ctx context.Context) (*dashboard.CotizadorDashboardStatsView, error) {
	st, err := a.store.GetDashboardStats(ctx)
	if err != nil {
		return nil, err
	}
	v := &dashboard.CotizadorDashboardStatsView{
		CotizadorOutcomeStatsView: dashboard.CotizadorOutcomeStatsView{
			Total: st.Total, Won: st.Won, Lost: st.Lost, Expired: st.Expired, Open: st.Open,
			WinRate: st.WinRate, AvgWonTotal: st.AvgWonTotal, AvgLostTotal: st.AvgLostTotal,
		},
		LeadsByStatus:    st.LeadsByStatus,
		QuotesByStatus:   st.QuotesByStatus,
		PipelineValue:    st.PipelineValue,
		AvgDealSize:      st.AvgDealSize,
		TotalPipelineMXN: st.TotalPipelineMXN,
		TopCurrencies:    st.TopCurrencies,
	}
	for _, p := range st.MonthlyTrend {
		v.MonthlyTrend = append(v.MonthlyTrend, dashboard.CotizadorMonthlyPoint{
			Month: p.Month, Created: p.Created, Won: p.Won, Lost: p.Lost, WonMXN: p.WonMXN,
		})
	}
	return v, nil
}

func (a *cotizadorAdapter) GetOutcomeStats(ctx context.Context) (dashboard.CotizadorOutcomeStatsView, error) {
	st, err := a.store.GetOutcomeStats(ctx)
	if err != nil {
		return dashboard.CotizadorOutcomeStatsView{}, err
	}
	return dashboard.CotizadorOutcomeStatsView{
		Total: st.Total, Won: st.Won, Lost: st.Lost, Expired: st.Expired, Open: st.Open,
		WinRate: st.WinRate, AvgWonTotal: st.AvgWonTotal, AvgLostTotal: st.AvgLostTotal,
	}, nil
}

func (a *cotizadorAdapter) GetClientHistory(ctx context.Context, query string) ([]dashboard.CotizadorClientHistoryView, error) {
	hist, err := a.store.GetClientHistory(ctx, query)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorClientHistoryView, 0, len(hist))
	for _, c := range hist {
		v := dashboard.CotizadorClientHistoryView{
			LeadID: c.LeadID, LeadName: c.LeadName, Company: c.Company,
			QuoteCount: c.QuoteCount, WonCount: c.WonCount, LostCount: c.LostCount,
			TotalSold: c.TotalSold,
		}
		if c.LastQuoteAt.Valid {
			t := c.LastQuoteAt.Time
			v.LastQuoteAt = &t
		}
		out = append(out, v)
	}
	return out, nil
}

func (a *cotizadorAdapter) SearchLessons(ctx context.Context, query, tag string, limit int) ([]dashboard.CotizadorLessonView, error) {
	lessons, err := a.store.SearchLessons(ctx, query, tag, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorLessonView, 0, len(lessons))
	for _, l := range lessons {
		out = append(out, toLessonView(l))
	}
	return out, nil
}

func (a *cotizadorAdapter) CreateLesson(ctx context.Context, in dashboard.CreateLessonInput) (*dashboard.CotizadorLessonView, error) {
	l, err := a.store.CreateLesson(ctx, cotizador.CreateLessonParams{
		QuoteID: in.QuoteID, LeadID: in.LeadID, Text: in.Text,
		Tags: in.Tags, CreatedByUID: in.CreatedByUID, Role: in.Role,
	})
	if err != nil {
		return nil, err
	}
	v := toLessonView(l)
	return &v, nil
}

func (a *cotizadorAdapter) PromoteLeadToClient(ctx context.Context, in dashboard.PromoteLeadInput) (*dashboard.CotizadorClientView, error) {
	c, err := a.store.PromoteLeadToClient(ctx, cotizador.PromoteLeadParams{
		LeadID: in.LeadID, LegalName: in.LegalName, RFC: in.RFC,
		FiscalAddress: in.FiscalAddress, BillingEmail: in.BillingEmail,
		ContactsJSON: in.ContactsJSON, Notes: in.Notes, CreatedByUID: in.CreatedByUID,
	})
	if err != nil {
		return nil, err
	}
	v := toClientView(c)
	return &v, nil
}

func (a *cotizadorAdapter) ListClients(ctx context.Context) ([]dashboard.CotizadorClientView, error) {
	clients, err := a.store.ListClients(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorClientView, 0, len(clients))
	for _, c := range clients {
		out = append(out, toClientView(c))
	}
	return out, nil
}

func (a *cotizadorAdapter) GetClient(ctx context.Context, id string) (*dashboard.CotizadorClientView, error) {
	c, err := a.store.GetClient(ctx, id)
	if err != nil {
		return nil, err
	}
	v := toClientView(c)
	return &v, nil
}

func toClientView(c *cotizador.Client) dashboard.CotizadorClientView {
	v := dashboard.CotizadorClientView{
		ID: c.ID, LegalName: c.LegalName, RFC: c.RFC,
		FiscalAddress: c.FiscalAddress, BillingEmail: c.BillingEmail,
		ContactsJSON: c.ContactsJSON, Notes: c.Notes, CreatedAt: c.CreatedAt,
	}
	if c.LeadID.Valid {
		v.LeadID = c.LeadID.String
	}
	return v
}

func toLessonView(l *cotizador.Lesson) dashboard.CotizadorLessonView {
	v := dashboard.CotizadorLessonView{
		ID: l.ID, Text: l.Text, Tags: l.Tags,
		CreatedByRole: l.CreatedByRole, CreatedAt: l.CreatedAt,
	}
	if l.QuoteID.Valid {
		v.QuoteID = l.QuoteID.String
	}
	if l.LeadID.Valid {
		v.LeadID = l.LeadID.String
	}
	return v
}
