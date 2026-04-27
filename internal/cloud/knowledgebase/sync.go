package knowledgebase

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Service es el contrato público del knowledge-base sync.
type Service interface {
	EnsureCentralRepo(ctx context.Context) error
	SyncPRD(ctx context.Context, pageID string) (commitSHA, repoPath string, err error)
	SyncHistoria(ctx context.Context, pageID string) (commitSHA, repoPath string, err error)
	SyncCotizacion(ctx context.Context, quoteID string) (commitSHA, repoPath string, err error)
	SyncProjectReadme(ctx context.Context, projectID string) (commitSHA, repoPath string, err error)
	GenerateQuoteDOCX(ctx context.Context, quoteID string) ([]byte, error)
	GenerateQuoteMarkdown(ctx context.Context, quoteID string) (string, error)
	RefreshIndex(ctx context.Context) error
	Status(ctx context.Context) (SyncStats, error)
	ListSyncedEntities(ctx context.Context, filter ListFilter) ([]SyncedEntity, error)
	ResyncFailed(ctx context.Context) (int, error)
	ResyncProject(ctx context.Context, projectID string) (int, error)
	OnQuoteFinalized(ctx context.Context, sessionID, quoteID string) error
	Available() bool
}

// QuoteChatHook es el contrato que el orchestrator de cotizaciones (wave 6)
// invoca al cerrar una sesión. Service implementa este hook vía
// OnQuoteFinalized para disparar SyncCotizacion async.
type QuoteChatHook interface {
	OnFinalize(ctx context.Context, sessionID, quoteID string) error
}

// PageReader es la fuente de PRDs/historias. Mantiene el knowledge-base
// desacoplado del paquete pages — el adapter (cmd/aria-core) implementa
// esto contra pages.PgStore.
type PageReader interface {
	GetPage(ctx context.Context, pageID string) (*PageRecord, error)
}

// PageRecord es la vista mínima que el sync necesita.
type PageRecord struct {
	ID           string
	Title        string
	ContentMD    string
	Project      string // slug del proyecto
	TemplateKey  string // prd-v1 | historia-v1 | etc
	CreatedByUID string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Status       string
}

// QuoteReader es la fuente de cotizaciones. El adapter implementa contra
// cotizador.Store.
type QuoteReader interface {
	GetQuote(ctx context.Context, quoteID string) (*QuoteRecord, error)
}

// QuoteRecord captura el estado completo de una quote para renderearla.
type QuoteRecord struct {
	ID                      string
	LeadID                  string
	ProjectSlug             string // resuelto vía leads → clients → projects
	Folio                   string
	Status                  string
	Currency                string
	Subtotal                float64
	Taxes                   float64
	Total                   float64
	IssueDate               *time.Time
	ValidUntil              *time.Time
	ProductName             string
	ProductSubtitle         string
	ProposalType            string
	Tags                    []string
	PreparedForCompany      string
	PreparedForArea         string
	PreparedForContactName  string
	PreparedForContactEmail string
	PreparedByName          string
	PreparedByEmail         string
	PreparedByRole          string
	Justification           string
	Terms                   string
	Items                   []QuoteItemView
	Sections                []QuoteSectionView
	UpdatedAt               time.Time
}

// ProjectReader es la fuente de team_projects (wave 7). El adapter
// implementa contra teamprojects.Store cuando exista; el knowledgebase
// no toca esa package.
type ProjectReader interface {
	GetProject(ctx context.Context, projectID string) (*ProjectInfo, error)
	ListProjects(ctx context.Context) ([]ProjectInfo, error)
	// ResolveProjectIDBySlug retorna "" si no existe.
	ResolveProjectIDBySlug(ctx context.Context, slug string) (string, error)
}

// Config son las opciones para construir un Service.
type Config struct {
	GitHub             GitHubLike
	Pages              PageReader
	Quotes             QuoteReader
	Projects           ProjectReader
	DOCXConverter      *DOCXConverter
	DB                 *sql.DB
	Org                string // default: ITECHDEV-MX
	Repo               string // default: team-knowledge-base
	DashboardPublicURL string // ej. https://ariacore.itechdev.com.mx
	AuthorName         string // commit author
	AuthorEmail        string // commit email
	// Async — si true, los hooks (OnQuoteFinalized, post-update PRD) corren
	// SyncX en goroutine para no bloquear el caller.
	Async bool
}

