package main

import (
	"context"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages"
)

// pagesDashboardAdapter conecta pages.PgStore al contrato dashboard.PagesDashboardService.
// Mantiene dashboard sin depender directamente del paquete pages (regla del proyecto:
// dashboard no importa packages de feature).
type pagesDashboardAdapter struct {
	store *pages.PgStore
}

func newPagesDashboardAdapter(cs *cloudstore.CloudStore) *pagesDashboardAdapter {
	return &pagesDashboardAdapter{store: pages.NewPgStore(cs.DB())}
}

// Tree retorna las páginas no archivadas filtradas por project/scope.
func (a *pagesDashboardAdapter) Tree(ctx context.Context, project, scope string) ([]dashboard.PageView, error) {
	rs, err := a.store.Tree(ctx, project, scope)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.PageView, 0, len(rs))
	for _, p := range rs {
		out = append(out, toDashboardPageView(p))
	}
	return out, nil
}

func (a *pagesDashboardAdapter) Get(ctx context.Context, id string) (*dashboard.PageView, error) {
	p, err := a.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	v := toDashboardPageView(p)
	return &v, nil
}

func (a *pagesDashboardAdapter) Create(ctx context.Context, in dashboard.CreatePageInput) (*dashboard.PageView, error) {
	// Si el caller paso template_key, hidratar contenido desde el builtin.
	contentMD := in.ContentMD
	icon := in.Icon
	if in.TemplateKey != "" {
		if tpl := pages.GetBuiltinTemplate(in.TemplateKey); tpl != nil {
			if contentMD == "" {
				contentMD = tpl.BodyMD
			}
			if icon == "" {
				icon = tpl.Icon
			}
		}
	}
	p, err := a.store.Create(ctx, pages.CreateParams{
		ParentID:     in.ParentID,
		Title:        in.Title,
		ContentMD:    contentMD,
		Icon:         icon,
		Project:      in.Project,
		Scope:        in.Scope,
		Sensitivity:  in.Sensitivity,
		TemplateKey:  in.TemplateKey,
		PageType:     "doc",
		CreatedByUID: in.CreatedByUID,
	})
	if err != nil {
		return nil, err
	}
	v := toDashboardPageView(p)
	return &v, nil
}

func (a *pagesDashboardAdapter) Update(ctx context.Context, id string, in dashboard.UpdatePageInput) (*dashboard.PageView, error) {
	p, err := a.store.Update(ctx, id, pages.UpdateParams{
		Title:        in.Title,
		ContentMD:    in.ContentMD,
		Icon:         in.Icon,
		Project:      in.Project,
		Scope:        in.Scope,
		Sensitivity:  in.Sensitivity,
		UpdatedByUID: in.UpdatedByUID,
		EditSummary:  in.EditSummary,
	})
	if err != nil {
		return nil, err
	}
	v := toDashboardPageView(p)
	return &v, nil
}

func (a *pagesDashboardAdapter) Move(ctx context.Context, id, newParentID string, newSortOrder int) error {
	return a.store.Move(ctx, id, newParentID, newSortOrder)
}

func (a *pagesDashboardAdapter) Archive(ctx context.Context, id string) error {
	return a.store.Archive(ctx, id)
}

func (a *pagesDashboardAdapter) Restore(ctx context.Context, id string) error {
	return a.store.Restore(ctx, id)
}

func (a *pagesDashboardAdapter) ListRevisions(ctx context.Context, pageID string, limit int) ([]dashboard.PageRevisionView, error) {
	rs, err := a.store.ListRevisions(ctx, pageID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.PageRevisionView, 0, len(rs))
	for _, r := range rs {
		out = append(out, dashboard.PageRevisionView{
			ID:          r.ID,
			PageID:      r.PageID,
			Title:       r.Title,
			ContentMD:   r.ContentMD,
			EditedByUID: r.EditedByUID,
			EditSummary: r.EditSummary,
			CreatedAt:   r.CreatedAt,
		})
	}
	return out, nil
}

func (a *pagesDashboardAdapter) RevertToRevision(ctx context.Context, pageID, revisionID, byUID string) error {
	return a.store.RevertToRevision(ctx, pageID, revisionID, byUID)
}

func (a *pagesDashboardAdapter) ListTemplates() []dashboard.PageTemplateView {
	tpls := pages.BuiltinTemplates()
	out := make([]dashboard.PageTemplateView, 0, len(tpls))
	for _, t := range tpls {
		out = append(out, dashboard.PageTemplateView{
			Key:         t.Key,
			Name:        t.Name,
			Description: t.Description,
			Icon:        t.Icon,
		})
	}
	return out
}

func (a *pagesDashboardAdapter) QuickSearchAll(ctx context.Context, query string, limit int) (*dashboard.QuickSearchView, error) {
	res, err := a.store.QuickSearchAll(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	return &dashboard.QuickSearchView{
		Pages:        toDashboardHits(res.Pages),
		Observations: toDashboardHits(res.Observations),
		Skills:       toDashboardHits(res.Skills),
		Recipes:      toDashboardHits(res.Recipes),
		Leads:        toDashboardHits(res.Leads),
		Quotes:       toDashboardHits(res.Quotes),
	}, nil
}

// Store expone el PgStore subyacente para CLI/MCP que necesitan operaciones
// fuera del contrato dashboard (Slug, SeedTemplates, etc.).
func (a *pagesDashboardAdapter) Store() *pages.PgStore {
	return a.store
}

// ─── Mapping helpers ────────────────────────────────────────────────────────

func toDashboardPageView(p *pages.Page) dashboard.PageView {
	if p == nil {
		return dashboard.PageView{}
	}
	view := dashboard.PageView{
		ID:            p.ID,
		ParentID:      p.ParentID,
		Title:         p.Title,
		ContentMD:     p.ContentMD,
		Icon:          p.Icon,
		Project:       p.Project,
		Scope:         p.Scope,
		ClientID:      p.ClientID,
		PageType:      p.PageType,
		TemplateKey:   p.TemplateKey,
		Sensitivity:   p.Sensitivity,
		SortOrder:     p.SortOrder,
		IsArchived:    p.IsArchived,
		CreatedByUID:  p.CreatedByUID,
		CreatedAt:     p.CreatedAt,
		UpdatedByUID:  p.UpdatedByUID,
		UpdatedAt:     p.UpdatedAt,
		ChildrenCount: p.ChildrenCount,
	}
	for _, b := range p.Path {
		view.Path = append(view.Path, dashboard.PageBreadcrumbView{
			ID: b.ID, Title: b.Title, Icon: b.Icon,
		})
	}
	return view
}

func toDashboardHits(hs []pages.QuickHit) []dashboard.QuickHitView {
	out := make([]dashboard.QuickHitView, 0, len(hs))
	for _, h := range hs {
		out = append(out, dashboard.QuickHitView{
			ID:       h.ID,
			Type:     h.Type,
			Title:    h.Title,
			Subtitle: h.Subtitle,
			URL:      h.URL,
			Score:    h.Score,
		})
	}
	return out
}
