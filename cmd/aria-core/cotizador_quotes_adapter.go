package main

import (
	"context"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
)

// === RFPs ===

func (a *cotizadorAdapter) ListRFPsByLead(ctx context.Context, leadID string) ([]dashboard.CotizadorRFPView, error) {
	rfps, err := a.store.ListRFPsByLead(ctx, leadID)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorRFPView, 0, len(rfps))
	for _, r := range rfps {
		out = append(out, toRFPView(r))
	}
	return out, nil
}

func (a *cotizadorAdapter) GetRFP(ctx context.Context, id string) (*dashboard.CotizadorRFPView, error) {
	r, err := a.store.GetRFP(ctx, id)
	if err != nil {
		return nil, err
	}
	v := toRFPView(r)
	return &v, nil
}

func (a *cotizadorAdapter) CreateRFP(ctx context.Context, in dashboard.CreateRFPInput) (*dashboard.CotizadorRFPView, error) {
	r, err := a.store.CreateRFP(ctx, cotizador.CreateRFPParams{
		LeadID: in.LeadID, SourceType: in.SourceType, SourceContent: in.SourceContent,
		AnalysisJSON: in.AnalysisJSON, CreatedByUID: in.CreatedByUID,
	})
	if err != nil {
		return nil, err
	}
	v := toRFPView(r)
	return &v, nil
}

func (a *cotizadorAdapter) UpdateRFPAnalysis(ctx context.Context, id, analysisJSON string) error {
	return a.store.UpdateRFPAnalysis(ctx, id, analysisJSON)
}

// === Quotes ===

func (a *cotizadorAdapter) ListQuotesByLead(ctx context.Context, leadID string) ([]dashboard.CotizadorQuoteView, error) {
	quotes, err := a.store.ListQuotesByLead(ctx, leadID)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorQuoteView, 0, len(quotes))
	for _, q := range quotes {
		out = append(out, toQuoteView(q))
	}
	return out, nil
}

func (a *cotizadorAdapter) GetQuote(ctx context.Context, id string) (*dashboard.CotizadorQuoteView, error) {
	q, err := a.store.GetQuote(ctx, id)
	if err != nil {
		return nil, err
	}
	v := toQuoteView(q)
	return &v, nil
}

func (a *cotizadorAdapter) ListQuoteItems(ctx context.Context, quoteID string) ([]dashboard.CotizadorQuoteItemView, error) {
	items, err := a.store.ListQuoteItems(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorQuoteItemView, 0, len(items))
	for _, it := range items {
		out = append(out, dashboard.CotizadorQuoteItemView{
			ID: it.ID, SKU: it.SKU, Description: it.Description,
			Qty: it.Qty, UnitPrice: it.UnitPrice, Subtotal: it.Subtotal, SortOrder: it.SortOrder,
		})
	}
	return out, nil
}

func (a *cotizadorAdapter) CreateQuote(ctx context.Context, in dashboard.CreateQuoteInput) (*dashboard.CotizadorQuoteView, error) {
	items := make([]cotizador.CreateQuoteItemParams, 0, len(in.Items))
	for _, it := range in.Items {
		items = append(items, cotizador.CreateQuoteItemParams{
			SKU: it.SKU, Description: it.Description, Qty: it.Qty, UnitPrice: it.UnitPrice,
		})
	}
	q, err := a.store.CreateQuote(ctx, cotizador.CreateQuoteParams{
		LeadID: in.LeadID, RFPID: in.RFPID, Currency: in.Currency, ValidUntil: in.ValidUntil,
		Terms: in.Terms, Justification: in.Justification,
		CreatedByUID: in.CreatedByUID, Role: in.Role, Items: items,
	})
	if err != nil {
		return nil, err
	}
	v := toQuoteView(q)
	return &v, nil
}

func (a *cotizadorAdapter) UpdateQuoteStatus(ctx context.Context, quoteID, newStatus, byUID, notes string) error {
	// Capture fromStatus pre-update so the notifier knows the transition.
	fromStatus := ""
	if q, err := a.store.GetQuote(ctx, quoteID); err == nil && q != nil {
		fromStatus = q.Status
	}
	if err := a.store.UpdateQuoteStatus(ctx, quoteID, newStatus, byUID, notes); err != nil {
		return err
	}
	if a.notifier != nil {
		a.notifier.NotifyQuoteStatusChange(ctx, quoteID, fromStatus, newStatus, byUID, notes)
	}
	return nil
}

