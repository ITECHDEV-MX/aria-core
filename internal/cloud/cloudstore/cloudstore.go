package cloudstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/chunkcodec"
	coresync "github.com/ITECHDEV-MX/aria-core/internal/sync"
	"github.com/jackc/pgx/v5/pgconn"
	_ "github.com/jackc/pgx/v5/stdlib"
)

type CloudStore struct {
	db                     *sql.DB
	dashboardAllowedScopes map[string]struct{}
	dashboardReadModelMu   sync.RWMutex
	dashboardReadModel     dashboardReadModel
	dashboardReadModelOK   bool
	dashboardReadModelLoad func() (dashboardReadModel, error)
}

var ErrChunkNotFound = errors.New("cloudstore: chunk not found")
var ErrChunkConflict = errors.New("cloudstore: chunk id conflict")

func New(cfg cloud.Config) (*CloudStore, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("cloudstore: database dsn is required")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: open postgres: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("cloudstore: ping postgres: %w", err)
	}
	store := &CloudStore{db: db}
	if err := store.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (cs *CloudStore) Close() error {
	if cs == nil || cs.db == nil {
		return nil
	}
	return cs.db.Close()
}

// DB exposes the underlying *sql.DB. Used by adapters that need direct SQL access
// (e.g., cloudusers.Store on top of cloud_users table).
func (cs *CloudStore) DB() *sql.DB {
	if cs == nil {
		return nil
	}
	return cs.db
}

func (cs *CloudStore) SetDashboardAllowedProjects(projects []string) {
	if cs == nil {
		return
	}
	cs.dashboardAllowedScopes = make(map[string]struct{})
	for _, project := range projects {
		project = strings.TrimSpace(project)
		if project == "" {
			continue
		}
		cs.dashboardAllowedScopes[project] = struct{}{}
	}
	cs.invalidateDashboardReadModel()
}

type User struct {
	ID           string
	Username     string
	Email        string
	PasswordHash string
}

func (cs *CloudStore) CreateUser(username, email, _ string) (*User, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	const q = `
		INSERT INTO cloud_users (username, email, password_hash)
		VALUES ($1, $2, '')
		ON CONFLICT (username) DO UPDATE SET email = EXCLUDED.email
		RETURNING id::text, username, email, password_hash`
	var u User
	if err := cs.db.QueryRowContext(context.Background(), q, strings.TrimSpace(username), strings.TrimSpace(email)).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash); err != nil {
		return nil, fmt.Errorf("cloudstore: create user: %w", err)
	}
	return &u, nil
}

func (cs *CloudStore) GetUserByUsername(username string) (*User, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	const q = `SELECT id::text, username, email, password_hash FROM cloud_users WHERE username = $1`
	var u User
	err := cs.db.QueryRowContext(context.Background(), q, strings.TrimSpace(username)).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cloudstore: lookup user by username: %w", err)
	}
	return &u, nil
}

func (cs *CloudStore) GetUserByEmail(email string) (*User, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	const q = `SELECT id::text, username, email, password_hash FROM cloud_users WHERE email = $1`
	var u User
	err := cs.db.QueryRowContext(context.Background(), q, strings.TrimSpace(email)).Scan(&u.ID, &u.Username, &u.Email, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cloudstore: lookup user by email: %w", err)
	}
	return &u, nil
}

func (cs *CloudStore) ReadManifest(ctx context.Context, project string) (*coresync.Manifest, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("cloudstore: project is required")
	}
	rows, err := cs.db.QueryContext(ctx, `
		SELECT chunk_id, created_by, COALESCE(client_created_at, created_at) AS manifest_created_at, sessions_count, observations_count, prompts_count, created_at
		FROM cloud_chunks
		WHERE project_name = $1
		ORDER BY created_at ASC, chunk_id ASC`, project)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: query manifest: %w", err)
	}
	defer rows.Close()

	manifestRows := make([]manifestRow, 0)
	for rows.Next() {
		var row manifestRow
		if err := rows.Scan(&row.chunkID, &row.createdBy, &row.manifestTime, &row.sessions, &row.observations, &row.prompts, &row.serverCreated); err != nil {
			return nil, fmt.Errorf("cloudstore: scan manifest: %w", err)
		}
		manifestRows = append(manifestRows, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: iterate manifest: %w", err)
	}
	return &coresync.Manifest{Version: 1, Chunks: toManifestEntries(manifestRows)}, nil
}

type manifestRow struct {
	chunkID       string
	createdBy     string
	manifestTime  time.Time
	sessions      int
	observations  int
	prompts       int
	serverCreated time.Time
}

