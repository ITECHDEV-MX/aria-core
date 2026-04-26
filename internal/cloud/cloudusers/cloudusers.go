// Package cloudusers gestiona usuarios del dashboard ARIA Core (admin/dev).
package cloudusers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	RoleAdmin        = "admin"
	RoleDev          = "dev"
	RoleCotizador    = "cotizador"
	RoleProjectAdmin = "project_admin"
	// RoleAgent es para asistentes IA (ej. Z.A.R.A., secretaria de JC vía Hermes).
	// Tiene los mismos permisos de escritura que admin + capacidad de leer
	// scope=personal de otros usuarios (cross-personal read). Mantiene bloqueo
	// del redactor para sensitivity=confidential. NO puede crear/borrar usuarios
	// (eso queda solo en admin para evitar escalada de privilegios).
	RoleAgent = "agent"

	BcryptCost = 12
)

// AllRoles lista los roles soportados (orden = orden de aparición en UI).
var AllRoles = []string{RoleAdmin, RoleDev, RoleCotizador, RoleProjectAdmin, RoleAgent}

// RoleLabel retorna el display name de un role.
func RoleLabel(role string) string {
	switch role {
	case RoleAdmin:
		return "Admin"
	case RoleDev:
		return "Dev"
	case RoleCotizador:
		return "Cotizador (Ventas)"
	case RoleProjectAdmin:
		return "Administrador Proyectos"
	case RoleAgent:
		return "Agente IA (asistente)"
	default:
		return role
	}
}

var (
	ErrNotFound          = errors.New("user not found")
	ErrInvalidCredential = errors.New("invalid credentials")
	ErrInactive          = errors.New("user is inactive")
	ErrEmailTaken        = errors.New("email already in use")
	ErrInvalidRole       = errors.New("invalid role (must be one of: admin, dev, cotizador, project_admin, agent)")
)

type User struct {
	UID          string
	Email        string
	Name         string
	Role         string // legacy single-role (compat); usar Roles para multi-role.
	Roles        []string
	IsActive     bool
	ClientID     sql.NullString
	PasswordHash string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	// Profile fields (self-service)
	Phone        string
	Timezone     string
	Language     string
	JobTitle     string
	Bio          string
	AvatarURL    string
	Preferences  []byte // raw JSON
	LastActiveAt sql.NullTime
}

// ProfileUpdate carries the editable profile fields for self-service.
type ProfileUpdate struct {
	Name      string
	Phone     string
	Timezone  string
	Language  string
	JobTitle  string
	Bio       string
	AvatarURL string
}

