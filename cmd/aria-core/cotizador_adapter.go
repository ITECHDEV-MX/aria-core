package main

import (
	"context"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// cotizadorAdapter conecta cotizador.Store al contrato dashboard.CotizadorService.
type cotizadorAdapter struct {
	store     *cotizador.Store
	notifier  quoteNotifier // optional — nil-safe; set via setNotifier.
}

// quoteNotifier is the contract used by the cotizador adapter to fire emails
// after a quote status transition. It mirrors a subset of EmailService but
// is internally typed so the adapter doesn't have a hard cloudserver dep.
type quoteNotifier interface {
	NotifyQuoteStatusChange(ctx context.Context, quoteID, fromStatus, newStatus, byUID, notes string)
	NotifyQuoteClose(ctx context.Context, quoteID, fromStatus, newStatus, byUID, reason string)
}

func newCotizadorAdapter(cs *cloudstore.CloudStore) *cotizadorAdapter {
	return &cotizadorAdapter{store: cotizador.New(cs.DB())}
}

// setNotifier wires the optional async notifier (post-commit email hook).
func (a *cotizadorAdapter) setNotifier(n quoteNotifier) {
	if a == nil {
		return
	}
	a.notifier = n
}

func (a *cotizadorAdapter) ListLeads(ctx context.Context, status string) ([]dashboard.CotizadorLeadView, error) {
	leads, err := a.store.ListLeads(ctx, status)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorLeadView, 0, len(leads))
	for _, l := range leads {
		out = append(out, toLeadView(l))
	}
	return out, nil
}

func (a *cotizadorAdapter) GetLead(ctx context.Context, id string) (*dashboard.CotizadorLeadView, error) {
	l, err := a.store.GetLead(ctx, id)
	if err != nil {
		return nil, err
	}
	v := toLeadView(l)
	return &v, nil
}

func (a *cotizadorAdapter) CreateLead(ctx context.Context, in dashboard.CreateLeadInput) (*dashboard.CotizadorLeadView, error) {
	l, err := a.store.CreateLead(ctx, cotizador.CreateLeadParams{
		Name: in.Name, Company: in.Company, Email: in.Email, Phone: in.Phone,
		Source: in.Source, Notes: in.Notes,
		CreatedByUID: in.CreatedByUID, Role: in.Role,
	})
	if err != nil {
		return nil, err
	}
	v := toLeadView(l)
	return &v, nil
}

func (a *cotizadorAdapter) UpdateLead(ctx context.Context, id, name, company, email, phone, source, notes string) error {
	return a.store.UpdateLead(ctx, id, name, company, email, phone, source, notes, "")
}

func (a *cotizadorAdapter) UpdateLeadStatus(ctx context.Context, id, newStatus, byUID, notes string) error {
	return a.store.UpdateLeadStatus(ctx, id, newStatus, byUID, notes)
}

func (a *cotizadorAdapter) LeadHistory(ctx context.Context, leadID string, limit int) ([]dashboard.CotizadorLeadHistoryView, error) {
	entries, err := a.store.LeadHistory(ctx, leadID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorLeadHistoryView, 0, len(entries))
	for _, e := range entries {
		v := dashboard.CotizadorLeadHistoryView{
			Action:     e.Action,
			Notes:      e.Notes,
			OccurredAt: e.OccurredAt,
		}
		if e.FromStatus.Valid {
			v.FromStatus = e.FromStatus.String
		}
		if e.ToStatus.Valid {
			v.ToStatus = e.ToStatus.String
		}
		out = append(out, v)
	}
	return out, nil
}

func (a *cotizadorAdapter) CountLeadsByStatus(ctx context.Context) (map[string]int, error) {
	return a.store.CountByStatus(ctx)
}

func toLeadView(l *cotizador.Lead) dashboard.CotizadorLeadView {
	return dashboard.CotizadorLeadView{
		ID:            l.ID,
		Name:          l.Name,
		Company:       l.Company,
		Email:         l.Email,
		Phone:         l.Phone,
		Source:        l.Source,
		Status:        l.Status,
		Notes:         l.Notes,
		CreatedByRole: l.CreatedByRole,
		CreatedAt:     l.CreatedAt,
		UpdatedAt:     l.UpdatedAt,
	}
}