// service es la implementación de Service.
type service struct {
	gh            GitHubLike
	pages         PageReader
	quotes        QuoteReader
	projects      ProjectReader
	docx          *DOCXConverter
	db            *sql.DB
	org           string
	repo          string
	defaultBranch string
	dashboardURL  string
	authorName    string
	authorEmail   string
	async         bool
	mu            sync.Mutex // serializa puts a la misma path
}

// NewService construye un Service. Si cfg.GitHub es nil, devuelve un Service
// que rechaza todas las operaciones con ErrGitHubNotConfigured (modo degradado
// para que el cloudserver arranque sin GitHub configurado).
func NewService(cfg Config) Service {
	org := strings.TrimSpace(cfg.Org)
	if org == "" {
		org = "ITECHDEV-MX"
	}
	repo := strings.TrimSpace(cfg.Repo)
	if repo == "" {
		repo = "team-knowledge-base"
	}
	authorName := strings.TrimSpace(cfg.AuthorName)
	if authorName == "" {
		authorName = "ARIA Core knowledge-base sync"
	}
	authorEmail := strings.TrimSpace(cfg.AuthorEmail)
	if authorEmail == "" {
		authorEmail = "knowledgebase@ariacore.itechdev.com.mx"
	}
	return &service{
		gh:            cfg.GitHub,
		pages:         cfg.Pages,
		quotes:        cfg.Quotes,
		projects:      cfg.Projects,
		docx:          cfg.DOCXConverter,
		db:            cfg.DB,
		org:           org,
		repo:          repo,
		defaultBranch: "main",
		dashboardURL:  strings.TrimRight(strings.TrimSpace(cfg.DashboardPublicURL), "/"),
		authorName:    authorName,
		authorEmail:   authorEmail,
		async:         cfg.Async,
	}
}

// Available reporta si el service tiene un GitHub client utilizable.
func (s *service) Available() bool {
	return s != nil && s.gh != nil
}

// ─── PRD / Historia ────────────────────────────────────────────────────────

// SyncPRD sincroniza un page con template_key=prd-v1 al repo central.
func (s *service) SyncPRD(ctx context.Context, pageID string) (string, string, error) {
	return s.syncPage(ctx, pageID, EntityTypePRD)
}

// SyncHistoria sincroniza un page con template_key=historia-v1 al repo central.
func (s *service) SyncHistoria(ctx context.Context, pageID string) (string, string, error) {
	return s.syncPage(ctx, pageID, EntityTypeHistoria)
}

func (s *service) syncPage(ctx context.Context, pageID, entityType string) (string, string, error) {
	if !s.Available() {
		return "", "", ErrGitHubNotConfigured
	}
	if s.pages == nil {
		return "", "", fmt.Errorf("knowledgebase: pages reader not configured")
	}
	page, err := s.pages.GetPage(ctx, pageID)
	if err != nil {
		return "", "", err
	}
	if page == nil {
		return "", "", ErrEntityNotFound
	}
	projectSlug := strings.TrimSpace(page.Project)
	if projectSlug == "" {
		// PRDs sin project no se sincronizan al repo central.
		_ = s.recordSyncSkipped(ctx, entityType, page.ID, "", "page has no project assignment")
		return "", "", fmt.Errorf("knowledgebase: page %s has no project assignment", pageID)
	}

	folder := "prds"
	if entityType == EntityTypeHistoria {
		folder = "historias"
	}
	// Sequence: count + 1 dentro del project.
	seq, err := s.nextSequenceForProject(ctx, projectSlug, entityType, page.ID)
	if err != nil {
		return "", "", err
	}
	titleSlug := slugify(page.Title)
	if titleSlug == "" {
		titleSlug = "sin-titulo"
	}
	repoPath := fmt.Sprintf("proyectos/%s/%s/%05d-%s.md", projectSlug, folder, seq, titleSlug)

	fm := PRDFrontmatter{
		ID:           page.ID,
		Title:        page.Title,
		Project:      projectSlug,
		Type:         entityType,
		Status:       page.Status,
		CreatedBy:    page.CreatedByUID,
		CreatedAt:    page.CreatedAt,
		LastUpdated:  page.UpdatedAt,
		DashboardURL: s.dashboardPageURL(page.ID),
	}
	body := RenderPageMarkdown(fm, page.ContentMD)
	hash := contentHash(body)

	// Skip si content hash no cambió.
	prev, _ := s.loadSync(ctx, entityType, page.ID)
	if prev != nil && prev.LastContentHash == hash && prev.SyncStatus == SyncStatusOK {
		return prev.LastCommitSHA, prev.RepoPath, nil
	}

	prefix := "prd"
	if entityType == EntityTypeHistoria {
		prefix = "historia"
	}
	msg := commitMessage(prefix, page.Title)
	prevBlobSHA := ""
	if prev != nil {
		prevBlobSHA = prev.LastCommitSHA // we treat it as opaque
	}
	res, putErr := s.putFile(ctx, repoPath, []byte(body), prevBlobSHA, msg)
	if putErr != nil {
		_ = s.recordSyncFailed(ctx, entityType, page.ID, projectSlug, repoPath, putErr.Error())
		return "", "", putErr
	}
	if err := s.recordSyncOK(ctx, entityType, page.ID, projectSlug, repoPath, res.CommitSHA, hash); err != nil {
		return "", "", err
	}
	return res.CommitSHA, repoPath, nil
}

