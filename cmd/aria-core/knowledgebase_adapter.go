package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cotizador"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/github"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/knowledgebase"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages"
)

// pagesAdapter implementa knowledgebase.PageReader sobre pages.PgStore.
type pagesAdapter struct {
	store pages.Store
}

func (a *pagesAdapter) GetPage(ctx context.Context, id string) (*knowledgebase.PageRecord, error) {
	if a == nil || a.store == nil {
		return nil, fmt.Errorf("pages store not configured")
	}
	p, err := a.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, pages.ErrNotFound) {
			return nil, knowledgebase.ErrEntityNotFound
		}
		return nil, err
	}
	return &knowledgebase.PageRecord{
		ID:           p.ID,
		Title:        p.Title,
		ContentMD:    p.ContentMD,
		Project:      p.Project,
		TemplateKey:  p.TemplateKey,
		CreatedByUID: p.CreatedByUID,
		CreatedAt:    p.CreatedAt,
		UpdatedAt:    p.UpdatedAt,
		Status:       "", // pages no tiene status field hoy; vendrá de databases o frontmatter
	}, nil
}

// quotesAdapter implementa knowledgebase.QuoteReader sobre cotizador.Store.
type quotesAdapter struct {
	store *cotizador.Store
	db    *sql.DB
}

func (a *quotesAdapter) GetQuote(ctx context.Context, id string) (*knowledgebase.QuoteRecord, error) {
	if a == nil || a.store == nil {
		return nil, fmt.Errorf("quotes store not configured")
	}
	q, err := a.store.GetQuote(ctx, id)
	if err != nil {
		if errors.Is(err, cotizador.ErrQuoteNotFound) {
			return nil, knowledgebase.ErrEntityNotFound
		}
		return nil, err
	}
	items, _ := a.store.ListQuoteItems(ctx, id)
	sections, _ := a.store.ListSections(ctx, id)

	rec := &knowledgebase.QuoteRecord{
		ID:                      q.ID,
		LeadID:                  q.LeadID,
		Folio:                   strNull(q.Folio),
		Status:                  q.Status,
		Currency:                q.Currency,
		Subtotal:                q.Subtotal,
		Taxes:                   q.Taxes,
		Total:                   q.Total,
		ProductName:             q.ProductName,
		ProductSubtitle:         q.ProductSubtitle,
		ProposalType:            q.ProposalType,
		Tags:                    append([]string(nil), q.Tags...),
		PreparedForCompany:      q.PreparedForCompany,
		PreparedForArea:         q.PreparedForArea,
		PreparedForContactName:  q.PreparedForContactName,
		PreparedForContactEmail: q.PreparedForContactEmail,
		PreparedByName:          q.PreparedByName,
		PreparedByEmail:         q.PreparedByEmail,
		PreparedByRole:          q.PreparedByRole,
		Justification:           q.Justification,
		Terms:                   q.Terms,
		UpdatedAt:               q.UpdatedAt,
	}
	if q.IssueDate.Valid {
		t := q.IssueDate.Time
		rec.IssueDate = &t
	}
	if q.ValidUntil.Valid {
		t := q.ValidUntil.Time
		rec.ValidUntil = &t
	}
	for _, it := range items {
		rec.Items = append(rec.Items, knowledgebase.QuoteItemView{
			SKU:         it.SKU,
			Description: it.Description,
			Qty:         it.Qty,
			UnitPrice:   it.UnitPrice,
			Subtotal:    it.Subtotal,
		})
	}
	for _, sec := range sections {
		rec.Sections = append(rec.Sections, knowledgebase.QuoteSectionView{
			Key:       sec.Key,
			Title:     sec.Title,
			ContentMD: sec.ContentMD,
			SortOrder: sec.SortOrder,
		})
	}
	// Resolver project slug vía leads → clients → projects (best-effort).
	rec.ProjectSlug = a.resolveProjectSlug(ctx, q.LeadID)
	return rec, nil
}

func (a *quotesAdapter) resolveProjectSlug(ctx context.Context, leadID string) string {
	if a.db == nil || strings.TrimSpace(leadID) == "" {
		return ""
	}
	// El esquema de wave 7 (aria_team_projects) puede o no estar presente.
	// Si la columna lead_id existe en aria_team_projects, intentamos
	// resolver el slug. Si la query falla, devolvemos "".
	var slug sql.NullString
	err := a.db.QueryRowContext(ctx, `
		SELECT COALESCE(slug,'') FROM aria_team_projects
		WHERE lead_id::text = $1 LIMIT 1
	`, leadID).Scan(&slug)
	if err != nil {
		return ""
	}
	if slug.Valid {
		return slug.String
	}
	return ""
}

// kbProjectReader implementa knowledgebase.ProjectReader sobre
// aria_team_projects. Wave 7 está construyendo internal/cloud/teamprojects/;
// hasta que mergeen, leemos el schema directo via SQL para no acoplarnos.
type kbProjectReader struct {
	db *sql.DB
}

