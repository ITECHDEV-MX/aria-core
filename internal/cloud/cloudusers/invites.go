package cloudusers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lib/pq"
)

// (fmt is used in error wrapping below)

// Invite represents a magic-link invite row.
type Invite struct {
	Token        string
	Email        string
	InvitedByUID sql.NullString
	Roles        []string
	ExpiresAt    time.Time
	UsedAt       sql.NullTime
	CreatedAt    time.Time
}

// IsUsable returns true if the invite is still valid (not used, not expired).
func (i *Invite) IsUsable(now time.Time) bool {
	if i == nil {
		return false
	}
	if i.UsedAt.Valid {
		return false
	}
	return now.Before(i.ExpiresAt)
}

var (
	// ErrInviteNotFound is returned when no invite matches the given token.
	ErrInviteNotFound = errors.New("invite not found")
	// ErrInviteExpired is returned when the invite expired or was already used.
	ErrInviteExpired = errors.New("invite expired or already used")
)

// CreateInvite inserts a new invite row and returns it. If invitedByUID is
// empty, the invited_by_uid column is left NULL.
func (s *Store) CreateInvite(ctx context.Context, email string, roles []string, invitedByUID string) (*Invite, error) {
	email = normalizeEmail(email)
	if email == "" {
		return nil, fmt.Errorf("email is required")
	}
	for _, r := range roles {
		if !ValidRole(r) {
			return nil, ErrInvalidRole
		}
	}
	if len(roles) == 0 {
		roles = []string{RoleDev}
	}
	byUID := strings.TrimSpace(invitedByUID)
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO cloud_invites (email, invited_by_uid, roles)
		VALUES ($1, NULLIF($2,'')::uuid, $3)
		RETURNING token::text, email, invited_by_uid::text, roles, expires_at, used_at, created_at
	`, email, byUID, pq.Array(roles))
	return scanInvite(row)
}

// GetInvite fetches an invite by token. Returns ErrInviteNotFound if no row.
func (s *Store) GetInvite(ctx context.Context, token string) (*Invite, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrInviteNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT token::text, email, invited_by_uid::text, roles, expires_at, used_at, created_at
		FROM cloud_invites WHERE token::text = $1
	`, token)
	inv, err := scanInvite(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	return inv, nil
}

// ConsumeInvite marks the invite used_at = NOW() and creates the user with the
// invite's email + roles + given password. Returns the created user.
// Idempotency: if the invite was already used or has expired, returns ErrInviteExpired.
func (s *Store) ConsumeInvite(ctx context.Context, token, password string) (*User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `
		SELECT token::text, email, invited_by_uid::text, roles, expires_at, used_at, created_at
		FROM cloud_invites WHERE token::text = $1 FOR UPDATE
	`, strings.TrimSpace(token))
	inv, err := scanInvite(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrInviteNotFound
		}
		return nil, err
	}
	if !inv.IsUsable(time.Now()) {
		return nil, ErrInviteExpired
	}

	if _, err := tx.ExecContext(ctx, `UPDATE cloud_invites SET used_at = NOW() WHERE token::text = $1`, inv.Token); err != nil {
		return nil, fmt.Errorf("mark invite used: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Create user outside the tx (Create has its own transaction).
	if len(inv.Roles) == 0 {
		inv.Roles = []string{RoleDev}
	}
	u, err := s.Create(ctx, inv.Email, "", inv.Roles[0], password)
	if err != nil {
		return nil, err
	}
	for _, extra := range inv.Roles[1:] {
		if err := s.AddRole(ctx, u.UID, extra); err != nil {
			return nil, fmt.Errorf("attach role %q: %w", extra, err)
		}
	}
	u.Roles = inv.Roles
	return u, nil
}

func scanInvite(s scanner) (*Invite, error) {
	var i Invite
	var roles pq.StringArray
	if err := s.Scan(&i.Token, &i.Email, &i.InvitedByUID, &roles, &i.ExpiresAt, &i.UsedAt, &i.CreatedAt); err != nil {
		return nil, err
	}
	i.Roles = []string(roles)
	return &i, nil
}