func toManifestEntries(rows []manifestRow) []coresync.ChunkEntry {
	sort.Slice(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if !left.serverCreated.Equal(right.serverCreated) {
			return left.serverCreated.Before(right.serverCreated)
		}
		return left.chunkID < right.chunkID
	})
	entries := make([]coresync.ChunkEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, coresync.ChunkEntry{
			ID:        row.chunkID,
			CreatedBy: row.createdBy,
			CreatedAt: row.manifestTime.UTC().Format(time.RFC3339),
			Sessions:  row.sessions,
			Memories:  row.observations,
			Prompts:   row.prompts,
		})
	}
	return entries
}

func (cs *CloudStore) WriteChunk(ctx context.Context, project, chunkID, createdBy, clientCreatedAt string, payload []byte) error {
	if cs == nil || cs.db == nil {
		return fmt.Errorf("cloudstore: not initialized")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return fmt.Errorf("cloudstore: project is required")
	}
	if strings.TrimSpace(chunkID) == "" {
		return fmt.Errorf("cloudstore: chunk id is required")
	}
	expectedChunkID := chunkIDFromPayload(payload)
	if chunkID != expectedChunkID {
		return fmt.Errorf("cloudstore: chunk id does not match payload hash (expected %s)", expectedChunkID)
	}
	originCreatedAt, err := parseClientCreatedAt(clientCreatedAt)
	if err != nil {
		return err
	}

	var existingPayload []byte
	err = cs.db.QueryRowContext(ctx, `SELECT payload::text FROM cloud_chunks WHERE project_name = $1 AND chunk_id = $2`, project, chunkID).Scan(&existingPayload)
	if err == nil {
		normalizedIncoming := normalizeJSON(payload)
		normalizedExisting := normalizeJSON(existingPayload)
		if string(normalizedIncoming) != string(normalizedExisting) {
			return fmt.Errorf("%w: existing chunk %q has different payload", ErrChunkConflict, chunkID)
		}
		_ = cs.indexChunkSessions(ctx, project, payload)
		cs.invalidateDashboardReadModel()
		return nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("cloudstore: read existing chunk: %w", err)
	}

	counts := summarizeChunk(payload)
	_, err = cs.db.ExecContext(ctx, `
		INSERT INTO cloud_chunks (project_name, chunk_id, created_by, client_created_at, payload, sessions_count, observations_count, prompts_count)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		project, strings.TrimSpace(chunkID), strings.TrimSpace(createdBy), originCreatedAt, payload, counts.sessions, counts.observations, counts.prompts)
	if err != nil {
		if isUniqueViolation(err) {
			conflictErr := cs.resolveChunkConflict(ctx, project, chunkID, payload)
			if conflictErr != nil {
				return conflictErr
			}
			cs.invalidateDashboardReadModel()
			return nil
		}
		return fmt.Errorf("cloudstore: write chunk: %w", err)
	}
	if err := cs.indexChunkSessions(ctx, project, payload); err != nil {
		return err
	}
	cs.invalidateDashboardReadModel()
	return nil
}

func (cs *CloudStore) invalidateDashboardReadModel() {
	if cs == nil {
		return
	}
	cs.dashboardReadModelMu.Lock()
	defer cs.dashboardReadModelMu.Unlock()
	cs.dashboardReadModel = dashboardReadModel{}
	cs.dashboardReadModelOK = false
}

func (cs *CloudStore) KnownSessionIDs(ctx context.Context, project string) (map[string]struct{}, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("cloudstore: project is required")
	}
	rows, err := cs.db.QueryContext(ctx, `SELECT session_id FROM cloud_project_sessions WHERE project_name = $1`, project)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: query session index: %w", err)
	}
	defer rows.Close()

	known := make(map[string]struct{})
	for rows.Next() {
		var sessionID string
		if err := rows.Scan(&sessionID); err != nil {
			return nil, fmt.Errorf("cloudstore: scan session index: %w", err)
		}
		sessionID = strings.TrimSpace(sessionID)
		if sessionID == "" {
			continue
		}
		known[sessionID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cloudstore: iterate session index: %w", err)
	}
	return known, nil
}

func (cs *CloudStore) indexChunkSessions(ctx context.Context, project string, payload []byte) error {
	sessionIDs := collectSessionIDsFromPayload(payload)
	if len(sessionIDs) == 0 {
		return nil
	}
	for sessionID := range sessionIDs {
		if _, err := cs.db.ExecContext(ctx,
			`INSERT INTO cloud_project_sessions (project_name, session_id) VALUES ($1, $2) ON CONFLICT (project_name, session_id) DO NOTHING`,
			project, sessionID,
		); err != nil {
			return fmt.Errorf("cloudstore: index session %q: %w", sessionID, err)
		}
	}
	return nil
}

func (cs *CloudStore) backfillProjectSessionsFromChunks(ctx context.Context) error {
	rows, err := cs.db.QueryContext(ctx, `SELECT project_name, payload FROM cloud_chunks`)
	if err != nil {
		return fmt.Errorf("cloudstore: backfill session index: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var project string
		var payload []byte
		if err := rows.Scan(&project, &payload); err != nil {
			return fmt.Errorf("cloudstore: backfill session index scan: %w", err)
		}
		if err := cs.indexChunkSessions(ctx, project, payload); err != nil {
			return fmt.Errorf("cloudstore: backfill session index row: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("cloudstore: backfill session index iterate: %w", err)
	}
	return nil
}

func collectSessionIDsFromPayload(payload []byte) map[string]struct{} {
	chunk, err := parseChunkData(payload)
	if err != nil {
		return map[string]struct{}{}
	}
	return collectSessionIDs(chunk)
}

func parseChunkData(payload []byte) (coresync.ChunkData, error) {
	var chunk coresync.ChunkData
	if err := json.Unmarshal(payload, &chunk); err != nil {
		return coresync.ChunkData{}, err
	}
	return chunk, nil
}

func collectSessionIDs(chunk coresync.ChunkData) map[string]struct{} {
	sessionIDs := make(map[string]struct{})
	for _, session := range chunk.Sessions {
		sessionID := strings.TrimSpace(session.ID)
		if sessionID != "" {
			sessionIDs[sessionID] = struct{}{}
		}
	}
	for _, mutation := range chunk.Mutations {
		if mutation.Entity != "session" || mutation.Op == "delete" {
			continue
		}
		mutationPayload := strings.TrimSpace(mutation.Payload)
		if mutationPayload == "" {
			continue
		}
		var body struct {
			ID string `json:"id"`
		}
		if err := chunkcodec.DecodeSyncMutationPayload(mutationPayload, &body); err != nil {
			continue
		}
		sessionID := strings.TrimSpace(body.ID)
		if sessionID != "" {
			sessionIDs[sessionID] = struct{}{}
		}
	}
	return sessionIDs
}

func (cs *CloudStore) resolveChunkConflict(ctx context.Context, project, chunkID string, payload []byte) error {
	var existingPayload []byte
	err := cs.db.QueryRowContext(ctx, `SELECT payload::text FROM cloud_chunks WHERE project_name = $1 AND chunk_id = $2`, project, chunkID).Scan(&existingPayload)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: existing chunk %q was concurrently inserted", ErrChunkConflict, chunkID)
	}
	if err != nil {
		return fmt.Errorf("cloudstore: resolve chunk conflict: %w", err)
	}
	normalizedIncoming := normalizeJSON(payload)
	normalizedExisting := normalizeJSON(existingPayload)
	if string(normalizedIncoming) == string(normalizedExisting) {
		return nil
	}
	return fmt.Errorf("%w: existing chunk %q has different payload", ErrChunkConflict, chunkID)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505"
}

func (cs *CloudStore) ReadChunk(ctx context.Context, project, chunkID string) ([]byte, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	project = strings.TrimSpace(project)
	if project == "" {
		return nil, fmt.Errorf("cloudstore: project is required")
	}
	var payload []byte
	err := cs.db.QueryRowContext(ctx, `SELECT payload FROM cloud_chunks WHERE project_name = $1 AND chunk_id = $2`, project, strings.TrimSpace(chunkID)).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", ErrChunkNotFound, chunkID)
	}
	if err != nil {
		return nil, fmt.Errorf("cloudstore: read chunk: %w", err)
	}
	return payload, nil
}

func (cs *CloudStore) migrate(ctx context.Context) error {
	queries := []string{
		`CREATE EXTENSION IF NOT EXISTS pgcrypto`,
		`CREATE TABLE IF NOT EXISTS cloud_users (
			id BIGSERIAL PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`ALTER TABLE cloud_users ADD COLUMN IF NOT EXISTS uid UUID UNIQUE`,
		`UPDATE cloud_users SET uid = gen_random_uuid() WHERE uid IS NULL`,
		`ALTER TABLE cloud_users ALTER COLUMN uid SET NOT NULL`,
		`ALTER TABLE cloud_users ALTER COLUMN uid SET DEFAULT gen_random_uuid()`,
		`ALTER TABLE cloud_users ADD COLUMN IF NOT EXISTS name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cloud_users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'dev'`,
		`ALTER TABLE cloud_users ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE`,
		`ALTER TABLE cloud_users ADD COLUMN IF NOT EXISTS client_id UUID`,
		`ALTER TABLE cloud_users ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()`,
		`DO $$ BEGIN
			IF EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'cloud_users_role_check') THEN
				ALTER TABLE cloud_users DROP CONSTRAINT cloud_users_role_check;
			END IF;
			ALTER TABLE cloud_users ADD CONSTRAINT cloud_users_role_check CHECK (role IN ('admin','dev','cotizador','project_admin'));
		END $$`,
		`CREATE INDEX IF NOT EXISTS idx_cloud_users_email ON cloud_users(lower(email))`,
		// Multi-role: tabla many-to-many entre usuarios y roles.
		`CREATE TABLE IF NOT EXISTS cloud_user_roles (
			uid UUID NOT NULL REFERENCES cloud_users(uid) ON DELETE CASCADE,
			role TEXT NOT NULL,
			granted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (uid, role),
			CONSTRAINT cloud_user_roles_role_check CHECK (role IN ('admin','dev','cotizador','project_admin'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cloud_user_roles_uid ON cloud_user_roles(uid)`,
		`CREATE INDEX IF NOT EXISTS idx_cloud_user_roles_role ON cloud_user_roles(role)`,
		// Backfill desde la columna role single (legacy) hacia cloud_user_roles.
		`INSERT INTO cloud_user_roles (uid, role)
		 SELECT uid, role FROM cloud_users
		 WHERE role IS NOT NULL AND role <> ''
		 ON CONFLICT DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS cloud_chunks (
			project_name TEXT NOT NULL DEFAULT 'default',
			chunk_id TEXT NOT NULL,
			created_by TEXT NOT NULL,
			client_created_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			payload JSONB NOT NULL,
			sessions_count INTEGER NOT NULL DEFAULT 0,
			observations_count INTEGER NOT NULL DEFAULT 0,
			prompts_count INTEGER NOT NULL DEFAULT 0
		)`,
		`ALTER TABLE cloud_chunks ADD COLUMN IF NOT EXISTS project_name TEXT`,
		`ALTER TABLE cloud_chunks ADD COLUMN IF NOT EXISTS client_created_at TIMESTAMPTZ`,
		`UPDATE cloud_chunks SET project_name = 'default' WHERE project_name IS NULL OR btrim(project_name) = ''`,
		`ALTER TABLE cloud_chunks ALTER COLUMN project_name SET NOT NULL`,
		`DO $$ BEGIN
			IF EXISTS (
				SELECT 1 FROM pg_constraint
				WHERE conname = 'cloud_chunks_pkey' AND conrelid = 'cloud_chunks'::regclass
			) THEN
				ALTER TABLE cloud_chunks DROP CONSTRAINT cloud_chunks_pkey;
			END IF;
		END $$`,
		`CREATE UNIQUE INDEX IF NOT EXISTS cloud_chunks_project_chunk_uidx ON cloud_chunks (project_name, chunk_id)`,
		// created_by_role: scoping de visibilidad por rol de quien guardó el chunk.
		// 'shared' = visible para todos los autenticados (default para chunks legacy y para
		// uploads sin contexto de role). Cualquier otro valor = visible solo para users con
		// ese mismo role o rol admin.
		`ALTER TABLE cloud_chunks ADD COLUMN IF NOT EXISTS created_by_role TEXT NOT NULL DEFAULT 'shared'`,
		`CREATE INDEX IF NOT EXISTS idx_cloud_chunks_created_by_role ON cloud_chunks(created_by_role)`,

		// === Cotizador module (commit 2: leads + clients base) ===
		// Estado del lead a lo largo del funnel.
		`CREATE TABLE IF NOT EXISTS cotizador_leads (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name TEXT NOT NULL,
			company TEXT NOT NULL DEFAULT '',
			email TEXT NOT NULL DEFAULT '',
			phone TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL DEFAULT 'new',
			notes TEXT NOT NULL DEFAULT '',
			owner_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			created_by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			created_by_role TEXT NOT NULL DEFAULT 'cotizador',
			client_id UUID,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT cotizador_leads_status_check CHECK (status IN ('new','contacted','qualified','quoting','won','lost'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_leads_status ON cotizador_leads(status)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_leads_owner ON cotizador_leads(owner_uid)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_leads_created_at ON cotizador_leads(created_at DESC)`,

		// Audit log de cambios de estado del lead.
		`CREATE TABLE IF NOT EXISTS cotizador_lead_history (
			id BIGSERIAL PRIMARY KEY,
			lead_id UUID NOT NULL REFERENCES cotizador_leads(id) ON DELETE CASCADE,
			action TEXT NOT NULL,
			from_status TEXT,
			to_status TEXT,
			by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			notes TEXT NOT NULL DEFAULT '',
			occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_lead_history_lead ON cotizador_lead_history(lead_id, occurred_at DESC)`,

		// Clientes confirmados (post-won). Más metadata que un lead.
		`CREATE TABLE IF NOT EXISTS cotizador_clients (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			lead_id UUID REFERENCES cotizador_leads(id) ON DELETE SET NULL,
			legal_name TEXT NOT NULL,
			rfc TEXT NOT NULL DEFAULT '',
			fiscal_address TEXT NOT NULL DEFAULT '',
			billing_email TEXT NOT NULL DEFAULT '',
			contacts_json JSONB NOT NULL DEFAULT '[]',
			notes TEXT NOT NULL DEFAULT '',
			created_by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_clients_lead ON cotizador_clients(lead_id)`,

		// === Cotizador commit 4: RFPs + Quotes + items + FTS histórico ===
		// RFPs analizados (texto pegado o pdf-extracted; analysis_json es output del análisis manual o IA).
		`CREATE TABLE IF NOT EXISTS cotizador_rfps (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			lead_id UUID NOT NULL REFERENCES cotizador_leads(id) ON DELETE CASCADE,
			source_type TEXT NOT NULL DEFAULT 'text',
			source_content TEXT NOT NULL DEFAULT '',
			analysis_json JSONB NOT NULL DEFAULT '{}',
			created_by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT cotizador_rfps_source_check CHECK (source_type IN ('text','pdf','url'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_rfps_lead ON cotizador_rfps(lead_id, created_at DESC)`,

		// Cotizaciones (quotes). Una quote pertenece a un lead, opcionalmente vinculada a un RFP.
		// version es auto-incremental por lead (v1, v2, v3 cuando se reedita).
		`CREATE TABLE IF NOT EXISTS cotizador_quotes (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			lead_id UUID NOT NULL REFERENCES cotizador_leads(id) ON DELETE CASCADE,
			rfp_id UUID REFERENCES cotizador_rfps(id) ON DELETE SET NULL,
			version INTEGER NOT NULL DEFAULT 1,
			status TEXT NOT NULL DEFAULT 'draft',
			currency TEXT NOT NULL DEFAULT 'MXN',
			subtotal NUMERIC(14,2) NOT NULL DEFAULT 0,
			taxes NUMERIC(14,2) NOT NULL DEFAULT 0,
			total NUMERIC(14,2) NOT NULL DEFAULT 0,
			valid_until DATE,
			terms TEXT NOT NULL DEFAULT '',
			justification TEXT NOT NULL DEFAULT '',
			approved_at TIMESTAMPTZ,
			approved_by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			created_by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			created_by_role TEXT NOT NULL DEFAULT 'cotizador',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT cotizador_quotes_status_check CHECK (status IN ('draft','sent','in_review','approved','rejected','expired'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quotes_lead ON cotizador_quotes(lead_id, version DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quotes_status ON cotizador_quotes(status)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quotes_created_at ON cotizador_quotes(created_at DESC)`,

		// Items de cada cotización.
		`CREATE TABLE IF NOT EXISTS cotizador_quote_items (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			quote_id UUID NOT NULL REFERENCES cotizador_quotes(id) ON DELETE CASCADE,
			sku TEXT NOT NULL DEFAULT '',
			description TEXT NOT NULL,
			qty NUMERIC(12,3) NOT NULL DEFAULT 1,
			unit_price NUMERIC(14,2) NOT NULL DEFAULT 0,
			subtotal NUMERIC(14,2) NOT NULL DEFAULT 0,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quote_items_quote ON cotizador_quote_items(quote_id, sort_order)`,
		// FTS para búsqueda histórica de items (commit 5 lo va a usar full).
		`ALTER TABLE cotizador_quote_items ADD COLUMN IF NOT EXISTS description_tsv tsvector
		 GENERATED ALWAYS AS (to_tsvector('simple', coalesce(description,''))) STORED`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quote_items_fts ON cotizador_quote_items USING GIN (description_tsv)`,

		// Audit log de cambios de estado de la quote (snapshot del momento).
		`CREATE TABLE IF NOT EXISTS cotizador_quote_history (
			id BIGSERIAL PRIMARY KEY,
			quote_id UUID NOT NULL REFERENCES cotizador_quotes(id) ON DELETE CASCADE,
			action TEXT NOT NULL,
			from_status TEXT,
			to_status TEXT,
			by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			snapshot_json JSONB,
			notes TEXT NOT NULL DEFAULT '',
			occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quote_history_quote ON cotizador_quote_history(quote_id, occurred_at DESC)`,

		// === Cotizador commit 5: memoria histórica ===
		// Outcome final por quote (cuando se cierra: approved/rejected/expired).
		// Una quote puede tener varios outcomes históricos si se reabre y vuelve a cerrar.
		`CREATE TABLE IF NOT EXISTS cotizador_quote_outcomes (
			id BIGSERIAL PRIMARY KEY,
			quote_id UUID NOT NULL REFERENCES cotizador_quotes(id) ON DELETE CASCADE,
			outcome TEXT NOT NULL,
			reason TEXT NOT NULL DEFAULT '',
			recorded_by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			CONSTRAINT cotizador_quote_outcomes_check CHECK (outcome IN ('won','lost','expired'))
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quote_outcomes_quote ON cotizador_quote_outcomes(quote_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quote_outcomes_outcome ON cotizador_quote_outcomes(outcome)`,

		// Lecciones aprendidas (memoria libre, atadas a quote por decisión 4a, opcionalmente con tags).
		`CREATE TABLE IF NOT EXISTS cotizador_lessons (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			quote_id UUID REFERENCES cotizador_quotes(id) ON DELETE SET NULL,
			lead_id UUID REFERENCES cotizador_leads(id) ON DELETE SET NULL,
			text TEXT NOT NULL,
			tags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],
			created_by_uid UUID REFERENCES cloud_users(uid) ON DELETE SET NULL,
			created_by_role TEXT NOT NULL DEFAULT 'cotizador',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_lessons_quote ON cotizador_lessons(quote_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_lessons_lead ON cotizador_lessons(lead_id)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_lessons_tags ON cotizador_lessons USING GIN (tags)`,
		`ALTER TABLE cotizador_lessons ADD COLUMN IF NOT EXISTS text_tsv tsvector
		 GENERATED ALWAYS AS (to_tsvector('simple', coalesce(text,''))) STORED`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_lessons_fts ON cotizador_lessons USING GIN (text_tsv)`,

		// === Cotizador commit 7: formato propuesta completa ===
		// Header info para portada estilo iTechDev: folio, tipo de propuesta, producto,
		// preparado para, preparado por.
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS folio TEXT`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS proposal_type TEXT NOT NULL DEFAULT 'commercial'`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS product_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS product_subtitle TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS tags TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[]`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS prepared_for_company TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS prepared_for_area TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS prepared_for_contact_name TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS prepared_for_contact_email TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS issue_date DATE`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS prepared_by_name TEXT NOT NULL DEFAULT 'Juan Carlos Guajardo'`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS prepared_by_email TEXT NOT NULL DEFAULT 'jcguajardo@itechdev.com.mx'`,
		`ALTER TABLE cotizador_quotes ADD COLUMN IF NOT EXISTS prepared_by_role TEXT NOT NULL DEFAULT 'CEO & Founder'`,
		`CREATE UNIQUE INDEX IF NOT EXISTS cotizador_quotes_folio_uidx ON cotizador_quotes(folio) WHERE folio IS NOT NULL`,

		// Secciones markdown ordenables que componen el cuerpo de la propuesta.
		`CREATE TABLE IF NOT EXISTS cotizador_quote_sections (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			quote_id UUID NOT NULL REFERENCES cotizador_quotes(id) ON DELETE CASCADE,
			section_key TEXT NOT NULL,
			title TEXT NOT NULL,
			content_md TEXT NOT NULL DEFAULT '',
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(quote_id, section_key)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quote_sections_quote ON cotizador_quote_sections(quote_id, sort_order)`,

		// === Cotizador commit 8: FTS spanish + plantillas pre-fabricadas ===
		// Migrar tsvector existente a 'spanish' para mejor lematización (acentos, plurales).
		`ALTER TABLE cotizador_quote_items DROP COLUMN IF EXISTS description_tsv`,
		`ALTER TABLE cotizador_quote_items ADD COLUMN description_tsv tsvector
		 GENERATED ALWAYS AS (to_tsvector('spanish', coalesce(description,''))) STORED`,
		`DROP INDEX IF EXISTS idx_cotizador_quote_items_fts`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_quote_items_fts ON cotizador_quote_items USING GIN (description_tsv)`,

		`ALTER TABLE cotizador_lessons DROP COLUMN IF EXISTS text_tsv`,
		`ALTER TABLE cotizador_lessons ADD COLUMN text_tsv tsvector
		 GENERATED ALWAYS AS (to_tsvector('spanish', coalesce(text,''))) STORED`,
		`DROP INDEX IF EXISTS idx_cotizador_lessons_fts`,
		`CREATE INDEX IF NOT EXISTS idx_cotizador_lessons_fts ON cotizador_lessons USING GIN (text_tsv)`,

		// Accent-insensitive helpers (ARIA Core busca con/sin acentos).
		// La extensión unaccent existe en Postgres core. Usamos function helper
		// porque to_tsvector con un text-search config custom requiere superuser.
		// La búsqueda hace unaccent(query) AND to_tsvector(spanish, unaccent(text)).
		`CREATE EXTENSION IF NOT EXISTS unaccent`,
		`CREATE TABLE IF NOT EXISTS cloud_project_sessions (
			project_name TEXT NOT NULL,
			session_id TEXT NOT NULL,
			indexed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (project_name, session_id)
		)`,
		`INSERT INTO cloud_project_sessions (project_name, session_id)
		 SELECT c.project_name, btrim(elem->>'id')
		 FROM cloud_chunks c,
		      jsonb_array_elements(COALESCE(c.payload->'sessions', '[]'::jsonb)) AS elem
		 WHERE btrim(COALESCE(elem->>'id', '')) <> ''
		 ON CONFLICT (project_name, session_id) DO NOTHING`,
		`CREATE TABLE IF NOT EXISTS cloud_project_controls (
		    project       TEXT PRIMARY KEY,
		    sync_enabled  BOOLEAN NOT NULL DEFAULT TRUE,
		    paused_reason TEXT,
		    updated_by    TEXT,
		    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cloud_project_controls_enabled ON cloud_project_controls(sync_enabled)`,
		// cloud_mutations: journal for fine-grained mutation sync (REQ-200, REQ-201).
		`CREATE TABLE IF NOT EXISTS cloud_mutations (
			seq        BIGSERIAL PRIMARY KEY,
			project    TEXT NOT NULL,
			entity     TEXT NOT NULL,
			entity_key TEXT NOT NULL,
			op         TEXT NOT NULL,
			payload    JSONB NOT NULL DEFAULT '{}',
			occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)`,
		`CREATE INDEX IF NOT EXISTS idx_cloud_mutations_project ON cloud_mutations(project)`,
		`CREATE INDEX IF NOT EXISTS idx_cloud_mutations_seq ON cloud_mutations(seq)`,
		// cloud_sync_audit_log: persistent audit trail for push-rejection events (REQ-400).
		`CREATE TABLE IF NOT EXISTS cloud_sync_audit_log (
			id           SERIAL PRIMARY KEY,
			occurred_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			contributor  TEXT NOT NULL,
			project      TEXT NOT NULL,
			action       TEXT NOT NULL,
			outcome      TEXT NOT NULL,
			entry_count  INT NOT NULL DEFAULT 0,
			reason_code  TEXT,
			metadata     JSONB
		)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_log_occurred_at ON cloud_sync_audit_log (occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_log_contributor_project ON cloud_sync_audit_log (contributor, project)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_log_outcome ON cloud_sync_audit_log (outcome)`,
	}
	for _, q := range queries {
		if _, err := cs.db.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("cloudstore: migrate: %w", err)
		}
	}
	if err := cs.backfillProjectSessionsFromChunks(ctx); err != nil {
		return err
	}
	return nil
}

// ─── Mutation Journal Queries ─────────────────────────────────────────────────

// MutationEntry mirrors cloudserver.MutationEntry to avoid a circular import.
type MutationEntry struct {
	Project   string          `json:"project"`
	Entity    string          `json:"entity"`
	EntityKey string          `json:"entity_key"`
	Op        string          `json:"op"`
	Payload   json.RawMessage `json:"payload"`
}

// StoredMutation mirrors cloudserver.StoredMutation to avoid a circular import.
type StoredMutation struct {
	Seq        int64           `json:"seq"`
	Project    string          `json:"project"`
	Entity     string          `json:"entity"`
	EntityKey  string          `json:"entity_key"`
	Op         string          `json:"op"`
	Payload    json.RawMessage `json:"payload"`
	OccurredAt string          `json:"occurred_at"`
}

// InsertMutationBatch inserts a batch of mutations into the cloud_mutations journal.
// Returns the sequence numbers assigned to each entry.
// BW3: The entire batch is wrapped in a transaction — partial failures roll back
// all prior entries so the client can retry the full batch without creating duplicates.
func (cs *CloudStore) InsertMutationBatch(ctx context.Context, batch []MutationEntry) ([]int64, error) {
	if cs == nil || cs.db == nil {
		return nil, fmt.Errorf("cloudstore: not initialized")
	}
	if len(batch) == 0 {
		return []int64{}, nil
	}

	tx, err := cs.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: begin mutation batch tx: %w", err)
	}
	// Ensure rollback on any error path.
	defer func() {
		if tx != nil {
			_ = tx.Rollback()
		}
	}()

	seqs := make([]int64, 0, len(batch))
	for _, entry := range batch {
		project := strings.TrimSpace(entry.Project)
		entity := strings.TrimSpace(entry.Entity)
		entityKey := strings.TrimSpace(entry.EntityKey)
		op := strings.TrimSpace(entry.Op)
		payload := entry.Payload
		if len(payload) == 0 {
			payload = json.RawMessage("{}")
		}
		var seq int64
		err := tx.QueryRowContext(ctx, `
			INSERT INTO cloud_mutations (project, entity, entity_key, op, payload)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING seq`,
			project, entity, entityKey, op, payload,
		).Scan(&seq)
		if err != nil {
			return nil, fmt.Errorf("cloudstore: insert mutation: %w", err)
		}
		seqs = append(seqs, seq)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("cloudstore: commit mutation batch: %w", err)
	}
	tx = nil // mark committed so deferred Rollback is a no-op
	return seqs, nil
}

// ListMutationsSince returns mutations with seq > sinceSeq, filtered to allowedProjects.
// If allowedProjects is nil, no project filter is applied (returns all).
// If allowedProjects is non-nil (even empty), only those projects are returned.
// Returns (mutations, hasMore, latestSeq, error).
func (cs *CloudStore) ListMutationsSince(ctx context.Context, sinceSeq int64, limit int, allowedProjects []string) ([]StoredMutation, bool, int64, error) {
	if cs == nil || cs.db == nil {
		return nil, false, 0, fmt.Errorf("cloudstore: not initialized")
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}

	// If allowedProjects is non-nil but empty, return empty result immediately.
	if allowedProjects != nil && len(allowedProjects) == 0 {
		return []StoredMutation{}, false, 0, nil
	}

	// Fetch limit+1 to detect hasMore.
	fetchLimit := limit + 1

	var rows *sql.Rows
	var err error

	if allowedProjects == nil {
		// No enrollment filter.
		rows, err = cs.db.QueryContext(ctx, `
			SELECT seq, project, entity, entity_key, op, payload::text, occurred_at
			FROM cloud_mutations
			WHERE seq > $1
			ORDER BY seq ASC
			LIMIT $2`,
			sinceSeq, fetchLimit,
		)
	} else {
		// Filter by allowed projects.
		rows, err = cs.db.QueryContext(ctx, `
			SELECT seq, project, entity, entity_key, op, payload::text, occurred_at
			FROM cloud_mutations
			WHERE seq > $1 AND project = ANY($2)
			ORDER BY seq ASC
			LIMIT $3`,
			sinceSeq, allowedProjects, fetchLimit,
		)
	}
	if err != nil {
		return nil, false, 0, fmt.Errorf("cloudstore: list mutations since %d: %w", sinceSeq, err)
	}
	defer rows.Close()

	var all []StoredMutation
	for rows.Next() {
		var m StoredMutation
		var payloadStr string
		var occurredAt time.Time
		if err := rows.Scan(&m.Seq, &m.Project, &m.Entity, &m.EntityKey, &m.Op, &payloadStr, &occurredAt); err != nil {
			return nil, false, 0, fmt.Errorf("cloudstore: scan mutation: %w", err)
		}
		m.Payload = json.RawMessage(payloadStr)
		m.OccurredAt = occurredAt.UTC().Format(time.RFC3339)
		all = append(all, m)
	}
	if err := rows.Err(); err != nil {
		return nil, false, 0, fmt.Errorf("cloudstore: iterate mutations: %w", err)
	}

	hasMore := len(all) > limit
	if hasMore {
		all = all[:limit]
	}

	latestSeq := int64(0)
	if len(all) > 0 {
		latestSeq = all[len(all)-1].Seq
	}

	return all, hasMore, latestSeq, nil
}

func parseClientCreatedAt(value string) (*time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, fmt.Errorf("cloudstore: invalid client_created_at: %w", err)
	}
	parsed = parsed.UTC()
	return &parsed, nil
}

func chunkIDFromPayload(payload []byte) string {
	return chunkcodec.ChunkID(payload)
}

func normalizeJSON(payload []byte) []byte {
	var body any
	if err := json.Unmarshal(payload, &body); err != nil {
		return payload
	}
	normalized, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return normalized
}

type chunkSummary struct {
	sessions     int
	observations int
	prompts      int
}

func summarizeChunk(payload []byte) chunkSummary {
	var body struct {
		Sessions     []json.RawMessage `json:"sessions"`
		Observations []json.RawMessage `json:"observations"`
		Prompts      []json.RawMessage `json:"prompts"`
	}
	if err := json.Unmarshal(payload, &body); err != nil {
		return chunkSummary{}
	}
	return chunkSummary{
		sessions:     len(body.Sessions),
		observations: len(body.Observations),
		prompts:      len(body.Prompts),
	}
}