func (a *kbProjectReader) GetProject(ctx context.Context, id string) (*knowledgebase.ProjectInfo, error) {
	if a == nil || a.db == nil {
		return nil, fmt.Errorf("project reader not configured")
	}
	row := a.db.QueryRowContext(ctx, `
		SELECT id::text, COALESCE(slug,''), COALESCE(name,''), COALESCE(description,''),
		       COALESCE(status,''), started_at, delivered_at, COALESCE(code_repo_url,'')
		FROM aria_team_projects WHERE id::text = $1
	`, id)
	var p knowledgebase.ProjectInfo
	var startedAt, deliveredAt sql.NullTime
	if err := row.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Status,
		&startedAt, &deliveredAt, &p.CodeRepoURL); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, knowledgebase.ErrEntityNotFound
		}
		return nil, err
	}
	if startedAt.Valid {
		t := startedAt.Time
		p.StartedAt = &t
	}
	if deliveredAt.Valid {
		t := deliveredAt.Time
		p.DeliveredAt = &t
	}
	p.Members = a.loadMembers(ctx, p.ID)
	return &p, nil
}

func (a *kbProjectReader) loadMembers(ctx context.Context, projectID string) []knowledgebase.ProjectMember {
	rows, err := a.db.QueryContext(ctx, `
		SELECT m.user_uid::text, COALESCE(u.name,''), COALESCE(m.role,'')
		FROM aria_team_project_members m
		LEFT JOIN cloud_users u ON u.uid::text = m.user_uid::text
		WHERE m.project_id::text = $1
		ORDER BY m.role, u.name
	`, projectID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []knowledgebase.ProjectMember{}
	for rows.Next() {
		var m knowledgebase.ProjectMember
		if err := rows.Scan(&m.UID, &m.Name, &m.Role); err != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

func (a *kbProjectReader) ListProjects(ctx context.Context) ([]knowledgebase.ProjectInfo, error) {
	if a == nil || a.db == nil {
		return nil, nil
	}
	rows, err := a.db.QueryContext(ctx, `
		SELECT id::text, COALESCE(slug,''), COALESCE(name,''), COALESCE(description,''),
		       COALESCE(status,''), started_at, delivered_at, COALESCE(code_repo_url,'')
		FROM aria_team_projects
		ORDER BY name
	`)
	if err != nil {
		return nil, nil
	}
	defer rows.Close()
	out := []knowledgebase.ProjectInfo{}
	for rows.Next() {
		var p knowledgebase.ProjectInfo
		var startedAt, deliveredAt sql.NullTime
		if err := rows.Scan(&p.ID, &p.Slug, &p.Name, &p.Description, &p.Status,
			&startedAt, &deliveredAt, &p.CodeRepoURL); err != nil {
			continue
		}
		if startedAt.Valid {
			t := startedAt.Time
			p.StartedAt = &t
		}
		if deliveredAt.Valid {
			t := deliveredAt.Time
			p.DeliveredAt = &t
		}
		out = append(out, p)
	}
	return out, nil
}

func (a *kbProjectReader) ResolveProjectIDBySlug(ctx context.Context, slug string) (string, error) {
	if a == nil || a.db == nil {
		return "", nil
	}
	var id string
	err := a.db.QueryRowContext(ctx,
		`SELECT id::text FROM aria_team_projects WHERE slug = $1 LIMIT 1`, slug,
	).Scan(&id)
	if err != nil {
		return "", nil
	}
	return id, nil
}

// kbDashboardAdapter implementa dashboard.KnowledgeBaseDashboardService
// sobre knowledgebase.Service.
type kbDashboardAdapter struct {
	svc knowledgebase.Service
	org string
	repo string
}

func (a *kbDashboardAdapter) Available() bool { return a != nil && a.svc != nil && a.svc.Available() }

func (a *kbDashboardAdapter) Status(ctx context.Context) (dashboard.KBStatusView, error) {
	stats, err := a.svc.Status(ctx)
	if err != nil {
		return dashboard.KBStatusView{}, err
	}
	return dashboard.KBStatusView{
		Total:   stats.Total,
		OK:      stats.ByState[knowledgebase.SyncStatusOK],
		Pending: stats.ByState[knowledgebase.SyncStatusPending],
		Failed:  stats.ByState[knowledgebase.SyncStatusFailed],
		Skipped: stats.ByState[knowledgebase.SyncStatusSkipped],
	}, nil
}

func (a *kbDashboardAdapter) List(ctx context.Context, f dashboard.KBListFilterView) ([]dashboard.KBSyncedEntityView, error) {
	rows, err := a.svc.ListSyncedEntities(ctx, knowledgebase.ListFilter{
		EntityType: f.EntityType,
		ProjectID:  f.ProjectID,
		Status:     f.Status,
		Limit:      f.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.KBSyncedEntityView, 0, len(rows))
	for _, e := range rows {
		ts, _ := time.Parse(time.RFC3339, e.LastSyncedAt)
		ghURL := ""
		if e.RepoPath != "" {
			ghURL = fmt.Sprintf("https://github.com/%s/%s/blob/main/%s", a.org, a.repo, e.RepoPath)
		}
		out = append(out, dashboard.KBSyncedEntityView{
			ID:            e.ID,
			EntityType:    e.EntityType,
			EntityID:      e.EntityID,
			ProjectID:     e.ProjectID,
			RepoPath:      e.RepoPath,
			LastCommitSHA: e.LastCommitSHA,
			LastSyncedAt:  ts,
			SyncStatus:    e.SyncStatus,
			LastError:     e.LastError,
			GitHubURL:     ghURL,
		})
	}
	return out, nil
}

func (a *kbDashboardAdapter) ResyncFailed(ctx context.Context) (int, error) {
	return a.svc.ResyncFailed(ctx)
}

func (a *kbDashboardAdapter) ResyncProject(ctx context.Context, projectID string) (int, error) {
	return a.svc.ResyncProject(ctx, projectID)
}

func (a *kbDashboardAdapter) SyncQuote(ctx context.Context, quoteID string) (string, string, error) {
	return a.svc.SyncCotizacion(ctx, quoteID)
}

func (a *kbDashboardAdapter) SyncPRD(ctx context.Context, pageID string) (string, string, error) {
	return a.svc.SyncPRD(ctx, pageID)
}

func (a *kbDashboardAdapter) RefreshIndex(ctx context.Context) error {
	return a.svc.RefreshIndex(ctx)
}

func (a *kbDashboardAdapter) GenerateQuoteDOCX(ctx context.Context, quoteID string) ([]byte, error) {
	return a.svc.GenerateQuoteDOCX(ctx, quoteID)
}

// newKnowledgeBaseRuntime arma el knowledgebase.Service + adapter dashboard.
// Si las env vars de GitHub no están seteadas, retorna un service en modo
// degraded (Available()==false) — los endpoints devolverán 503 hasta que
// wave 7 mergee el GitHub client.
func newKnowledgeBaseRuntime(cs *cloudstore.CloudStore, cotizadorStore *cotizador.Store, pagesStore pages.Store, publicURL string, ghClient *github.Client) (knowledgebase.Service, dashboard.KnowledgeBaseDashboardService) {
	org := strings.TrimSpace(os.Getenv("ARIA_CORE_GITHUB_ORG"))
	if org == "" {
		org = "ITECHDEV-MX"
	}
	repo := strings.TrimSpace(os.Getenv("ARIA_CORE_KB_REPO"))
	if repo == "" {
		repo = "team-knowledge-base"
	}

	// Pandoc reference doc — bootstrap path en /var/lib/aria-core/templates
	// si no hay override. El converter genera default si no existe el archivo.
	refDocOverride := strings.TrimSpace(os.Getenv("ARIA_CORE_KB_REFERENCE_DOCX"))
	bootstrapPath := strings.TrimSpace(os.Getenv("ARIA_CORE_KB_REFERENCE_BOOTSTRAP"))
	if bootstrapPath == "" {
		bootstrapPath = filepath.Join(os.TempDir(), "aria-core-itechdev-reference.docx")
	}
	docxConverter := knowledgebase.NewDOCXConverter(refDocOverride, bootstrapPath)
	if err := docxConverter.VerifyAvailable(context.Background()); err != nil {
		log.Printf("[aria-core-cloud] knowledgebase pandoc DOCX export DISABLED: %v", err)
	} else {
		log.Printf("[aria-core-cloud] knowledgebase pandoc DOCX export ready")
	}

	// GitHub client — adapter envuelve github.Client (wave 7) para implementar
	// knowledgebase.GitHubLike. Si ghClient es nil (vault degraded o token
	// faltante), el service arranca en modo degraded (Available()==false).
	var gh knowledgebase.GitHubLike
	if ghClient != nil {
		gh = newKBGitHubAdapter(ghClient)
	}

	svc := knowledgebase.NewService(knowledgebase.Config{
		GitHub:             gh,
		Pages:              &pagesAdapter{store: pagesStore},
		Quotes:             &quotesAdapter{store: cotizadorStore, db: cs.DB()},
		Projects:           &kbProjectReader{db: cs.DB()},
		DOCXConverter:      docxConverter,
		DB:                 cs.DB(),
		Org:                org,
		Repo:               repo,
		DashboardPublicURL: publicURL,
		Async:              true,
	})
	dashAdapter := &kbDashboardAdapter{svc: svc, org: org, repo: repo}
	return svc, dashAdapter
}

// strNull utility to convert sql.NullString to string.
func strNull(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}
