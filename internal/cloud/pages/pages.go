// Package pages implementa la fundación mini-Notion de ARIA Core.
//
// Reemplaza Notion+Confluence+Drive como fuente única de verdad del equipo iTechDev.
// Hoy ARIA es observation-centric (memorias atómicas); este paquete agrega la capa
// document-centric: páginas largas anidadas, editor markdown, templates, búsqueda
// cross-everything (Cmd+K).
//
// Diseño:
//   - aria_pages: tree padre-hijo con sort_order para drag-to-reorder.
//   - aria_page_revisions: cada Update inserta snapshot — historial completo.
//   - FTS spanish accent-insensitive (unaccent on-the-fly en queries).
//   - QuickSearchAll combina pages + obs + skills + recipes + leads + quotes
//     con errgroup paralelo, top-3 por fuente, scoring uniforme.
package pages

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Errores públicos.
var (
	ErrNotFound      = errors.New("pages: not found")
	ErrInvalidInput  = errors.New("pages: invalid input")
	ErrInvalidParent = errors.New("pages: invalid parent (cycle or missing)")
)

// Page es la representación pública de una página.
type Page struct {
	ID            string
	ParentID      string // "" si root
	Title         string
	ContentMD     string
	Icon          string
	Project       string
	Scope         string // personal|project|team|client_knowledge
	ClientID      string
	PageType      string // doc|template|database
	TemplateKey   string
	Sensitivity   string // public|internal|client|confidential
	SortOrder     int
	IsArchived    bool
	CreatedByUID  string
	CreatedAt     time.Time
	UpdatedByUID  string
	UpdatedAt     time.Time
	ChildrenCount int             // poblado en List/Tree
	Path          []PageBreadcrumb // poblado en Get
}

// PageBreadcrumb es un eslabón del path raíz→hoja.
type PageBreadcrumb struct {
	ID    string
	Title string
	Icon  string
}

// Revision representa una snapshot del contenido en aria_page_revisions.
type Revision struct {
	ID           string
	PageID       string
	Title        string
	ContentMD    string
	EditedByUID  string
	EditSummary  string
	CreatedAt    time.Time
}

// CreateParams es el input de Create.
type CreateParams struct {
	ParentID     string
	Title        string
	ContentMD    string
	Icon         string
	Project      string
	Scope        string
	ClientID     string
	PageType     string
	TemplateKey  string
	Sensitivity  string
	SortOrder    int
	CreatedByUID string
}

// UpdateParams es el input de Update. Cada save genera revision.
type UpdateParams struct {
	Title        *string // si nil, no toca
	ContentMD    *string
	Icon         *string
	Project      *string
	Scope        *string
	ClientID     *string
	Sensitivity  *string
	UpdatedByUID string
	EditSummary  string
}

