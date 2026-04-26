// Package vault implementa la bóveda de secretos de ARIA Core.
//
// Diseño:
//   - Encryption-at-rest AES-256-GCM con per-cliente HKDF derivation.
//   - ACL via aria_secret_grants (uid o role; con expiry).
//   - Audit log en aria_secret_access_log para cada acción (read/rotate/use_in_cmd/denied).
//   - Master key vía env ARIA_CORE_VAULT_MASTER_KEY (32 bytes hex). Empty → degraded mode.
//
// API: Create / GetMetadata / Reveal / List / Rotate / Delete / Grant / Revoke / AccessLog / CanAccess.
package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Errores públicos del store.
var (
	ErrNotFound     = errors.New("vault: secret not found")
	ErrForbidden    = errors.New("vault: forbidden")
	ErrInvalidInput = errors.New("vault: invalid input")
	ErrConflict     = errors.New("vault: conflict (duplicate active name)")
	ErrDegraded     = errors.New("vault: in degraded mode (no master key configured)")
)

// Permisos canónicos.
const (
	PermRead   = "read"
	PermRotate = "rotate"
	PermDelete = "delete"
)

// Categorías permitidas por el CHECK constraint.
var validCategories = map[string]struct{}{
	"db_password": {}, "api_token": {}, "ssh_key": {}, "cert": {},
	"env": {}, "webhook": {}, "generic": {},
}

// Scopes permitidos por el CHECK constraint del schema.
var validScopes = map[string]struct{}{
	"personal": {}, "project": {}, "team": {}, "client_knowledge": {},
}

// Rotation policies aceptadas.
var validRotationPolicies = map[string]struct{}{
	"manual": {}, "30d": {}, "90d": {}, "180d": {}, "never": {},
}

// Secret es la representación pública de un secret (sin valor descifrado).
// IMPORTANTE: NUNCA incluir ciphertext, nonce o key_id en estructuras
// que crucen una API hacia el dev/Claude — solo metadata.
type Secret struct {
	ID             string
	Name           string
	Category       string
	Scope          string
	Project        string
	ClientID       *uuid.UUID
	Description    string
	Metadata       map[string]any
	ExpiresAt      *time.Time
	RotationPolicy string
	CreatedAt      time.Time
	CreatedByUID   string
	IsActive       bool
}

// AccessEntry es una fila del audit log.
type AccessEntry struct {
	ID            string
	SecretID      string
	AccessedByUID string
	Action        string
	ClientIP      string
	UserAgent     string
	Reason        string
	CommandHash   string
	AccessedAt    time.Time
}

// CreateParams es el input de Store.Create.
type CreateParams struct {
	Name           string
	Category       string
	Scope          string
	Project        string
	ClientID       *uuid.UUID
	Description    string
	Value          string // plaintext — se cifra antes de persistir
	Metadata       map[string]any
	ExpiresAt      *time.Time
	RotationPolicy string
	CreatedByUID   string
}

// GrantParams es el input de Store.Grant.
type GrantParams struct {
	SecretID      string
	GrantedToUID  string // uno de los dos
	GrantedToRole string // (al menos uno)
	Permission    string // read | rotate | delete
	GrantedByUID  string
	ExpiresAt     *time.Time
}

// ListFilter parametriza Store.List.
type ListFilter struct {
	Project   string
	ClientID  *uuid.UUID
	Category  string
	Scope     string
	Limit     int
	OnlyOwned bool // true = solo creados por byUID
}

// Principal es la identidad del caller para CanAccess.
type Principal struct {
	UID   string
	Roles []string // e.g. ["admin","dev"]
}

// HasRole verifica si el principal tiene un rol específico.
func (p Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if strings.EqualFold(r, role) {
			return true
		}
	}
	return false
}

// IsAdmin verifica si el principal tiene rol admin.
func (p Principal) IsAdmin() bool {
	return p.HasRole("admin")
}