// ─── Cotizaciones ──────────────────────────────────────────────────────────

// SyncCotizacion exporta una quote a markdown + DOCX + metadata.json y la
// commitea al repo central bajo proyectos/<slug>/cotizaciones/<folio>/.
func (s *service) SyncCotizacion(ctx context.Context, quoteID string) (string, string, error) {
	if !s.Available() {
		return "", "", ErrGitHubNotConfigured
	}
	if s.quotes == nil {
		return "", "", fmt.Errorf("knowledgebase: quotes reader not configured")
	}
	q, err := s.quotes.GetQuote(ctx, quoteID)
	if err != nil {
		return "", "", err
	}
	if q == nil {
		return "", "", ErrEntityNotFound
	}
	projectSlug := strings.TrimSpace(q.ProjectSlug)
	if projectSlug == "" {
		projectSlug = "sin-proyecto"
	}
	folio := strings.TrimSpace(q.Folio)
	if folio == "" {
		// Si no hay folio, usar el primer trozo del UUID como fallback estable.
		folio = "QUOTE-" + shortID(q.ID)
	}

	dirPath := fmt.Sprintf("proyectos/%s/cotizaciones/%s", projectSlug, folio)
	mdPath := dirPath + "/propuesta.md"
	docxPath := dirPath + "/propuesta.docx"
	metaPath := dirPath + "/metadata.json"

	mdBody := RenderQuoteMarkdown(s.quoteRenderInput(q))
	hash := contentHash(mdBody)

	prev, _ := s.loadSync(ctx, EntityTypeCotizacion, q.ID)
	if prev != nil && prev.LastContentHash == hash && prev.SyncStatus == SyncStatusOK {
		return prev.LastCommitSHA, prev.RepoPath, nil
	}

	commitMsg := commitMessage("cotizacion", folio)

	// 1. Markdown.
	resMD, err := s.putFile(ctx, mdPath, []byte(mdBody), "", commitMsg)
	if err != nil {
		_ = s.recordSyncFailed(ctx, EntityTypeCotizacion, q.ID, projectSlug, dirPath, err.Error())
		return "", "", err
	}

	// 2. Metadata.
	meta := QuoteMetadata{
		QuoteID:         q.ID,
		Folio:           q.Folio,
		Status:          q.Status,
		Currency:        q.Currency,
		Subtotal:        q.Subtotal,
		Taxes:           q.Taxes,
		Total:           q.Total,
		PreparedFor:     q.PreparedForCompany,
		PreparedForArea: q.PreparedForArea,
		PreparedBy:      q.PreparedByName,
		ProductName:     q.ProductName,
		ProposalType:    q.ProposalType,
		Tags:            q.Tags,
		DashboardURL:    s.dashboardQuoteURL(q.ID),
		GeneratedAt:     time.Now().UTC(),
	}
	if q.IssueDate != nil {
		meta.IssueDate = q.IssueDate.Format("2006-01-02")
	}
	if q.ValidUntil != nil {
		meta.ValidUntil = q.ValidUntil.Format("2006-01-02")
	}
	metaBytes, _ := json.MarshalIndent(meta, "", "  ")
	metaBytes = append(metaBytes, '\n')
	if _, err := s.putFile(ctx, metaPath, metaBytes, "", commitMsg); err != nil {
		_ = s.recordSyncFailed(ctx, EntityTypeCotizacion, q.ID, projectSlug, dirPath, err.Error())
		return "", "", err
	}

	// 3. DOCX. Si pandoc no está, registramos failure parcial pero el .md
	//    + metadata ya quedan committed.
	if s.docx != nil {
		docxBytes, derr := s.docx.ConvertMarkdown(ctx, []byte(mdBody))
		if derr != nil {
			_ = s.recordSyncFailed(ctx, EntityTypeCotizacion, q.ID, projectSlug, dirPath,
				"DOCX conversion failed: "+derr.Error())
			return resMD.CommitSHA, dirPath, derr
		}
		if _, err := s.putFile(ctx, docxPath, docxBytes, "", commitMsg); err != nil {
			_ = s.recordSyncFailed(ctx, EntityTypeCotizacion, q.ID, projectSlug, dirPath, err.Error())
			return "", "", err
		}
	}

	if err := s.recordSyncOK(ctx, EntityTypeCotizacion, q.ID, projectSlug, dirPath, resMD.CommitSHA, hash); err != nil {
		return "", "", err
	}
	return resMD.CommitSHA, dirPath, nil
}