// Store es la API pública del paquete.
type Store interface {
	Create(ctx context.Context, params CreateParams) (*Page, error)
	Update(ctx context.Context, id string, params UpdateParams) (*Page, error)
	Get(ctx context.Context, id string) (*Page, error)
	GetBySlug(ctx context.Context, slug string) (*Page, error)
	Tree(ctx context.Context, project, scope string) ([]*Page, error)
	Move(ctx context.Context, id, newParentID string, newSortOrder int) error
	Archive(ctx context.Context, id string) error
	Restore(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	ListRevisions(ctx context.Context, pageID string, limit int) ([]*Revision, error)
	RevertToRevision(ctx context.Context, pageID, revisionID, byUID string) error

	// Search / Cmd+K
	SearchPages(ctx context.Context, query, project string, limit int) ([]QuickHit, error)
	QuickSearchAll(ctx context.Context, query string, limit int) (*QuickSearchResult, error)
}

// PgStore es la implementación Postgres de Store.
type PgStore struct {
	db *sql.DB
}

// NewPgStore crea un store contra el *sql.DB dado.
func NewPgStore(db *sql.DB) *PgStore {
	return &PgStore{db: db}
}

// DB expone el handler para tests/diagnóstico.
func (s *PgStore) DB() *sql.DB { return s.db }

// ─── Create ─────────────────────────────────────────────────────────────────

func (s *PgStore) Create(ctx context.Context, p CreateParams) (*Page, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pages: store not initialized")
	}
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return nil, fmt.Errorf("%w: title required", ErrInvalidInput)
	}
	if p.CreatedByUID == "" {
		return nil, fmt.Errorf("%w: created_by_uid required", ErrInvalidInput)
	}
	scope := normalizeScope(p.Scope)
	sensitivity := normalizeSensitivity(p.Sensitivity)
	pageType := strings.TrimSpace(p.PageType)
	if pageType == "" {
		pageType = "doc"
	}

	id := uuid.New().String()
	parent := nullableUUID(p.ParentID)
	clientID := nullableUUID(p.ClientID)
	project := nullableString(p.Project)
	icon := nullableString(p.Icon)
	templateKey := nullableString(p.TemplateKey)

	const q = `
		INSERT INTO aria_pages (
			id, parent_id, title, content_md, icon, project, scope, client_id,
			page_type, template_key, sensitivity, sort_order,
			created_by_uid, updated_by_uid
		) VALUES (
			$1::uuid, $2, $3, $4, $5, $6, $7, $8,
			$9, $10, $11, $12,
			$13::uuid, $13::uuid
		)
		RETURNING id::text, COALESCE(parent_id::text,''), title, content_md,
		          COALESCE(icon,''), COALESCE(project,''), scope, COALESCE(client_id::text,''),
		          page_type, COALESCE(template_key,''), sensitivity, sort_order,
		          is_archived, created_by_uid::text, created_at,
		          COALESCE(updated_by_uid::text,''), updated_at`

	row := s.db.QueryRowContext(ctx, q,
		id, parent, title, p.ContentMD, icon, project, scope, clientID,
		pageType, templateKey, sensitivity, p.SortOrder,
		p.CreatedByUID,
	)
	page, err := scanPage(row)
	if err != nil {
		return nil, fmt.Errorf("pages: create: %w", err)
	}

	// Snapshot inicial en revisions: trazabilidad desde el save 1.
	if err := s.insertRevision(ctx, page.ID, page.Title, page.ContentMD, p.CreatedByUID, "initial"); err != nil {
		return nil, fmt.Errorf("pages: insert initial revision: %w", err)
	}
	return page, nil
}

// ─── Update ─────────────────────────────────────────────────────────────────

func (s *PgStore) Update(ctx context.Context, id string, p UpdateParams) (*Page, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pages: store not initialized")
	}
	if !isUUID(id) {
		return nil, fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	if p.UpdatedByUID == "" {
		return nil, fmt.Errorf("%w: updated_by_uid required", ErrInvalidInput)
	}

	// Build dynamic SET. Para no fragmentar, levantamos current y aplicamos diff.
	current, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}

	newTitle := current.Title
	newContent := current.ContentMD
	if p.Title != nil {
		t := strings.TrimSpace(*p.Title)
		if t == "" {
			return nil, fmt.Errorf("%w: title cannot be blank", ErrInvalidInput)
		}
		newTitle = t
	}
	if p.ContentMD != nil {
		newContent = *p.ContentMD
	}
	newIcon := nullableString(stringOr(p.Icon, current.Icon))
	newProject := nullableString(stringOr(p.Project, current.Project))
	newScope := normalizeScope(stringOr(p.Scope, current.Scope))
	newClient := nullableUUID(stringOr(p.ClientID, current.ClientID))
	newSensitivity := normalizeSensitivity(stringOr(p.Sensitivity, current.Sensitivity))

	const q = `
		UPDATE aria_pages SET
			title = $2,
			content_md = $3,
			icon = $4,
			project = $5,
			scope = $6,
			client_id = $7,
			sensitivity = $8,
			updated_by_uid = $9::uuid,
			updated_at = NOW()
		WHERE id = $1::uuid
		RETURNING id::text, COALESCE(parent_id::text,''), title, content_md,
		          COALESCE(icon,''), COALESCE(project,''), scope, COALESCE(client_id::text,''),
		          page_type, COALESCE(template_key,''), sensitivity, sort_order,
		          is_archived, created_by_uid::text, created_at,
		          COALESCE(updated_by_uid::text,''), updated_at`

	row := s.db.QueryRowContext(ctx, q,
		id, newTitle, newContent, newIcon, newProject, newScope, newClient, newSensitivity, p.UpdatedByUID,
	)
	page, err := scanPage(row)
	if err != nil {
		return nil, fmt.Errorf("pages: update: %w", err)
	}

	// Insert revision SOLO si title o content cambiaron.
	if newTitle != current.Title || newContent != current.ContentMD {
		summary := strings.TrimSpace(p.EditSummary)
		if err := s.insertRevision(ctx, id, newTitle, newContent, p.UpdatedByUID, summary); err != nil {
			return nil, fmt.Errorf("pages: insert revision: %w", err)
		}
	}
	return page, nil
}