// HasRole retorna true si el usuario tiene el rol dado en su set.
func (u *User) HasRole(role string) bool {
	for _, r := range u.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// HasAnyRole retorna true si el usuario tiene al menos uno de los roles dados.
func (u *User) HasAnyRole(roles ...string) bool {
	for _, want := range roles {
		if u.HasRole(want) {
			return true
		}
	}
	return false
}

type Store struct {
	db *sql.DB
}

func New(db *sql.DB) *Store {
	return &Store{db: db}
}

func ValidRole(role string) bool {
	for _, r := range AllRoles {
		if r == role {
			return true
		}
	}
	return false
}

func normalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// Create inserta un nuevo usuario con un rol inicial.
// Para asignar más roles después, usar AddRole.
func (s *Store) Create(ctx context.Context, email, name, role, password string) (*User, error) {
	email = normalizeEmail(email)
	name = strings.TrimSpace(name)
	role = strings.TrimSpace(role)
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	if !ValidRole(role) {
		return nil, ErrInvalidRole
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	if err != nil {
		return nil, fmt.Errorf("hash password: %w", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	username := email
	row := tx.QueryRowContext(ctx, `
		INSERT INTO cloud_users (username, email, name, role, password_hash, is_active)
		VALUES ($1, $2, $3, $4, $5, TRUE)
		RETURNING uid::text, email, name, role, is_active, client_id, password_hash, created_at, updated_at
	`, username, email, name, role, string(hash))
	u, err := scanUser(row)
	if err != nil {
		if isUniqueViolation(err) {
			return nil, ErrEmailTaken
		}
		return nil, fmt.Errorf("insert user: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cloud_user_roles (uid, role) VALUES ($1, $2) ON CONFLICT DO NOTHING`, u.UID, role); err != nil {
		return nil, fmt.Errorf("insert role: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	u.Roles = []string{role}
	return u, nil
}

func (s *Store) GetByEmail(ctx context.Context, email string) (*User, error) {
	email = normalizeEmail(email)
	row := s.db.QueryRowContext(ctx, `
		SELECT uid::text, email, name, role, is_active, client_id, password_hash, created_at, updated_at
		FROM cloud_users WHERE lower(email) = $1 LIMIT 1
	`, email)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := s.attachRoles(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

func (s *Store) GetByUID(ctx context.Context, uid string) (*User, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return nil, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT uid::text, email, name, role, is_active, client_id, password_hash, created_at, updated_at
		FROM cloud_users WHERE uid::text = $1 LIMIT 1
	`, uid)
	u, err := scanUser(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if err := s.attachRoles(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// attachRoles carga los roles del usuario desde cloud_user_roles.
func (s *Store) attachRoles(ctx context.Context, u *User) error {
	rows, err := s.db.QueryContext(ctx, `SELECT role FROM cloud_user_roles WHERE uid::text = $1 ORDER BY role`, u.UID)
	if err != nil {
		return fmt.Errorf("load roles: %w", err)
	}
	defer rows.Close()
	u.Roles = nil
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return err
		}
		u.Roles = append(u.Roles, r)
	}
	return rows.Err()
}

// AddRole asigna un rol adicional al usuario. Idempotente.
func (s *Store) AddRole(ctx context.Context, uid, role string) error {
	if !ValidRole(role) {
		return ErrInvalidRole
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO cloud_user_roles (uid, role) VALUES ($1, $2) ON CONFLICT DO NOTHING`, uid, role)
	return err
}

// RemoveRole quita un rol del usuario. Si era el único rol, error.
func (s *Store) RemoveRole(ctx context.Context, uid, role string) error {
	if !ValidRole(role) {
		return ErrInvalidRole
	}
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM cloud_user_roles WHERE uid::text = $1`, uid).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return fmt.Errorf("cannot remove last role from user (assign another role first)")
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM cloud_user_roles WHERE uid::text = $1 AND role = $2`, uid, role)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("user does not have role %q", role)
	}
	return nil
}

// ListRoles retorna los roles asignados al usuario.
func (s *Store) ListRoles(ctx context.Context, uid string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT role FROM cloud_user_roles WHERE uid::text = $1 ORDER BY role`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// VerifyPassword devuelve el usuario si email/password coinciden y está activo.
func (s *Store) VerifyPassword(ctx context.Context, email, password string) (*User, error) {
	u, err := s.GetByEmail(ctx, email)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			_ = bcrypt.CompareHashAndPassword([]byte("$2a$12$dummy.hash.for.timing.attack.protection.padding"), []byte(password))
			return nil, ErrInvalidCredential
		}
		return nil, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredential
	}
	if !u.IsActive {
		return nil, ErrInactive
	}
	return u, nil
}

func (s *Store) List(ctx context.Context) ([]*User, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT uid::text, email, name, role, is_active, client_id, password_hash, created_at, updated_at
		FROM cloud_users ORDER BY created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, u := range out {
		if err := s.attachRoles(ctx, u); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) SetRole(ctx context.Context, uid, role string) error {
	if !ValidRole(role) {
		return ErrInvalidRole
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cloud_users SET role = $1, updated_at = NOW() WHERE uid::text = $2
	`, role, uid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetActive(ctx context.Context, uid string, active bool) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE cloud_users SET is_active = $1, updated_at = NOW() WHERE uid::text = $2
	`, active, uid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) ChangePassword(ctx context.Context, uid, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), BcryptCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cloud_users SET password_hash = $1, updated_at = NOW() WHERE uid::text = $2
	`, string(hash), uid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// VerifyAndChangePassword verifica el password actual antes de cambiarlo.
// Usado por self-service: el dev debe probar conocimiento del password viejo.
// Retorna ErrInvalidCredential si el current no matchea.
func (s *Store) VerifyAndChangePassword(ctx context.Context, uid, currentPassword, newPassword string) error {
	if strings.TrimSpace(currentPassword) == "" {
		return fmt.Errorf("current password required")
	}
	if len(newPassword) < 8 {
		return fmt.Errorf("new password must be at least 8 characters")
	}
	if currentPassword == newPassword {
		return fmt.Errorf("new password must differ from current")
	}
	// Cargar hash actual
	var hash string
	err := s.db.QueryRowContext(ctx, `SELECT password_hash FROM cloud_users WHERE uid::text = $1`, uid).Scan(&hash)
	if err != nil {
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		return err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(currentPassword)); err != nil {
		return ErrInvalidCredential
	}
	return s.ChangePassword(ctx, uid, newPassword)
}

// CreatePasswordResetToken crea un token magic-link tipo password_reset
// asociado al email dado, válido 1h. Retorna (token, expiresAt, error).
// Si no hay user con ese email, NO retorna error (anti-enumeration) pero
// tampoco crea el token — el caller debe enviar email genérico.
func (s *Store) CreatePasswordResetToken(ctx context.Context, email string) (token string, expiresAt time.Time, found bool, err error) {
	email = strings.TrimSpace(strings.ToLower(email))
	if email == "" {
		return "", time.Time{}, false, fmt.Errorf("email is required")
	}
	// Verificar que el usuario existe
	var uid string
	err = s.db.QueryRowContext(ctx, `SELECT uid::text FROM cloud_users WHERE lower(email) = $1 AND is_active = TRUE`, email).Scan(&uid)
	if err == sql.ErrNoRows {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	// Crear token con expires=1h
	expires := time.Now().UTC().Add(1 * time.Hour)
	err = s.db.QueryRowContext(ctx, `
		INSERT INTO cloud_invites (email, expires_at, type)
		VALUES ($1, $2, 'password_reset')
		RETURNING token::text
	`, email, expires).Scan(&token)
	if err != nil {
		return "", time.Time{}, false, fmt.Errorf("create reset token: %w", err)
	}
	return token, expires, true, nil
}

// ConsumePasswordResetToken valida el token (no expirado, no usado, type=password_reset),
// updatea el password del user matcheado por email, y marca el token como usado.
func (s *Store) ConsumePasswordResetToken(ctx context.Context, token, newPassword string) (uid string, err error) {
	if len(newPassword) < 8 {
		return "", fmt.Errorf("password must be at least 8 characters")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var email string
	var expires time.Time
	var usedAt sql.NullTime
	var typ string
	err = tx.QueryRowContext(ctx, `
		SELECT email, expires_at, used_at, type FROM cloud_invites
		WHERE token::text = $1 FOR UPDATE
	`, token).Scan(&email, &expires, &usedAt, &typ)
	if err == sql.ErrNoRows {
		return "", ErrInviteNotFound
	}
	if err != nil {
		return "", err
	}
	if typ != "password_reset" {
		return "", fmt.Errorf("invalid token type")
	}
	if usedAt.Valid {
		return "", ErrInviteExpired
	}
	if time.Now().UTC().After(expires) {
		return "", ErrInviteExpired
	}
	// Actualizar password
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), BcryptCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `
		UPDATE cloud_users SET password_hash = $1, updated_at = NOW()
		WHERE lower(email) = $2 RETURNING uid::text
	`, string(hash), strings.ToLower(email)).Scan(&uid); err != nil {
		if err == sql.ErrNoRows {
			return "", ErrNotFound
		}
		return "", err
	}
	// Marcar token usado
	if _, err := tx.ExecContext(ctx, `UPDATE cloud_invites SET used_at = NOW() WHERE token::text = $1`, token); err != nil {
		return "", err
	}
	return uid, tx.Commit()
}

// GetProfile carga User con TODOS los campos de perfil (incluye phone, bio, etc).
// El path "normal" (GetByUID) solo trae los core fields; este es para /dashboard/me/profile.
func (s *Store) GetProfile(ctx context.Context, uid string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT uid::text, email, name, role, is_active, client_id, password_hash, created_at, updated_at,
		       COALESCE(phone,''), COALESCE(timezone,'America/Mexico_City'), COALESCE(language,'es'),
		       COALESCE(job_title,''), COALESCE(bio,''), COALESCE(avatar_url,''),
		       COALESCE(preferences::text, '{}'), last_active_at
		FROM cloud_users WHERE uid::text = $1
	`, uid)
	var u User
	var prefs string
	err := row.Scan(&u.UID, &u.Email, &u.Name, &u.Role, &u.IsActive, &u.ClientID, &u.PasswordHash,
		&u.CreatedAt, &u.UpdatedAt, &u.Phone, &u.Timezone, &u.Language, &u.JobTitle, &u.Bio, &u.AvatarURL,
		&prefs, &u.LastActiveAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u.Preferences = []byte(prefs)
	// Cargar roles
	roles, err := s.ListRoles(ctx, u.UID)
	if err == nil {
		u.Roles = roles
	}
	return &u, nil
}

// UpdateProfile actualiza los campos editables de perfil del propio usuario.
// Solo el dueño del UID puede llamar esto (verifica el caller).
func (s *Store) UpdateProfile(ctx context.Context, uid string, p ProfileUpdate) error {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return fmt.Errorf("name is required")
	}
	tz := strings.TrimSpace(p.Timezone)
	if tz == "" {
		tz = "America/Mexico_City"
	}
	lang := strings.TrimSpace(p.Language)
	if lang == "" {
		lang = "es"
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cloud_users
		SET name = $1,
		    phone = NULLIF($2,''),
		    timezone = $3,
		    language = $4,
		    job_title = NULLIF($5,''),
		    bio = NULLIF($6,''),
		    avatar_url = NULLIF($7,''),
		    updated_at = NOW()
		WHERE uid::text = $8
	`, name, strings.TrimSpace(p.Phone), tz, lang, strings.TrimSpace(p.JobTitle), strings.TrimSpace(p.Bio), strings.TrimSpace(p.AvatarURL), uid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdatePreferences actualiza el JSONB de preferencias (notification toggles, etc).
// Recibe el JSON completo. Caller responsable de validar shape.
func (s *Store) UpdatePreferences(ctx context.Context, uid string, prefsJSON []byte) error {
	if len(prefsJSON) == 0 {
		prefsJSON = []byte(`{}`)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE cloud_users SET preferences = $1::jsonb, updated_at = NOW()
		WHERE uid::text = $2
	`, string(prefsJSON), uid)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkActive actualiza last_active_at del user (llamado en cada login/operation).
func (s *Store) MarkActive(ctx context.Context, uid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE cloud_users SET last_active_at = NOW() WHERE uid::text = $1`, uid)
	return err
}

// Count devuelve total de usuarios (útil para detectar bootstrap inicial).
func (s *Store) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM cloud_users`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanUser(s scanner) (*User, error) {
	var u User
	err := s.Scan(&u.UID, &u.Email, &u.Name, &u.Role, &u.IsActive, &u.ClientID, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &u, nil
}

func isUniqueViolation(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint") || strings.Contains(msg, "23505")
}
