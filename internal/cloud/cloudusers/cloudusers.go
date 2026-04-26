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

	BcryptCost = 12
)

// AllRoles lista los roles soportados (orden = orden de aparición en UI).
var AllRoles = []string{RoleAdmin, RoleDev, RoleCotizador, RoleProjectAdmin}

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
	default:
		return role
	}
}

var (
	ErrNotFound          = errors.New("user not found")
	ErrInvalidCredential = errors.New("invalid credentials")
	ErrInactive          = errors.New("user is inactive")
	ErrEmailTaken        = errors.New("email already in use")
	ErrInvalidRole       = errors.New("invalid role (must be one of: admin, dev, cotizador, project_admin)")
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