// ─── Get / GetBySlug ────────────────────────────────────────────────────────

func (s *PgStore) Get(ctx context.Context, id string) (*Page, error) {
	if !isUUID(id) {
		return nil, fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	const q = `
		SELECT id::text, COALESCE(parent_id::text,''), title, content_md,
		       COALESCE(icon,''), COALESCE(project,''), scope, COALESCE(client_id::text,''),
		       page_type, COALESCE(template_key,''), sensitivity, sort_order,
		       is_archived, created_by_uid::text, created_at,
		       COALESCE(updated_by_uid::text,''), updated_at
		FROM aria_pages WHERE id = $1::uuid`
	row := s.db.QueryRowContext(ctx, q, id)
	page, err := scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pages: get: %w", err)
	}
	// Poblar Path.
	page.Path = s.buildBreadcrumbs(ctx, page)
	return page, nil
}

// GetBySlug acepta el formato sluggify(title)+last8 (ej. mi-pagina-a1b2c3d4).
// Si el suffix de 8 chars es una porción válida del UUID, hace match por LIKE.
func (s *PgStore) GetBySlug(ctx context.Context, slug string) (*Page, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, fmt.Errorf("%w: slug required", ErrInvalidInput)
	}
	// Si el slug es UUID completo, despachar a Get.
	if isUUID(slug) {
		return s.Get(ctx, slug)
	}
	parts := strings.Split(slug, "-")
	if len(parts) < 1 {
		return nil, ErrNotFound
	}
	suffix := parts[len(parts)-1]
	if len(suffix) != 8 {
		return nil, ErrNotFound
	}
	const q = `
		SELECT id::text, COALESCE(parent_id::text,''), title, content_md,
		       COALESCE(icon,''), COALESCE(project,''), scope, COALESCE(client_id::text,''),
		       page_type, COALESCE(template_key,''), sensitivity, sort_order,
		       is_archived, created_by_uid::text, created_at,
		       COALESCE(updated_by_uid::text,''), updated_at
		FROM aria_pages WHERE id::text LIKE '%' || $1 LIMIT 1`
	row := s.db.QueryRowContext(ctx, q, suffix)
	page, err := scanPage(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("pages: get by slug: %w", err)
	}
	page.Path = s.buildBreadcrumbs(ctx, page)
	return page, nil
}

// Slug calcula el slug canónico de una página (sluggify(title)+last8).
func Slug(p *Page) string {
	if p == nil {
		return ""
	}
	base := slugify(p.Title)
	if base == "" {
		base = "page"
	}
	if len(p.ID) >= 8 {
		return base + "-" + strings.ReplaceAll(p.ID, "-", "")[len(strings.ReplaceAll(p.ID, "-", ""))-8:]
	}
	return base
}

// ─── Tree ───────────────────────────────────────────────────────────────────

// Tree retorna todas las páginas no-archivadas filtradas por project/scope con
// ChildrenCount poblado. El consumidor (templ) se encarga del rendering recursivo.
func (s *PgStore) Tree(ctx context.Context, project, scope string) ([]*Page, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("pages: store not initialized")
	}
	conds := []string{"NOT is_archived"}
	args := []any{}
	idx := 1
	if p := strings.TrimSpace(project); p != "" {
		conds = append(conds, fmt.Sprintf("project = $%d", idx))
		args = append(args, p)
		idx++
	}
	if sc := strings.TrimSpace(scope); sc != "" {
		conds = append(conds, fmt.Sprintf("scope = $%d", idx))
		args = append(args, sc)
		idx++
	}
	q := fmt.Sprintf(`
		SELECT p.id::text, COALESCE(p.parent_id::text,''), p.title, p.content_md,
		       COALESCE(p.icon,''), COALESCE(p.project,''), p.scope, COALESCE(p.client_id::text,''),
		       p.page_type, COALESCE(p.template_key,''), p.sensitivity, p.sort_order,
		       p.is_archived, p.created_by_uid::text, p.created_at,
		       COALESCE(p.updated_by_uid::text,''), p.updated_at,
		       (SELECT COUNT(*) FROM aria_pages c WHERE c.parent_id = p.id AND NOT c.is_archived)
		FROM aria_pages p
		WHERE %s
		ORDER BY p.sort_order ASC, p.created_at ASC, p.title ASC`, strings.Join(conds, " AND "))
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("pages: tree: %w", err)
	}
	defer rows.Close()
	out := []*Page{}
	for rows.Next() {
		page := &Page{}
		var childCount int
		if err := rows.Scan(
			&page.ID, &page.ParentID, &page.Title, &page.ContentMD,
			&page.Icon, &page.Project, &page.Scope, &page.ClientID,
			&page.PageType, &page.TemplateKey, &page.Sensitivity, &page.SortOrder,
			&page.IsArchived, &page.CreatedByUID, &page.CreatedAt,
			&page.UpdatedByUID, &page.UpdatedAt,
			&childCount,
		); err != nil {
			return nil, fmt.Errorf("pages: tree scan: %w", err)
		}
		page.ChildrenCount = childCount
		out = append(out, page)
	}
	return out, rows.Err()
}