// Store es la API pública del vault. Implementada por *PgStore.
type Store interface {
	Create(ctx context.Context, params CreateParams) (*Secret, error)
	GetMetadata(ctx context.Context, id string) (*Secret, error)
	Reveal(ctx context.Context, id string, by Principal, reason string) (string, error)
	List(ctx context.Context, filter ListFilter, by Principal) ([]*Secret, error)
	Rotate(ctx context.Context, id, newValue string, by Principal) (*Secret, error)
	Delete(ctx context.Context, id string, by Principal) error
	Grant(ctx context.Context, params GrantParams) error
	Revoke(ctx context.Context, grantID string, by Principal) error
	AccessLog(ctx context.Context, secretID string, limit int) ([]AccessEntry, error)
	CanAccess(ctx context.Context, secretID string, by Principal, perm string) (bool, error)
	Available() bool // false → degraded mode (no master key)
}

// PgStore es la implementación Postgres del Store.
type PgStore struct {
	db     *sql.DB
	crypto *Crypto
}

// New construye el store.
func New(db *sql.DB, crypto *Crypto) *PgStore {
	return &PgStore{db: db, crypto: crypto}
}

// Available retorna si el master key está configurado (encrypt/decrypt funcionan).
func (s *PgStore) Available() bool {
	return s.crypto != nil && s.crypto.Available()
}

// DBRaw expone el sql.DB subyacente para queries puntuales (e.g. global audit log
// que joinea con aria_secrets para resolver names en el dashboard).
func (s *PgStore) DBRaw() *sql.DB {
	if s == nil {
		return nil
	}
	return s.db
}

// Create cifra y persiste un nuevo secret.
func (s *PgStore) Create(ctx context.Context, p CreateParams) (*Secret, error) {
	if !s.Available() {
		return nil, ErrDegraded
	}
	if err := validateCreate(&p); err != nil {
		return nil, err
	}
	id := uuid.NewString()

	ct, nonce, keyID, err := s.crypto.EncryptSecret(p.Value, id, p.Name, p.ClientID)
	if err != nil {
		return nil, err
	}
	metaJSON, err := json.Marshal(p.Metadata)
	if err != nil {
		return nil, fmt.Errorf("vault: marshal metadata: %w", err)
	}
	if string(metaJSON) == "null" {
		metaJSON = []byte("{}")
	}
	policy := p.RotationPolicy
	if policy == "" {
		policy = "manual"
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `
		INSERT INTO aria_secrets
		  (id, name, category, scope, project, client_id, description,
		   ciphertext, nonce, key_id, metadata, expires_at, rotation_policy,
		   is_active, created_by_uid)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,NULLIF($7,''),
		        $8,$9,$10,$11::jsonb,$12,$13,
		        TRUE, $14::uuid)
	`,
		id, p.Name, p.Category, p.Scope, p.Project, nullableUUID(p.ClientID), p.Description,
		ct, nonce, keyID, string(metaJSON), p.ExpiresAt, policy,
		p.CreatedByUID,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrConflict
		}
		return nil, fmt.Errorf("vault: insert secret: %w", err)
	}

	if err := logAccessTx(ctx, tx, id, p.CreatedByUID, "create", "", "", "create", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMetadata(ctx, id)
}

// GetMetadata retorna metadata del secret (sin valor).
func (s *PgStore) GetMetadata(ctx context.Context, id string) (*Secret, error) {
	row := s.db.QueryRowContext(ctx, secretSelectCols+`
		FROM aria_secrets WHERE id = $1::uuid`, id)
	sec, err := scanSecret(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return sec, nil
}

// Reveal descifra y retorna el plaintext. Logea siempre el acceso (incluso denied).
func (s *PgStore) Reveal(ctx context.Context, id string, by Principal, reason string) (string, error) {
	if !s.Available() {
		return "", ErrDegraded
	}
	allowed, err := s.CanAccess(ctx, id, by, PermRead)
	if err != nil {
		return "", err
	}
	if !allowed {
		_ = s.logAccess(ctx, id, by.UID, "denied", reason, "")
		return "", ErrForbidden
	}

	var ct, nonce []byte
	var name, keyID string
	err = s.db.QueryRowContext(ctx, `
		SELECT ciphertext, nonce, key_id, name FROM aria_secrets WHERE id = $1::uuid
	`, id).Scan(&ct, &nonce, &keyID, &name)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("vault: load ciphertext: %w", err)
	}
	pt, err := s.crypto.DecryptSecret(ct, nonce, id, name, keyID)
	if err != nil {
		return "", err
	}
	if err := s.logAccess(ctx, id, by.UID, "read", reason, ""); err != nil {
		return "", err
	}
	return pt, nil
}