// GenerateQuoteMarkdown renderea el .md fuente de una quote SIN commitearla.
func (s *service) GenerateQuoteMarkdown(ctx context.Context, quoteID string) (string, error) {
	if s.quotes == nil {
		return "", fmt.Errorf("knowledgebase: quotes reader not configured")
	}
	q, err := s.quotes.GetQuote(ctx, quoteID)
	if err != nil {
		return "", err
	}
	if q == nil {
		return "", ErrEntityNotFound
	}
	return RenderQuoteMarkdown(s.quoteRenderInput(q)), nil
}

// quoteRenderInput convierte un QuoteRecord al QuoteRenderInput de templates.
func (s *service) quoteRenderInput(q *QuoteRecord) QuoteRenderInput {
	return QuoteRenderInput{
		Folio:                   q.Folio,
		ProductName:             q.ProductName,
		ProductSubtitle:         q.ProductSubtitle,
		ProposalType:            q.ProposalType,
		PreparedForCompany:      q.PreparedForCompany,
		PreparedForArea:         q.PreparedForArea,
		PreparedForContactName:  q.PreparedForContactName,
		PreparedForContactEmail: q.PreparedForContactEmail,
		PreparedByName:          q.PreparedByName,
		PreparedByEmail:         q.PreparedByEmail,
		PreparedByRole:          q.PreparedByRole,
		IssueDate:               q.IssueDate,
		ValidUntil:              q.ValidUntil,
		Currency:                q.Currency,
		Subtotal:                q.Subtotal,
		Taxes:                   q.Taxes,
		Total:                   q.Total,
		Status:                  q.Status,
		Justification:           q.Justification,
		Terms:                   q.Terms,
		Items:                   q.Items,
		Sections:                q.Sections,
		Tags:                    q.Tags,
		DashboardURL:            s.dashboardQuoteURL(q.ID),
	}
}

// GenerateQuoteDOCX produce los bytes DOCX de una cotización SIN commitearla
// al repo (usado por endpoints HTTP /export/docx + MCP tool aria_quote_export_docx).
func (s *service) GenerateQuoteDOCX(ctx context.Context, quoteID string) ([]byte, error) {
	if s.quotes == nil {
		return nil, fmt.Errorf("knowledgebase: quotes reader not configured")
	}
	if s.docx == nil {
		return nil, ErrPandocNotAvailable
	}
	q, err := s.quotes.GetQuote(ctx, quoteID)
	if err != nil {
		return nil, err
	}
	if q == nil {
		return nil, ErrEntityNotFound
	}
	md := RenderQuoteMarkdown(s.quoteRenderInput(q))
	return s.docx.ConvertMarkdown(ctx, []byte(md))
}