// ─── Move / Archive / Restore / Delete ─────────────────────────────────────

func (s *PgStore) Move(ctx context.Context, id, newParentID string, newSortOrder int) error {
	if !isUUID(id) {
		return fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	// Prevenir ciclos: si newParentID == id o es descendiente de id, rechazar.
	if newParentID != "" {
		if newParentID == id {
			return ErrInvalidParent
		}
		ok, err := s.isDescendant(ctx, newParentID, id)
		if err != nil {
			return fmt.Errorf("pages: move check cycle: %w", err)
		}
		if ok {
			return ErrInvalidParent
		}
	}
	const q = `UPDATE aria_pages SET parent_id = $2, sort_order = $3, updated_at = NOW() WHERE id = $1::uuid`
	parent := nullableUUID(newParentID)
	res, err := s.db.ExecContext(ctx, q, id, parent, newSortOrder)
	if err != nil {
		return fmt.Errorf("pages: move: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) Archive(ctx context.Context, id string) error {
	return s.setArchived(ctx, id, true)
}

func (s *PgStore) Restore(ctx context.Context, id string) error {
	return s.setArchived(ctx, id, false)
}

func (s *PgStore) setArchived(ctx context.Context, id string, archived bool) error {
	if !isUUID(id) {
		return fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	const q = `UPDATE aria_pages SET is_archived = $2, updated_at = NOW() WHERE id = $1::uuid`
	res, err := s.db.ExecContext(ctx, q, id, archived)
	if err != nil {
		return fmt.Errorf("pages: archive: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete borra hard la página (cascade a children y revisions). Para uso admin/cleanup.
func (s *PgStore) Delete(ctx context.Context, id string) error {
	if !isUUID(id) {
		return fmt.Errorf("%w: id must be uuid", ErrInvalidInput)
	}
	const q = `DELETE FROM aria_pages WHERE id = $1::uuid`
	res, err := s.db.ExecContext(ctx, q, id)
	if err != nil {
		return fmt.Errorf("pages: delete: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Revisions ──────────────────────────────────────────────────────────────

func (s *PgStore) ListRevisions(ctx context.Context, pageID string, limit int) ([]*Revision, error) {
	if !isUUID(pageID) {
		return nil, fmt.Errorf("%w: page_id must be uuid", ErrInvalidInput)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT id::text, page_id::text, title, content_md, edited_by_uid::text,
		       COALESCE(edit_summary,''), created_at
		FROM aria_page_revisions WHERE page_id = $1::uuid
		ORDER BY created_at DESC LIMIT $2`
	rows, err := s.db.QueryContext(ctx, q, pageID, limit)
	if err != nil {
		return nil, fmt.Errorf("pages: list revisions: %w", err)
	}
	defer rows.Close()
	out := []*Revision{}
	for rows.Next() {
		rev := &Revision{}
		if err := rows.Scan(&rev.ID, &rev.PageID, &rev.Title, &rev.ContentMD, &rev.EditedByUID, &rev.EditSummary, &rev.CreatedAt); err != nil {
			return nil, fmt.Errorf("pages: scan revision: %w", err)
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

// RevertToRevision aplica el contenido de revisionID a pageID; insert nueva revisión "revert".
func (s *PgStore) RevertToRevision(ctx context.Context, pageID, revisionID, byUID string) error {
	if !isUUID(pageID) || !isUUID(revisionID) {
		return fmt.Errorf("%w: ids must be uuid", ErrInvalidInput)
	}
	if byUID == "" {
		return fmt.Errorf("%w: by_uid required", ErrInvalidInput)
	}
	const qRev = `SELECT title, content_md FROM aria_page_revisions WHERE id = $1::uuid AND page_id = $2::uuid`
	var title, content string
	if err := s.db.QueryRowContext(ctx, qRev, revisionID, pageID).Scan(&title, &content); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("pages: load revision: %w", err)
	}
	const qUpd = `UPDATE aria_pages SET title = $2, content_md = $3, updated_by_uid = $4::uuid, updated_at = NOW() WHERE id = $1::uuid`
	if _, err := s.db.ExecContext(ctx, qUpd, pageID, title, content, byUID); err != nil {
		return fmt.Errorf("pages: revert apply: %w", err)
	}
	return s.insertRevision(ctx, pageID, title, content, byUID, "revert to "+revisionID)
}

// ─── Internal helpers ───────────────────────────────────────────────────────

func (s *PgStore) insertRevision(ctx context.Context, pageID, title, content, byUID, summary string) error {
	const q = `
		INSERT INTO aria_page_revisions (page_id, title, content_md, edited_by_uid, edit_summary)
		VALUES ($1::uuid, $2, $3, $4::uuid, NULLIF($5,''))`
	_, err := s.db.ExecContext(ctx, q, pageID, title, content, byUID, summary)
	return err
}

// isDescendant retorna true si candidate es descendiente de root en el árbol.
// Usado por Move para prevenir ciclos.
func (s *PgStore) isDescendant(ctx context.Context, candidate, root string) (bool, error) {
	const q = `
		WITH RECURSIVE down AS (
			SELECT id, parent_id FROM aria_pages WHERE id = $1::uuid
			UNION ALL
			SELECT p.id, p.parent_id FROM aria_pages p
			JOIN down d ON p.parent_id = d.id
		)
		SELECT 1 FROM down WHERE id = $2::uuid LIMIT 1`
	var v int
	if err := s.db.QueryRowContext(ctx, q, root, candidate).Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return v == 1, nil
}

// buildBreadcrumbs camina parent_id arriba hasta root, retorna path raíz→hoja (incluye self).
func (s *PgStore) buildBreadcrumbs(ctx context.Context, page *Page) []PageBreadcrumb {
	if page == nil {
		return nil
	}
	const q = `
		WITH RECURSIVE up AS (
			SELECT id, parent_id, title, COALESCE(icon,'') AS icon, 0 AS depth
			FROM aria_pages WHERE id = $1::uuid
			UNION ALL
			SELECT p.id, p.parent_id, p.title, COALESCE(p.icon,''), u.depth + 1
			FROM aria_pages p JOIN up u ON p.id = u.parent_id
		)
		SELECT id::text, title, icon FROM up ORDER BY depth DESC`
	rows, err := s.db.QueryContext(ctx, q, page.ID)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := []PageBreadcrumb{}
	for rows.Next() {
		var b PageBreadcrumb
		if err := rows.Scan(&b.ID, &b.Title, &b.Icon); err != nil {
			return out
		}
		out = append(out, b)
	}
	return out
}

func scanPage(row interface{ Scan(...any) error }) (*Page, error) {
	page := &Page{}
	if err := row.Scan(
		&page.ID, &page.ParentID, &page.Title, &page.ContentMD,
		&page.Icon, &page.Project, &page.Scope, &page.ClientID,
		&page.PageType, &page.TemplateKey, &page.Sensitivity, &page.SortOrder,
		&page.IsArchived, &page.CreatedByUID, &page.CreatedAt,
		&page.UpdatedByUID, &page.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return page, nil
}

func nullableString(s string) sql.NullString {
	s = strings.TrimSpace(s)
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// nullableUUID acepta string vacío o UUID válido.
// Si vacío → NULL en DB. Si inválido → fuerza NULL para no romper el INSERT.
func nullableUUID(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	if !isUUID(s) {
		return nil
	}
	return s
}

func isUUID(s string) bool {
	_, err := uuid.Parse(strings.TrimSpace(s))
	return err == nil
}

func normalizeScope(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "personal", "project", "team", "client_knowledge":
		return s
	default:
		return "project"
	}
}

func normalizeSensitivity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "public", "internal", "client", "confidential":
		return s
	default:
		return "internal"
	}
}

func stringOr(ptr *string, fallback string) string {
	if ptr == nil {
		return fallback
	}
	return *ptr
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	// Remover acentos básicos.
	repl := strings.NewReplacer(
		"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ñ", "n", "ü", "u",
	)
	s = repl.Replace(s)
	s = slugRE.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 60 {
		s = s[:60]
	}
	return s
}
