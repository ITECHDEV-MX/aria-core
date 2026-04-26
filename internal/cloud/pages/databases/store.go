package databases

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Database es la representación de un container database (schema + metadata).
type Database struct {
	ID          string
	PageID      string
	Schema      []PropDef
	DefaultView string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Row es una fila de la database.
type Row struct {
	ID           string
	DatabaseID   string
	Props        map[string]any
	SortOrder    int
	CreatedByUID string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// View es una view guardada (table/kanban/gallery/list/calendar).
type View struct {
	ID           string
	DatabaseID   string
	Name         string
	ViewType     string
	Config       ViewConfig
	SortOrder    int
	CreatedByUID string
	CreatedAt    time.Time
}

// Store implementa CRUD de databases, rows y views sobre Postgres.
type Store struct {
	db *sql.DB
}

// New construye un Store sobre la conexión postgres ya abierta.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// validViewTypes contiene los view_type aceptados por el CHECK constraint.
var validViewTypes = map[string]struct{}{
	"table": {}, "kanban": {}, "gallery": {}, "list": {}, "calendar": {},
}

// ─── Database CRUD ──────────────────────────────────────────────────────────

// CreateParams parametriza la creación inicial de una database container.
type CreateParams struct {
	PageID       string
	Schema       []PropDef
	DefaultView  string
	CreatedByUID string
}

// Create inicializa una database para una page con page_type='database'.
// Si ya existe (page_id UNIQUE) retorna ErrConflict.
func (s *Store) Create(ctx context.Context, p CreateParams) (*Database, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("databases: store not initialized")
	}
	if _, err := uuid.Parse(p.PageID); err != nil {
		return nil, fmt.Errorf("%w: page_id must be UUID", ErrInvalidSchema)
	}
	if errs := ValidateSchema(p.Schema); len(errs) > 0 {
		return nil, schemaErrorList(errs)
	}
	defaultView := strings.TrimSpace(p.DefaultView)
	if defaultView == "" {
		defaultView = "table"
	}
	if _, ok := validViewTypes[defaultView]; !ok {
		return nil, fmt.Errorf("%w: invalid default_view %q", ErrInvalidSchema, defaultView)
	}
	if p.Schema == nil {
		p.Schema = []PropDef{}
	}
	schemaJSON, err := json.Marshal(p.Schema)
	if err != nil {
		return nil, fmt.Errorf("databases: marshal schema: %w", err)
	}
	const q = `
		INSERT INTO aria_page_databases (page_id, schema_json, default_view)
		VALUES ($1, $2::jsonb, $3)
		RETURNING id, page_id, schema_json, default_view, created_at, updated_at`
	row := s.db.QueryRowContext(ctx, q, p.PageID, string(schemaJSON), defaultView)
	out, err := scanDatabase(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, fmt.Errorf("%w: database already exists for page", ErrConflict)
		}
		return nil, fmt.Errorf("databases: create: %w", err)
	}
	return out, nil
}

// GetByPage retorna la database asociada a un page_id, o ErrNotFound.
func (s *Store) GetByPage(ctx context.Context, pageID string) (*Database, error) {
	if s == nil || s.db == nil {
		return nil, fmt.Errorf("databases: store not initialized")
	}
	const q = `
		SELECT id, page_id, schema_json, default_view, created_at, updated_at
		FROM aria_page_databases WHERE page_id = $1`
	row := s.db.QueryRowContext(ctx, q, pageID)
	out, err := scanDatabase(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("databases: get by page: %w", err)
	}
	return out, nil
}

// GetByID retorna la database por su id.
func (s *Store) GetByID(ctx context.Context, id string) (*Database, error) {
	const q = `
		SELECT id, page_id, schema_json, default_view, created_at, updated_at
		FROM aria_page_databases WHERE id = $1`
	row := s.db.QueryRowContext(ctx, q, id)
	out, err := scanDatabase(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("databases: get by id: %w", err)
	}
	return out, nil
}