// ─── Project README + index ────────────────────────────────────────────────

// SyncProjectReadme actualiza proyectos/<slug>/README.md y refresca el índice
// raíz.
func (s *service) SyncProjectReadme(ctx context.Context, projectID string) (string, string, error) {
	if !s.Available() {
		return "", "", ErrGitHubNotConfigured
	}
	if s.projects == nil {
		return "", "", fmt.Errorf("knowledgebase: project reader not configured")
	}
	p, err := s.projects.GetProject(ctx, projectID)
	if err != nil {
		return "", "", err
	}
	if p == nil {
		return "", "", ErrEntityNotFound
	}
	slug := strings.TrimSpace(p.Slug)
	if slug == "" {
		slug = slugify(p.Name)
	}
	if slug == "" {
		return "", "", fmt.Errorf("knowledgebase: project %s has no slug or name", projectID)
	}
	body := RenderProjectReadme(*p)
	hash := contentHash(body)
	repoPath := fmt.Sprintf("proyectos/%s/README.md", slug)

	prev, _ := s.loadSync(ctx, EntityTypeProjectReadme, p.ID)
	if prev != nil && prev.LastContentHash == hash && prev.SyncStatus == SyncStatusOK {
		// Aún así refrescamos el índice por si cambiaron otros proyectos.
		_ = s.RefreshIndex(ctx)
		return prev.LastCommitSHA, prev.RepoPath, nil
	}
	res, err := s.putFile(ctx, repoPath, []byte(body), "", commitMessage("project", slug+" README"))
	if err != nil {
		_ = s.recordSyncFailed(ctx, EntityTypeProjectReadme, p.ID, slug, repoPath, err.Error())
		return "", "", err
	}
	if err := s.recordSyncOK(ctx, EntityTypeProjectReadme, p.ID, slug, repoPath, res.CommitSHA, hash); err != nil {
		return "", "", err
	}
	// Refresh root index — best effort, no bloquea el commit principal.
	if err := s.RefreshIndex(ctx); err != nil {
		// Solo log via field — el sync de project README ya fue exitoso.
		_ = s.recordSyncFailed(ctx, EntityTypeRootIndex, "", "", "README.md", err.Error())
	}
	return res.CommitSHA, repoPath, nil
}

// RefreshIndex regenera el README.md raíz listando todos los proyectos
// sincronizados.
func (s *service) RefreshIndex(ctx context.Context) error {
	if !s.Available() {
		return ErrGitHubNotConfigured
	}
	var entries []IndexEntry
	if s.projects != nil {
		all, err := s.projects.ListProjects(ctx)
		if err == nil {
			for _, p := range all {
				if strings.TrimSpace(p.Slug) == "" {
					continue
				}
				updated := time.Time{}
				if p.DeliveredAt != nil {
					updated = *p.DeliveredAt
				} else if p.StartedAt != nil {
					updated = *p.StartedAt
				}
				entries = append(entries, IndexEntry{
					Slug:         p.Slug,
					Name:         p.Name,
					Description:  p.Description,
					Status:       p.Status,
					DashboardURL: p.DashboardURL,
					UpdatedAt:    updated,
				})
			}
		}
	}
	body := RenderRootIndex(entries)
	hash := contentHash(body)
	prev, _ := s.loadSync(ctx, EntityTypeRootIndex, "")
	if prev != nil && prev.LastContentHash == hash && prev.SyncStatus == SyncStatusOK {
		return nil
	}
	res, err := s.putFile(ctx, "README.md", []byte(body), "", commitMessage("docs", "refresh root index"))
	if err != nil {
		_ = s.recordSyncFailed(ctx, EntityTypeRootIndex, "", "", "README.md", err.Error())
		return err
	}
	return s.recordSyncOK(ctx, EntityTypeRootIndex, "", "", "README.md", res.CommitSHA, hash)
}

// ─── Hook + admin API ──────────────────────────────────────────────────────

// OnQuoteFinalized se invoca desde el orchestrator de chat cuando una
// session.status pasa a 'finalized'. Si async=true, lanza goroutine.
func (s *service) OnQuoteFinalized(ctx context.Context, sessionID, quoteID string) error {
	if strings.TrimSpace(quoteID) == "" {
		return fmt.Errorf("knowledgebase: OnQuoteFinalized requires quote_id")
	}
	if !s.Available() {
		return ErrGitHubNotConfigured
	}
	if s.async {
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			_, _, _ = s.SyncCotizacion(bg, quoteID)
		}()
		return nil
	}
	_, _, err := s.SyncCotizacion(ctx, quoteID)
	return err
}