// List retorna metadata de secrets visibles para el principal según filtros.
// Visibilidad: admin ve todo. Otros ven los que crearon + los con grant explícito.
func (s *PgStore) List(ctx context.Context, f ListFilter, by Principal) ([]*Secret, error) {
	conds := []string{"is_active = TRUE"}
	args := []any{}
	idx := 1

	if !by.IsAdmin() {
		// uid puede ver: created_by_uid = uid OR existe grant para uid OR para alguno de sus roles.
		conds = append(conds, fmt.Sprintf(`(
			created_by_uid = $%d::uuid
			OR EXISTS (
				SELECT 1 FROM aria_secret_grants g
				WHERE g.secret_id = aria_secrets.id
				  AND (g.granted_to_uid = $%d::uuid
				       OR g.granted_to_role = ANY($%d))
				  AND (g.expires_at IS NULL OR g.expires_at > NOW())
			)
		)`, idx, idx, idx+1))
		args = append(args, by.UID, by.Roles)
		idx += 2
	}
	if f.OnlyOwned {
		conds = append(conds, fmt.Sprintf("created_by_uid = $%d::uuid", idx))
		args = append(args, by.UID)
		idx++
	}
	if f.Project != "" {
		conds = append(conds, fmt.Sprintf("project = $%d", idx))
		args = append(args, f.Project)
		idx++
	}
	if f.ClientID != nil {
		conds = append(conds, fmt.Sprintf("client_id = $%d::uuid", idx))
		args = append(args, f.ClientID.String())
		idx++
	}
	if f.Category != "" {
		conds = append(conds, fmt.Sprintf("category = $%d", idx))
		args = append(args, f.Category)
		idx++
	}
	if f.Scope != "" {
		conds = append(conds, fmt.Sprintf("scope = $%d", idx))
		args = append(args, f.Scope)
		idx++
	}
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	q := secretSelectCols + " FROM aria_secrets WHERE " + strings.Join(conds, " AND ") +
		" ORDER BY created_at DESC LIMIT $" + fmt.Sprint(idx)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("vault: list: %w", err)
	}
	defer rows.Close()

	var out []*Secret
	for rows.Next() {
		sec, err := scanSecret(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// Rotate marca el viejo como is_active=FALSE/superseded_by=NEW e inserta uno nuevo
// con el mismo nombre/categoria/scope/project/client_id.
func (s *PgStore) Rotate(ctx context.Context, id, newValue string, by Principal) (*Secret, error) {
	if !s.Available() {
		return nil, ErrDegraded
	}
	allowed, err := s.CanAccess(ctx, id, by, PermRotate)
	if err != nil {
		return nil, err
	}
	if !allowed {
		_ = s.logAccess(ctx, id, by.UID, "denied", "rotate", "")
		return nil, ErrForbidden
	}

	old, err := s.GetMetadata(ctx, id)
	if err != nil {
		return nil, err
	}

	newID := uuid.NewString()
	ct, nonce, keyID, err := s.crypto.EncryptSecret(newValue, newID, old.Name, old.ClientID)
	if err != nil {
		return nil, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Marcar el viejo como inactivo. El unique index parcial WHERE is_active permite
	// que coexistan: viejo (is_active=FALSE) + nuevo (is_active=TRUE) con mismo name.
	if _, err := tx.ExecContext(ctx, `
		UPDATE aria_secrets SET is_active = FALSE, superseded_by = $1::uuid
		WHERE id = $2::uuid
	`, newID, id); err != nil {
		return nil, fmt.Errorf("vault: deactivate old: %w", err)
	}

	metaJSON, _ := json.Marshal(old.Metadata)
	if string(metaJSON) == "null" {
		metaJSON = []byte("{}")
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO aria_secrets
		  (id, name, category, scope, project, client_id, description,
		   ciphertext, nonce, key_id, metadata, expires_at, rotation_policy,
		   is_active, created_by_uid)
		VALUES ($1,$2,$3,$4,NULLIF($5,''),$6,NULLIF($7,''),
		        $8,$9,$10,$11::jsonb,$12,$13,
		        TRUE, $14::uuid)
	`,
		newID, old.Name, old.Category, old.Scope, old.Project, nullableUUID(old.ClientID), old.Description,
		ct, nonce, keyID, string(metaJSON), old.ExpiresAt, old.RotationPolicy, old.CreatedByUID,
	); err != nil {
		return nil, fmt.Errorf("vault: insert rotated: %w", err)
	}
	if err := logAccessTx(ctx, tx, id, by.UID, "rotate", "", "", "rotated", ""); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMetadata(ctx, newID)
}

// Delete soft-delete: marca is_active=FALSE.
func (s *PgStore) Delete(ctx context.Context, id string, by Principal) error {
	allowed, err := s.CanAccess(ctx, id, by, PermDelete)
	if err != nil {
		return err
	}
	if !allowed {
		_ = s.logAccess(ctx, id, by.UID, "denied", "delete", "")
		return ErrForbidden
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE aria_secrets SET is_active = FALSE WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("vault: delete: %w", err)
	}
	return s.logAccess(ctx, id, by.UID, "delete", "", "")
}

// Grant crea un grant ACL.
func (s *PgStore) Grant(ctx context.Context, p GrantParams) error {
	if strings.TrimSpace(p.SecretID) == "" {
		return fmt.Errorf("%w: secret_id required", ErrInvalidInput)
	}
	if _, ok := map[string]struct{}{PermRead: {}, PermRotate: {}, PermDelete: {}}[p.Permission]; !ok {
		return fmt.Errorf("%w: permission must be read|rotate|delete", ErrInvalidInput)
	}
	if strings.TrimSpace(p.GrantedToUID) == "" && strings.TrimSpace(p.GrantedToRole) == "" {
		return fmt.Errorf("%w: granted_to_uid or granted_to_role required", ErrInvalidInput)
	}
	if strings.TrimSpace(p.GrantedByUID) == "" {
		return fmt.Errorf("%w: granted_by_uid required", ErrInvalidInput)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO aria_secret_grants
		  (secret_id, granted_to_uid, granted_to_role, permission, granted_by_uid, expires_at)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, NULLIF($3,''), $4, $5::uuid, $6)
	`,
		p.SecretID, p.GrantedToUID, p.GrantedToRole, p.Permission, p.GrantedByUID, p.ExpiresAt,
	); err != nil {
		return fmt.Errorf("vault: insert grant: %w", err)
	}
	if err := logAccessTx(ctx, tx, p.SecretID, p.GrantedByUID, "grant", "",
		fmt.Sprintf("perm=%s,to_uid=%s,to_role=%s", p.Permission, p.GrantedToUID, p.GrantedToRole), "grant", ""); err != nil {
		return err
	}
	return tx.Commit()
}

// Revoke borra un grant.
func (s *PgStore) Revoke(ctx context.Context, grantID string, by Principal) error {
	var secretID string
	err := s.db.QueryRowContext(ctx, `SELECT secret_id::text FROM aria_secret_grants WHERE id = $1::uuid`, grantID).Scan(&secretID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	// Sólo creator del secret o admin pueden revocar.
	allowed, err := s.CanAccess(ctx, secretID, by, PermDelete)
	if err != nil {
		return err
	}
	if !allowed {
		_ = s.logAccess(ctx, secretID, by.UID, "denied", "revoke", "")
		return ErrForbidden
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM aria_secret_grants WHERE id = $1::uuid`, grantID); err != nil {
		return fmt.Errorf("vault: delete grant: %w", err)
	}
	return s.logAccess(ctx, secretID, by.UID, "revoke", grantID, "")
}

// AccessLog retorna las últimas N entradas de log para un secret.
func (s *PgStore) AccessLog(ctx context.Context, secretID string, limit int) ([]AccessEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, secret_id::text, accessed_by_uid::text, action,
		       COALESCE(client_ip::text,''), COALESCE(user_agent,''),
		       COALESCE(reason,''), COALESCE(command_hash,''), accessed_at
		FROM aria_secret_access_log
		WHERE secret_id = $1::uuid
		ORDER BY accessed_at DESC
		LIMIT $2
	`, secretID, limit)
	if err != nil {
		return nil, fmt.Errorf("vault: access log: %w", err)
	}
	defer rows.Close()
	var out []AccessEntry
	for rows.Next() {
		var e AccessEntry
		if err := rows.Scan(&e.ID, &e.SecretID, &e.AccessedByUID, &e.Action, &e.ClientIP, &e.UserAgent, &e.Reason, &e.CommandHash, &e.AccessedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// CanAccess evalúa el ACL:
//
//  1. Admin always allow.
//  2. Creator always allow.
//  3. Grant explícito (uid o role) con expiry válido.
//  4. Default: deny.
func (s *PgStore) CanAccess(ctx context.Context, secretID string, by Principal, perm string) (bool, error) {
	if by.IsAdmin() {
		return true, nil
	}
	if strings.TrimSpace(by.UID) == "" {
		return false, nil
	}

	var creatorUID string
	var scope string
	var clientIDStr sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT created_by_uid::text, scope, client_id::text
		FROM aria_secrets WHERE id = $1::uuid
	`, secretID).Scan(&creatorUID, &scope, &clientIDStr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, err
	}
	if creatorUID == by.UID {
		return true, nil
	}

	// Grants explícitos. Buscamos cualquier grant válido con perm >= solicitado.
	// Para simplificar: read es el básico; rotate/delete requieren grant exactamente con esos perms
	// (read no implica rotate/delete; rotate/delete no implican read pero aquí sí, semánticamente
	// quien puede borrar también puede leer).
	q := `
		SELECT 1 FROM aria_secret_grants
		WHERE secret_id = $1::uuid
		  AND (expires_at IS NULL OR expires_at > NOW())
		  AND (granted_to_uid = $2::uuid OR granted_to_role = ANY($3))
		  AND permission = $4
		LIMIT 1`
	var ok int
	err = s.db.QueryRowContext(ctx, q, secretID, by.UID, by.Roles, perm).Scan(&ok)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	// Para read: aceptar también grants de rotate o delete (permisos superiores).
	if perm == PermRead {
		err = s.db.QueryRowContext(ctx, `
			SELECT 1 FROM aria_secret_grants
			WHERE secret_id = $1::uuid
			  AND (expires_at IS NULL OR expires_at > NOW())
			  AND (granted_to_uid = $2::uuid OR granted_to_role = ANY($3))
			  AND permission IN ('rotate','delete')
			LIMIT 1`, secretID, by.UID, by.Roles).Scan(&ok)
		if err == nil {
			return true, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
	}
	// Scope-specific rule: client_knowledge requiere grant explícito siempre.
	// Para project: no hay project-role table, así que cae a default deny.
	_ = scope
	return false, nil
}

// LogUseInCmd es un helper público para registrar el uso de uno o más secrets
// dentro de un comando ejecutado por aria_vault_use_in_cmd. cmdHash es SHA256
// del comando entero. reason describe el motivo.
func (s *PgStore) LogUseInCmd(ctx context.Context, secretID, byUID, cmdHash, reason string) error {
	return s.logAccess(ctx, secretID, byUID, "use_in_cmd", reason, cmdHash)
}

// ─── helpers ──────────────────────────────────────────────────────────────

func validateCreate(p *CreateParams) error {
	if strings.TrimSpace(p.Name) == "" {
		return fmt.Errorf("%w: name required", ErrInvalidInput)
	}
	if _, ok := validCategories[p.Category]; !ok {
		return fmt.Errorf("%w: category must be one of db_password|api_token|ssh_key|cert|env|webhook|generic", ErrInvalidInput)
	}
	if _, ok := validScopes[p.Scope]; !ok {
		return fmt.Errorf("%w: scope must be one of personal|project|team|client_knowledge", ErrInvalidInput)
	}
	if p.RotationPolicy != "" {
		if _, ok := validRotationPolicies[p.RotationPolicy]; !ok {
			return fmt.Errorf("%w: rotation_policy must be manual|30d|90d|180d|never", ErrInvalidInput)
		}
	}
	if strings.TrimSpace(p.Value) == "" {
		return fmt.Errorf("%w: value required", ErrInvalidInput)
	}
	if strings.TrimSpace(p.CreatedByUID) == "" {
		return fmt.Errorf("%w: created_by_uid required", ErrInvalidInput)
	}
	if p.Scope == "client_knowledge" && p.ClientID == nil {
		return fmt.Errorf("%w: client_id required for scope=client_knowledge", ErrInvalidInput)
	}
	return nil
}

const secretSelectCols = `SELECT id::text, name, category, scope,
		COALESCE(project,''), client_id::text,
		COALESCE(description,''),
		COALESCE(metadata::text,'{}'),
		expires_at, COALESCE(rotation_policy,'manual'),
		created_at, created_by_uid::text, is_active`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanSecret(r rowScanner) (*Secret, error) {
	var s Secret
	var clientIDStr sql.NullString
	var metaStr string
	var expiresAt sql.NullTime
	if err := r.Scan(
		&s.ID, &s.Name, &s.Category, &s.Scope,
		&s.Project, &clientIDStr,
		&s.Description, &metaStr,
		&expiresAt, &s.RotationPolicy,
		&s.CreatedAt, &s.CreatedByUID, &s.IsActive,
	); err != nil {
		return nil, err
	}
	if clientIDStr.Valid && clientIDStr.String != "" {
		if id, err := uuid.Parse(clientIDStr.String); err == nil {
			s.ClientID = &id
		}
	}
	if expiresAt.Valid {
		t := expiresAt.Time
		s.ExpiresAt = &t
	}
	if metaStr == "" {
		metaStr = "{}"
	}
	_ = json.Unmarshal([]byte(metaStr), &s.Metadata)
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}
	return &s, nil
}

func nullableUUID(u *uuid.UUID) any {
	if u == nil {
		return nil
	}
	return u.String()
}

func (s *PgStore) logAccess(ctx context.Context, secretID, byUID, action, reason, cmdHash string) error {
	return logAccessExec(ctx, s.db, secretID, byUID, action, "", "", reason, cmdHash)
}

func logAccessTx(ctx context.Context, tx *sql.Tx, secretID, byUID, action, clientIP, userAgent, reason, cmdHash string) error {
	return logAccessExec(ctx, tx, secretID, byUID, action, clientIP, userAgent, reason, cmdHash)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func logAccessExec(ctx context.Context, ex execer, secretID, byUID, action, clientIP, userAgent, reason, cmdHash string) error {
	_, err := ex.ExecContext(ctx, `
		INSERT INTO aria_secret_access_log
		  (secret_id, accessed_by_uid, action, client_ip, user_agent, reason, command_hash)
		VALUES ($1::uuid, NULLIF($2,'')::uuid, $3, NULLIF($4,'')::inet, NULLIF($5,''), NULLIF($6,''), NULLIF($7,''))
	`, secretID, byUID, action, clientIP, userAgent, reason, cmdHash)
	if err != nil {
		return fmt.Errorf("vault: log access: %w", err)
	}
	return nil
}

// isUniqueViolation checks for Postgres unique-violation 23505.
// Implementado sin importar pgconn directo para evitar coupling — la firma del
// error de pgx incluye SQLSTATE 23505 en el string.
func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "23505") || strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
