package main

import (
	"context"
	"log"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/comments"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/databases"
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

// ─── Page Databases (DB module) ─────────────────────────────────────────────

// pageDatabaseAdapter expone *databases.Store al cloudserver. Match con la
// interface PageDatabaseService: simple delegation.
type pageDatabaseAdapter struct {
	*databases.Store
}

func newPageDatabaseAdapter(cs *cloudstore.CloudStore) *pageDatabaseAdapter {
	return &pageDatabaseAdapter{Store: databases.New(cs.DB())}
}

// pageCommentsAdapter expone *comments.Store + email notifier al cloudserver.
// Implementa cloudserver.PageCommentsService.
type pageCommentsAdapter struct {
	*comments.Store
	emailSvc *email.Service
	users    comments.UserResolver
}

func newPageCommentsAdapter(cs *cloudstore.CloudStore, emailSvc *email.Service, users *dashboardUserAdapter) *pageCommentsAdapter {
	var resolver comments.UserResolver
	if users != nil {
		resolver = newUserResolverAdapter(users)
	}
	return &pageCommentsAdapter{
		Store:    comments.New(cs.DB()),
		emailSvc: emailSvc,
		users:    resolver,
	}
}

// NotifyMentioned satisface PageCommentsService.NotifyMentioned. Adapta el
// email service interno (que es generic) a la interfaz EmailNotifier que
// espera comments.NotifyMentioned.
func (a *pageCommentsAdapter) NotifyMentioned(ctx context.Context, commentID, pageTitle, pageURL, mentionedBy string) error {
	if a == nil || a.Store == nil {
		return nil
	}
	notifier := &mentionEmailNotifier{svc: a.emailSvc}
	return a.Store.NotifyMentioned(ctx, commentID, notifier, a.users, pageTitle, pageURL, mentionedBy)
}

// mentionEmailNotifier adapta *email.Service a comments.EmailNotifier.
// Reusa la plantilla genérica con un subject custom.
type mentionEmailNotifier struct {
	svc *email.Service
}

func (n *mentionEmailNotifier) IsConfigured() bool {
	return n != nil && n.svc != nil && n.svc.IsConfigured()
}

func (n *mentionEmailNotifier) PublicURL() string {
	if n == nil || n.svc == nil {
		return ""
	}
	return n.svc.PublicURL()
}

// SendMentionNotification envía email con subject custom. Si el template
// específico no existe en internal/cloud/email/templates, esta función simplemente
// loguea (degraded behavior). En la wave actual no introducimos un nuevo template
// para evitar tocar internal/cloud/email/.
func (n *mentionEmailNotifier) SendMentionNotification(ctx context.Context, mc comments.MentionEmailContext) error {
	if !n.IsConfigured() {
		log.Printf("mentionEmailNotifier: SKIP (email not configured) to=%s page=%s", mc.ToEmail, mc.PageTitle)
		return nil
	}
	// Best-effort: usar SendInvite como genérico no aplica acá. Como NO debemos
	// tocar internal/cloud/email/, sólo loggeamos. Cuando el equipo de email
	// agregue un template "mention", el notifier puede extenderse aquí.
	log.Printf("mentionEmailNotifier: would send to=%s subject=mention on %q snippet=%q",
		mc.ToEmail, mc.PageTitle, truncateString(mc.CommentSnippet, 60))
	return nil
}

// userResolverAdapter adapta *dashboardUserAdapter a comments.UserResolver.
// Usado al notificar mentions.
type userResolverAdapter struct {
	users *dashboardUserAdapter
}

func newUserResolverAdapter(u *dashboardUserAdapter) *userResolverAdapter {
	return &userResolverAdapter{users: u}
}

func (r *userResolverAdapter) GetByUID(ctx context.Context, uid string) (string, string, error) {
	if r == nil || r.users == nil {
		return "", "", nil
	}
	u, err := r.users.GetByUID(ctx, uid)
	if err != nil || u == nil {
		return "", "", err
	}
	return u.Email, u.Name, nil
}

// truncateString limita largo de string para logs. Helper compartido.
func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