func (a *cotizadorAdapter) QuoteHistory(ctx context.Context, quoteID string, limit int) ([]dashboard.CotizadorQuoteHistoryView, error) {
	entries, err := a.store.QuoteHistory(ctx, quoteID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorQuoteHistoryView, 0, len(entries))
	for _, e := range entries {
		v := dashboard.CotizadorQuoteHistoryView{
			Action: e.Action, Notes: e.Notes, OccurredAt: e.OccurredAt,
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

// === converters ===

func toRFPView(r *cotizador.RFP) dashboard.CotizadorRFPView {
	return dashboard.CotizadorRFPView{
		ID: r.ID, LeadID: r.LeadID, SourceType: r.SourceType,
		SourceContent: r.SourceContent, AnalysisJSON: r.AnalysisJSON, CreatedAt: r.CreatedAt,
	}
}

func toQuoteView(q *cotizador.Quote) dashboard.CotizadorQuoteView {
	v := dashboard.CotizadorQuoteView{
		ID: q.ID, LeadID: q.LeadID, Version: q.Version, Status: q.Status,
		Currency: q.Currency, Subtotal: q.Subtotal, Taxes: q.Taxes, Total: q.Total,
		Terms: q.Terms, Justification: q.Justification,
		CreatedByRole: q.CreatedByRole, CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt,
		ProposalType:            q.ProposalType,
		ProductName:             q.ProductName,
		ProductSubtitle:         q.ProductSubtitle,
		Tags:                    q.Tags,
		PreparedForCompany:      q.PreparedForCompany,
		PreparedForArea:         q.PreparedForArea,
		PreparedForContactName:  q.PreparedForContactName,
		PreparedForContactEmail: q.PreparedForContactEmail,
		PreparedByName:          q.PreparedByName,
		PreparedByEmail:         q.PreparedByEmail,
		PreparedByRole:          q.PreparedByRole,
	}
	if q.RFPID.Valid {
		v.RFPID = q.RFPID.String
	}
	if q.Folio.Valid {
		v.Folio = q.Folio.String
	}
	if q.ValidUntil.Valid {
		t := q.ValidUntil.Time
		v.ValidUntil = &t
	}
	if q.ApprovedAt.Valid {
		t := q.ApprovedAt.Time
		v.ApprovedAt = &t
	}
	if q.IssueDate.Valid {
		t := q.IssueDate.Time
		v.IssueDate = &t
	}
	return v
}

// === Proposal sections (commit 7) ===

func (a *cotizadorAdapter) ListSections(ctx context.Context, quoteID string) ([]dashboard.CotizadorQuoteSectionView, error) {
	secs, err := a.store.ListSections(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.CotizadorQuoteSectionView, 0, len(secs))
	for _, s := range secs {
		out = append(out, dashboard.CotizadorQuoteSectionView{
			ID: s.ID, Key: s.Key, Title: s.Title, ContentMD: s.ContentMD, SortOrder: s.SortOrder,
		})
	}
	return out, nil
}

func (a *cotizadorAdapter) UpsertSection(ctx context.Context, quoteID, key, title, contentMD string, sortOrder int) error {
	return a.store.UpsertSection(ctx, quoteID, key, title, contentMD, sortOrder)
}

func (a *cotizadorAdapter) DeleteSection(ctx context.Context, quoteID, key string) error {
	return a.store.DeleteSection(ctx, quoteID, key)
}

func (a *cotizadorAdapter) UpdateProposalHeader(ctx context.Context, quoteID string, in dashboard.UpdateProposalHeaderInput) error {
	return a.store.UpdateProposalHeader(ctx, quoteID, cotizador.UpdateProposalHeaderParams{
		Folio: in.Folio, ProposalType: in.ProposalType,
		ProductName: in.ProductName, ProductSubtitle: in.ProductSubtitle, Tags: in.Tags,
		PreparedForCompany: in.PreparedForCompany, PreparedForArea: in.PreparedForArea,
		PreparedForContactName: in.PreparedForContactName, PreparedForContactEmail: in.PreparedForContactEmail,
		IssueDate: in.IssueDate, PreparedByName: in.PreparedByName,
		PreparedByEmail: in.PreparedByEmail, PreparedByRole: in.PreparedByRole,
	})
}

// === Templates (commit 9) ===

func (a *cotizadorAdapter) ListTemplates() []dashboard.CotizadorTemplateView {
	tmpls := cotizador.AvailableTemplates()
	out := make([]dashboard.CotizadorTemplateView, 0, len(tmpls))
	for _, t := range tmpls {
		out = append(out, dashboard.CotizadorTemplateView{
			Key: t.Key, Name: t.Name, Description: t.Description,
			ProposalType: t.ProposalType, DefaultProduct: t.DefaultProduct,
			DefaultSubtitle: t.DefaultSubtitle, DefaultTags: t.DefaultTags,
			SectionCount: len(t.Sections),
		})
	}
	return out
}

func (a *cotizadorAdapter) ApplyTemplate(ctx context.Context, quoteID, templateKey string) error {
	return a.store.ApplyTemplate(ctx, quoteID, templateKey)
}