// UpdateSchema reemplaza el schema_json de la database. Las rows existentes
// no son re-validadas automáticamente (sería destructivo); el caller puede
// pasar pruneUnknown=true para limpiar props eliminadas del nuevo schema.
func (s *Store) UpdateSchema(ctx context.Context, dbID string, newSchema []PropDef, pruneUnknown bool) (*Database, error) {
	if errs := ValidateSchema(newSchema); len(errs) > 0 {
		return nil, schemaErrorList(errs)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

	schemaJSON, err := json.Marshal(newSchema)
	if err != nil {
		return nil, fmt.Errorf("databases: marshal new schema: %w", err)
	}
	const q = `
		UPDATE aria_page_databases SET schema_json = $1::jsonb, updated_at = NOW()
		WHERE id = $2
		RETURNING id, page_id, schema_json, default_view, created_at, updated_at`
	row := tx.QueryRowContext(ctx, q, string(schemaJSON), dbID)
	out, err := scanDatabase(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("databases: update schema: %w", err)
	}

	if pruneUnknown {
		keepKeys := make([]string, 0, len(newSchema))
		for _, def := range newSchema {
			keepKeys = append(keepKeys, def.Key)
		}
		// Removemos del props_json toda key que no esté en la lista nueva,
		// usando jsonb_object_agg sobre un FILTER WHERE.
		const pruneQ = `
			UPDATE aria_page_database_rows
			SET props_json = COALESCE((
				SELECT jsonb_object_agg(k, v)
				FROM jsonb_each(props_json) AS x(k, v)
				WHERE k = ANY($1::text[])
			), '{}'::jsonb), updated_at = NOW()
			WHERE database_id = $2`
		if _, err := tx.ExecContext(ctx, pruneQ, asTextArray(keepKeys), dbID); err != nil {
			return nil, fmt.Errorf("databases: prune props: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── Rows CRUD ──────────────────────────────────────────────────────────────

// CreateRowParams parametriza la creación de una row.
type CreateRowParams struct {
	DatabaseID   string
	Props        map[string]any
	SortOrder    int
	CreatedByUID string
}

// CreateRow valida props contra el schema actual y la inserta.
func (s *Store) CreateRow(ctx context.Context, p CreateRowParams) (*Row, error) {
	db, err := s.GetByID(ctx, p.DatabaseID)
	if err != nil {
		return nil, err
	}
	if p.Props == nil {
		p.Props = map[string]any{}
	}
	if errs := ValidateRow(db.Schema, p.Props); len(errs) > 0 {
		return nil, rowErrorList(errs)
	}
	if _, err := uuid.Parse(strings.TrimSpace(p.CreatedByUID)); err != nil {
		return nil, fmt.Errorf("%w: created_by_uid must be UUID", ErrInvalidRow)
	}
	propsJSON, err := json.Marshal(p.Props)
	if err != nil {
		return nil, fmt.Errorf("databases: marshal props: %w", err)
	}
	const q = `
		INSERT INTO aria_page_database_rows (database_id, props_json, sort_order, created_by_uid)
		VALUES ($1, $2::jsonb, $3, $4)
		RETURNING id, database_id, props_json, sort_order, created_by_uid, created_at, updated_at`
	row := s.db.QueryRowContext(ctx, q, p.DatabaseID, string(propsJSON), p.SortOrder, p.CreatedByUID)
	return scanRow(row)
}

// ListRowsOpts parametriza ListRows.
type ListRowsOpts struct {
	Filters []Filter
	Sorts   []Sort
	Limit   int
	Offset  int
}

// ListRows retorna rows aplicando filters/sort. Limit default=100.
func (s *Store) ListRows(ctx context.Context, dbID string, opts ListRowsOpts) ([]*Row, error) {
	db, err := s.GetByID(ctx, dbID)
	if err != nil {
		return nil, err
	}
	whereClause, args, err := BuildWhereClause(opts.Filters, db.Schema, 1)
	if err != nil {
		return nil, err
	}
	orderBy, err := BuildOrderBy(opts.Sorts, db.Schema)
	if err != nil {
		return nil, err
	}

	limit := opts.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}

	allArgs := []any{dbID}
	allArgs = append(allArgs, args...)
	allArgs = append(allArgs, limit, offset)

	q := `SELECT id, database_id, props_json, sort_order, created_by_uid, created_at, updated_at
		  FROM aria_page_database_rows WHERE database_id = $1`
	if whereClause != "" {
		q += " AND " + whereClause
	}
	q += " " + orderBy + fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(allArgs)-1, len(allArgs))

	rows, err := s.db.QueryContext(ctx, q, allArgs...)
	if err != nil {
		return nil, fmt.Errorf("databases: list rows: %w", err)
	}
	defer rows.Close()
	var out []*Row
	for rows.Next() {
		r, err := scanRowFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CountRows retorna el número total de rows que matchean los filters.
func (s *Store) CountRows(ctx context.Context, dbID string, filters []Filter) (int, error) {
	db, err := s.GetByID(ctx, dbID)
	if err != nil {
		return 0, err
	}
	whereClause, args, err := BuildWhereClause(filters, db.Schema, 1)
	if err != nil {
		return 0, err
	}
	allArgs := []any{dbID}
	allArgs = append(allArgs, args...)
	q := `SELECT COUNT(*) FROM aria_page_database_rows WHERE database_id = $1`
	if whereClause != "" {
		q += " AND " + whereClause
	}
	var n int
	if err := s.db.QueryRowContext(ctx, q, allArgs...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// GetRow retorna una row específica.
func (s *Store) GetRow(ctx context.Context, rowID string) (*Row, error) {
	const q = `SELECT id, database_id, props_json, sort_order, created_by_uid, created_at, updated_at
		  FROM aria_page_database_rows WHERE id = $1`
	row := s.db.QueryRowContext(ctx, q, rowID)
	r, err := scanRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return r, nil
}

// UpdateRowProps hace merge sobre props_json (no replace). Si pasas un key con
// nil, se elimina del map. Valida resultado contra schema.
func (s *Store) UpdateRowProps(ctx context.Context, rowID string, patch map[string]any) (*Row, error) {
	current, err := s.GetRow(ctx, rowID)
	if err != nil {
		return nil, err
	}
	db, err := s.GetByID(ctx, current.DatabaseID)
	if err != nil {
		return nil, err
	}
	merged := make(map[string]any, len(current.Props)+len(patch))
	for k, v := range current.Props {
		merged[k] = v
	}
	for k, v := range patch {
		if v == nil {
			delete(merged, k)
			continue
		}
		merged[k] = v
	}
	if errs := ValidateRow(db.Schema, merged); len(errs) > 0 {
		return nil, rowErrorList(errs)
	}
	merged2, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	const q = `
		UPDATE aria_page_database_rows SET props_json = $1::jsonb, updated_at = NOW()
		WHERE id = $2
		RETURNING id, database_id, props_json, sort_order, created_by_uid, created_at, updated_at`
	row := s.db.QueryRowContext(ctx, q, string(merged2), rowID)
	return scanRow(row)
}

// MoveRow updatea sort_order, útil para reordenar drag-drop.
func (s *Store) MoveRow(ctx context.Context, rowID string, newOrder int) error {
	const q = `UPDATE aria_page_database_rows SET sort_order = $1, updated_at = NOW() WHERE id = $2`
	res, err := s.db.ExecContext(ctx, q, newOrder, rowID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteRow borra una row.
func (s *Store) DeleteRow(ctx context.Context, rowID string) error {
	const q = `DELETE FROM aria_page_database_rows WHERE id = $1`
	res, err := s.db.ExecContext(ctx, q, rowID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ─── Views CRUD ─────────────────────────────────────────────────────────────

// CreateViewParams parametriza la creación de una view.
type CreateViewParams struct {
	DatabaseID   string
	Name         string
	ViewType     string
	Config       ViewConfig
	SortOrder    int
	CreatedByUID string
}

// CreateView crea una view guardada.
func (s *Store) CreateView(ctx context.Context, p CreateViewParams) (*View, error) {
	if _, ok := validViewTypes[p.ViewType]; !ok {
		return nil, fmt.Errorf("%w: invalid view_type %q", ErrInvalidSchema, p.ViewType)
	}
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("%w: name is required", ErrInvalidSchema)
	}
	if _, err := uuid.Parse(strings.TrimSpace(p.CreatedByUID)); err != nil {
		return nil, fmt.Errorf("%w: created_by_uid must be UUID", ErrInvalidSchema)
	}
	configJSON, err := json.Marshal(p.Config)
	if err != nil {
		return nil, err
	}
	const q = `
		INSERT INTO aria_page_database_views (database_id, name, view_type, config_json, sort_order, created_by_uid)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6)
		RETURNING id, database_id, name, view_type, config_json, sort_order, created_by_uid, created_at`
	row := s.db.QueryRowContext(ctx, q, p.DatabaseID, p.Name, p.ViewType, string(configJSON), p.SortOrder, p.CreatedByUID)
	return scanView(row)
}

// ListViews retorna las views de una database (ordenadas por sort_order).
func (s *Store) ListViews(ctx context.Context, dbID string) ([]*View, error) {
	const q = `
		SELECT id, database_id, name, view_type, config_json, sort_order, created_by_uid, created_at
		FROM aria_page_database_views WHERE database_id = $1
		ORDER BY sort_order ASC, created_at ASC`
	rows, err := s.db.QueryContext(ctx, q, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*View
	for rows.Next() {
		v, err := scanViewFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// GetView retorna una view por id.
func (s *Store) GetView(ctx context.Context, viewID string) (*View, error) {
	const q = `
		SELECT id, database_id, name, view_type, config_json, sort_order, created_by_uid, created_at
		FROM aria_page_database_views WHERE id = $1`
	row := s.db.QueryRowContext(ctx, q, viewID)
	v, err := scanView(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return v, nil
}

// UpdateView updatea name + view_type + config_json + sort_order.
func (s *Store) UpdateView(ctx context.Context, viewID string, name, viewType string, config ViewConfig, sortOrder int) (*View, error) {
	if _, ok := validViewTypes[viewType]; !ok {
		return nil, fmt.Errorf("%w: invalid view_type %q", ErrInvalidSchema, viewType)
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	const q = `
		UPDATE aria_page_database_views
		SET name = $1, view_type = $2, config_json = $3::jsonb, sort_order = $4
		WHERE id = $5
		RETURNING id, database_id, name, view_type, config_json, sort_order, created_by_uid, created_at`
	row := s.db.QueryRowContext(ctx, q, name, viewType, string(configJSON), sortOrder, viewID)
	v, err := scanView(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return v, nil
}

// DeleteView borra una view.
func (s *Store) DeleteView(ctx context.Context, viewID string) error {
	const q = `DELETE FROM aria_page_database_views WHERE id = $1`
	res, err := s.db.ExecContext(ctx, q, viewID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GroupRowsByKey agrupa rows en buckets según el valor de groupKey.
// Útil para Kanban view: groupKey debe apuntar a una prop de tipo 'select'.
// Rows con valor faltante van al bucket "" (vacío).
func GroupRowsByKey(rows []*Row, groupKey string) map[string][]*Row {
	out := make(map[string][]*Row)
	for _, r := range rows {
		val := ""
		if v, ok := r.Props[groupKey]; ok && v != nil {
			if s, ok := v.(string); ok {
				val = s
			}
		}
		out[val] = append(out[val], r)
	}
	return out
}

// ─── helpers internos ───────────────────────────────────────────────────────

type rowScanner interface {
	Scan(dest ...any) error
}

func scanDatabase(s rowScanner) (*Database, error) {
	var d Database
	var schemaRaw []byte
	if err := s.Scan(&d.ID, &d.PageID, &schemaRaw, &d.DefaultView, &d.CreatedAt, &d.UpdatedAt); err != nil {
		return nil, err
	}
	if len(schemaRaw) > 0 {
		if err := json.Unmarshal(schemaRaw, &d.Schema); err != nil {
			return nil, fmt.Errorf("databases: scan schema: %w", err)
		}
	}
	if d.Schema == nil {
		d.Schema = []PropDef{}
	}
	return &d, nil
}

func scanRow(s rowScanner) (*Row, error) {
	var r Row
	var propsRaw []byte
	if err := s.Scan(&r.ID, &r.DatabaseID, &propsRaw, &r.SortOrder, &r.CreatedByUID, &r.CreatedAt, &r.UpdatedAt); err != nil {
		return nil, err
	}
	if len(propsRaw) > 0 {
		if err := json.Unmarshal(propsRaw, &r.Props); err != nil {
			return nil, fmt.Errorf("databases: scan props: %w", err)
		}
	}
	if r.Props == nil {
		r.Props = map[string]any{}
	}
	return &r, nil
}

func scanRowFromRows(rs *sql.Rows) (*Row, error) {
	return scanRow(rs)
}

func scanView(s rowScanner) (*View, error) {
	var v View
	var configRaw []byte
	if err := s.Scan(&v.ID, &v.DatabaseID, &v.Name, &v.ViewType, &configRaw, &v.SortOrder, &v.CreatedByUID, &v.CreatedAt); err != nil {
		return nil, err
	}
	if len(configRaw) > 0 {
		if err := json.Unmarshal(configRaw, &v.Config); err != nil {
			return nil, fmt.Errorf("databases: scan view config: %w", err)
		}
	}
	return &v, nil
}

func scanViewFromRows(rs *sql.Rows) (*View, error) {
	return scanView(rs)
}

func schemaErrorList(errs []ValidationError) error {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return fmt.Errorf("%w: %s", ErrInvalidSchema, strings.Join(parts, "; "))
}

func rowErrorList(errs []ValidationError) error {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return fmt.Errorf("%w: %s", ErrInvalidRow, strings.Join(parts, "; "))
}

// asTextArray formatea []string como literal Postgres text[].
func asTextArray(items []string) string {
	if len(items) == 0 {
		return "{}"
	}
	parts := make([]string, len(items))
	for i, s := range items {
		// escape comillas dobles y backslashes
		s = strings.ReplaceAll(s, `\`, `\\`)
		s = strings.ReplaceAll(s, `"`, `\"`)
		parts[i] = `"` + s + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// isUniqueViolation detecta SQLSTATE 23505 sin importar pgconn.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(msg, "duplicate key")
}