// OnFinalize implementa QuoteChatHook (alias para compatibilidad de naming).
func (s *service) OnFinalize(ctx context.Context, sessionID, quoteID string) error {
	return s.OnQuoteFinalized(ctx, sessionID, quoteID)
}

// ListFilter agrupa los filtros del listado de aria_kb_synced_entities.
type ListFilter struct {
	EntityType string // prd|historia|cotizacion|...
	ProjectID  string
	Status     string
	Limit      int
}

// ListSyncedEntities expone las filas del tracking para el dashboard.
func (s *service) ListSyncedEntities(ctx context.Context, filter ListFilter) ([]SyncedEntity, error) {
	if s.db == nil {
		return nil, fmt.Errorf("knowledgebase: db not configured")
	}
	conds := []string{"1=1"}
	args := []any{}
	idx := 1
	if v := strings.TrimSpace(filter.EntityType); v != "" {
		conds = append(conds, fmt.Sprintf("entity_type = $%d", idx))
		args = append(args, v)
		idx++
	}
	if v := strings.TrimSpace(filter.ProjectID); v != "" {
		conds = append(conds, fmt.Sprintf("project_id::text = $%d", idx))
		args = append(args, v)
		idx++
	}
	if v := strings.TrimSpace(filter.Status); v != "" {
		conds = append(conds, fmt.Sprintf("sync_status = $%d", idx))
		args = append(args, v)
		idx++
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	args = append(args, limit)
	q := fmt.Sprintf(`
		SELECT id::text, entity_type, COALESCE(entity_id::text,''), COALESCE(project_id::text,''),
		       repo_path, COALESCE(last_commit_sha,''), COALESCE(last_content_hash,''),
		       last_synced_at, sync_status, COALESCE(last_error,'')
		FROM aria_kb_synced_entities
		WHERE %s
		ORDER BY last_synced_at DESC
		LIMIT $%d`, strings.Join(conds, " AND "), idx)
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SyncedEntity
	for rows.Next() {
		var e SyncedEntity
		var ts time.Time
		if err := rows.Scan(&e.ID, &e.EntityType, &e.EntityID, &e.ProjectID,
			&e.RepoPath, &e.LastCommitSHA, &e.LastContentHash,
			&ts, &e.SyncStatus, &e.LastError); err != nil {
			return nil, err
		}
		e.LastSyncedAt = ts.UTC().Format(time.RFC3339)
		out = append(out, e)
	}
	return out, rows.Err()
}

// Status retorna counts globales agrupados por sync_status.
func (s *service) Status(ctx context.Context) (SyncStats, error) {
	stats := SyncStats{ByState: map[string]int{}}
	if s.db == nil {
		return stats, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT sync_status, COUNT(*) FROM aria_kb_synced_entities GROUP BY sync_status`)
	if err != nil {
		return stats, err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return stats, err
		}
		stats.ByState[st] = n
		stats.Total += n
	}
	return stats, rows.Err()
}

// ResyncFailed re-intenta cada entidad con sync_status='failed'. Retorna
// cuántas se reintentaron exitosamente.
func (s *service) ResyncFailed(ctx context.Context) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("knowledgebase: db not configured")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entity_type, COALESCE(entity_id::text,'') FROM aria_kb_synced_entities WHERE sync_status='failed'`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type pair struct{ kind, id string }
	var todo []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.kind, &p.id); err != nil {
			return 0, err
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	ok := 0
	for _, p := range todo {
		if err := s.resyncByKind(ctx, p.kind, p.id); err == nil {
			ok++
		}
	}
	return ok, nil
}

// ResyncProject re-sincea TODO lo asociado a un project_id (PRDs +
// historias + cotizaciones del project + project README + root index).
func (s *service) ResyncProject(ctx context.Context, projectID string) (int, error) {
	if s.db == nil {
		return 0, fmt.Errorf("knowledgebase: db not configured")
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT entity_type, COALESCE(entity_id::text,'') FROM aria_kb_synced_entities WHERE project_id::text=$1`,
		projectID,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type pair struct{ kind, id string }
	var todo []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.kind, &p.id); err != nil {
			return 0, err
		}
		todo = append(todo, p)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	ok := 0
	for _, p := range todo {
		if err := s.resyncByKind(ctx, p.kind, p.id); err == nil {
			ok++
		}
	}
	if _, _, err := s.SyncProjectReadme(ctx, projectID); err == nil {
		ok++
	}
	return ok, nil
}

func (s *service) resyncByKind(ctx context.Context, kind, id string) error {
	switch kind {
	case EntityTypePRD:
		_, _, err := s.SyncPRD(ctx, id)
		return err
	case EntityTypeHistoria:
		_, _, err := s.SyncHistoria(ctx, id)
		return err
	case EntityTypeCotizacion:
		_, _, err := s.SyncCotizacion(ctx, id)
		return err
	case EntityTypeProjectReadme:
		_, _, err := s.SyncProjectReadme(ctx, id)
		return err
	case EntityTypeRootIndex:
		return s.RefreshIndex(ctx)
	}
	return fmt.Errorf("knowledgebase: unknown entity_type %q", kind)
}

// ─── Internal: sync DB tracking + GitHub put ───────────────────────────────

func (s *service) putFile(ctx context.Context, path string, body []byte, prevSHA, message string) (*GitHubPutFileResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	req := GitHubPutFileRequest{
		Owner:         s.org,
		Repo:          s.repo,
		Branch:        s.defaultBranch,
		Path:          path,
		Content:       body,
		CommitMessage: message,
		PreviousSHA:   prevSHA,
		AuthorName:    s.authorName,
		AuthorEmail:   s.authorEmail,
	}
	res, err := s.gh.PutFile(ctx, req)
	if err != nil {
		return nil, err
	}
	return res, nil
}

func (s *service) loadSync(ctx context.Context, entityType, entityID string) (*SyncedEntity, error) {
	if s.db == nil {
		return nil, sql.ErrNoRows
	}
	q := `
		SELECT id::text, entity_type, COALESCE(entity_id::text,''), COALESCE(project_id::text,''),
		       repo_path, COALESCE(last_commit_sha,''), COALESCE(last_content_hash,''),
		       last_synced_at, sync_status, COALESCE(last_error,'')
		FROM aria_kb_synced_entities
		WHERE entity_type = $1
		  AND COALESCE(entity_id::text, '___index___') = COALESCE(NULLIF($2,'')::text, '___index___')
		LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, entityType, entityID)
	var e SyncedEntity
	var ts time.Time
	if err := row.Scan(&e.ID, &e.EntityType, &e.EntityID, &e.ProjectID,
		&e.RepoPath, &e.LastCommitSHA, &e.LastContentHash,
		&ts, &e.SyncStatus, &e.LastError); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	e.LastSyncedAt = ts.UTC().Format(time.RFC3339)
	return &e, nil
}

func (s *service) recordSyncOK(ctx context.Context, entityType, entityID, projectIDOrSlug, repoPath, commitSHA, hash string) error {
	return s.upsertSync(ctx, entityType, entityID, projectIDOrSlug, repoPath, commitSHA, hash, SyncStatusOK, "")
}

func (s *service) recordSyncFailed(ctx context.Context, entityType, entityID, projectIDOrSlug, repoPath, errMsg string) error {
	return s.upsertSync(ctx, entityType, entityID, projectIDOrSlug, repoPath, "", "", SyncStatusFailed, errMsg)
}

func (s *service) recordSyncSkipped(ctx context.Context, entityType, entityID, projectIDOrSlug, reason string) error {
	return s.upsertSync(ctx, entityType, entityID, projectIDOrSlug, "", "", "", SyncStatusSkipped, reason)
}

// upsertSync inserta o actualiza la fila de tracking. project_id puede venir
// como slug en algunos paths (entity sin UUID resoluble); en ese caso lo
// dejamos en NULL y guardamos el slug en repo_path para visibilidad.
func (s *service) upsertSync(ctx context.Context, entityType, entityID, projectRef, repoPath, commitSHA, hash, status, errMsg string) error {
	if s.db == nil {
		return nil
	}
	var entityIDArg any
	if strings.TrimSpace(entityID) != "" && isUUIDLike(entityID) {
		entityIDArg = entityID
	}
	var projectIDArg any
	if strings.TrimSpace(projectRef) != "" && isUUIDLike(projectRef) {
		projectIDArg = projectRef
	}
	const q = `
		INSERT INTO aria_kb_synced_entities (
			entity_type, entity_id, project_id, repo_path,
			last_commit_sha, last_content_hash, sync_status, last_error
		) VALUES ($1, $2::uuid, $3::uuid, $4, NULLIF($5,''), NULLIF($6,''), $7, NULLIF($8,''))
		ON CONFLICT (entity_type, COALESCE(entity_id::text, '___index___'))
		DO UPDATE SET
			project_id = COALESCE(EXCLUDED.project_id, aria_kb_synced_entities.project_id),
			repo_path = EXCLUDED.repo_path,
			last_commit_sha = COALESCE(EXCLUDED.last_commit_sha, aria_kb_synced_entities.last_commit_sha),
			last_content_hash = COALESCE(EXCLUDED.last_content_hash, aria_kb_synced_entities.last_content_hash),
			last_synced_at = NOW(),
			sync_status = EXCLUDED.sync_status,
			last_error = EXCLUDED.last_error`
	_, err := s.db.ExecContext(ctx, q,
		entityType, entityIDArg, projectIDArg, repoPath,
		commitSHA, hash, status, errMsg,
	)
	return err
}

// nextSequenceForProject calcula el próximo NNNNN usado en file names. Si la
// página ya fue sincronizada antes, reutilizamos su sequence (extraído del
// repo_path). Si no, contamos cuántas filas existen para (project, type) +1.
func (s *service) nextSequenceForProject(ctx context.Context, projectSlug, entityType, pageID string) (int, error) {
	if s.db == nil {
		return 1, nil
	}
	// 1. Si ya sincronizado, extraer sequence del repo_path.
	var existingPath string
	err := s.db.QueryRowContext(ctx,
		`SELECT repo_path FROM aria_kb_synced_entities WHERE entity_type=$1 AND entity_id::text=$2 LIMIT 1`,
		entityType, pageID,
	).Scan(&existingPath)
	if err == nil && existingPath != "" {
		if n := extractSequence(existingPath); n > 0 {
			return n, nil
		}
	}
	// 2. Contar cuántos del mismo (project, type) ya están sincronizados.
	folder := "prds"
	if entityType == EntityTypeHistoria {
		folder = "historias"
	}
	prefix := fmt.Sprintf("proyectos/%s/%s/", projectSlug, folder)
	var n int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM aria_kb_synced_entities
		 WHERE entity_type=$1 AND repo_path LIKE $2`,
		entityType, prefix+"%",
	).Scan(&n); err != nil {
		return 1, nil
	}
	return n + 1, nil
}

// dashboardPageURL construye el deep-link al editor de page del dashboard.
func (s *service) dashboardPageURL(pageID string) string {
	if s.dashboardURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/dashboard/pages/editor?id=%s", s.dashboardURL, pageID)
}

// dashboardQuoteURL construye el deep-link a la quote del dashboard.
func (s *service) dashboardQuoteURL(quoteID string) string {
	if s.dashboardURL == "" {
		return ""
	}
	return fmt.Sprintf("%s/dashboard/cotizador/quotes/%s", s.dashboardURL, quoteID)
}

// ─── Helpers ───────────────────────────────────────────────────────────────

func contentHash(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:])
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	repl := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n", "ü", "u",
		"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ñ", "n",
	)
	s = repl.Replace(s)
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}

var uuidRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

func isUUIDLike(s string) bool {
	return uuidRE.MatchString(strings.TrimSpace(s))
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 8 {
		return strings.ToUpper(id[:8])
	}
	return strings.ToUpper(id)
}

var seqRE = regexp.MustCompile(`/(\d{5})-`)

// extractSequence devuelve el NNNNN encontrado en el repo_path o 0.
func extractSequence(path string) int {
	m := seqRE.FindStringSubmatch(path)
	if len(m) < 2 {
		return 0
	}
	var n int
	fmt.Sscanf(m[1], "%d", &n)
	return n
}
