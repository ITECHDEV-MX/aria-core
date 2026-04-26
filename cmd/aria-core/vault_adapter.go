package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/vault"
	"github.com/google/uuid"
)

// vaultAdapter envuelve vault.PgStore exponiendo:
//   - cloudserver.VaultService (HTTP /v1/vault/*)
//   - dashboard.VaultDashboardService (admin UI)
type vaultAdapter struct {
	store *vault.PgStore
}

func newVaultAdapter(cs *cloudstore.CloudStore, masterKeyHex string) (*vaultAdapter, error) {
	c, err := vault.NewCrypto(masterKeyHex)
	if err != nil {
		return nil, fmt.Errorf("vault: init crypto: %w", err)
	}
	return &vaultAdapter{store: vault.New(cs.DB(), c)}, nil
}

// ─── cloudserver.VaultService implementation ────────────────────────────────

func (a *vaultAdapter) Create(ctx context.Context, p vault.CreateParams) (*vault.Secret, error) {
	return a.store.Create(ctx, p)
}
func (a *vaultAdapter) GetMetadata(ctx context.Context, id string) (*vault.Secret, error) {
	return a.store.GetMetadata(ctx, id)
}
func (a *vaultAdapter) Reveal(ctx context.Context, id string, by vault.Principal, reason string) (string, error) {
	return a.store.Reveal(ctx, id, by, reason)
}
func (a *vaultAdapter) List(ctx context.Context, f vault.ListFilter, by vault.Principal) ([]*vault.Secret, error) {
	return a.store.List(ctx, f, by)
}
func (a *vaultAdapter) Rotate(ctx context.Context, id, newValue string, by vault.Principal) (*vault.Secret, error) {
	return a.store.Rotate(ctx, id, newValue, by)
}
func (a *vaultAdapter) Delete(ctx context.Context, id string, by vault.Principal) error {
	return a.store.Delete(ctx, id, by)
}
func (a *vaultAdapter) Grant(ctx context.Context, p vault.GrantParams) error {
	return a.store.Grant(ctx, p)
}
func (a *vaultAdapter) Revoke(ctx context.Context, grantID string, by vault.Principal) error {
	return a.store.Revoke(ctx, grantID, by)
}
func (a *vaultAdapter) AccessLog(ctx context.Context, secretID string, limit int) ([]vault.AccessEntry, error) {
	return a.store.AccessLog(ctx, secretID, limit)
}
func (a *vaultAdapter) Available() bool { return a.store.Available() }

// ─── dashboard.VaultDashboardService implementation ─────────────────────────

func (a *vaultAdapter) ListD(ctx context.Context, ownerUID string, onlyOwned bool) ([]dashboard.VaultSecretView, error) {
	by := dashAdminPrincipal(ownerUID)
	rs, err := a.store.List(ctx, vault.ListFilter{OnlyOwned: onlyOwned, Limit: 200}, by)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.VaultSecretView, 0, len(rs))
	for _, sec := range rs {
		out = append(out, secretToDashView(sec))
	}
	return out, nil
}

func (a *vaultAdapter) CreateD(ctx context.Context, name, category, scope, project, clientID, description, value, byUID string) (string, error) {
	var cid *uuid.UUID
	if v := strings.TrimSpace(clientID); v != "" {
		u, err := uuid.Parse(v)
		if err != nil {
			return "", fmt.Errorf("invalid client_id: %w", err)
		}
		cid = &u
	}
	sec, err := a.store.Create(ctx, vault.CreateParams{
		Name: name, Category: category, Scope: scope, Project: project,
		ClientID: cid, Description: description, Value: value, CreatedByUID: byUID,
	})
	if err != nil {
		return "", err
	}
	return sec.ID, nil
}

func (a *vaultAdapter) RevealD(ctx context.Context, id, byUID, reason string) (string, error) {
	return a.store.Reveal(ctx, id, dashAdminPrincipal(byUID), reason)
}

func (a *vaultAdapter) RotateD(ctx context.Context, id, newValue, byUID string) error {
	_, err := a.store.Rotate(ctx, id, newValue, dashAdminPrincipal(byUID))
	return err
}

func (a *vaultAdapter) DeleteD(ctx context.Context, id, byUID string) error {
	return a.store.Delete(ctx, id, dashAdminPrincipal(byUID))
}

func (a *vaultAdapter) AccessLogD(ctx context.Context, secretID string, limit int) ([]dashboard.VaultAccessEntryView, error) {
	rs, err := a.store.AccessLog(ctx, secretID, limit)
	if err != nil {
		return nil, err
	}
	out := make([]dashboard.VaultAccessEntryView, 0, len(rs))
	// Resolver name del secret una sola vez para todas las filas.
	var name string
	if sec, err := a.store.GetMetadata(ctx, secretID); err == nil && sec != nil {
		name = sec.Name
	}
	for _, e := range rs {
		out = append(out, accessEntryToDashView(e, name))
	}
	return out, nil
}

func (a *vaultAdapter) GlobalAuditLogD(ctx context.Context, limit, offset int) ([]dashboard.VaultAccessEntryView, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := a.store.DBRaw().QueryContext(ctx, `
		SELECT l.id::text, l.secret_id::text, COALESCE(s.name,''),
		       l.accessed_by_uid::text, l.action,
		       COALESCE(l.reason,''), COALESCE(l.command_hash,''), l.accessed_at
		FROM aria_secret_access_log l
		LEFT JOIN aria_secrets s ON s.id = l.secret_id
		ORDER BY l.accessed_at DESC
		LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("vault: global audit query: %w", err)
	}
	defer rows.Close()
	var out []dashboard.VaultAccessEntryView
	for rows.Next() {
		var v dashboard.VaultAccessEntryView
		var t time.Time
		if err := rows.Scan(&v.ID, &v.SecretID, &v.SecretName, &v.AccessedByUID, &v.Action, &v.Reason, &v.CommandHash, &t); err != nil {
			return nil, err
		}
		v.AccessedAt = t
		out = append(out, v)
	}
	return out, rows.Err()
}

// AvailableD wraps store.Available() — separado para no chocar con
// VaultService.Available al cumplir ambas interfaces.
func (a *vaultAdapter) AvailableD() bool { return a.store.Available() }

// ─── helpers ────────────────────────────────────────────────────────────────

// dashAdminPrincipal: en el dashboard ya gateamos por requireAdmin, así que
// tratamos al user como principal con role admin para que pase los ACL del store.
// Esto es consistente porque el cloudserver dashboard mount usa requireAdmin
// en todas las rutas /dashboard/vault/*.
func dashAdminPrincipal(uid string) vault.Principal {
	return vault.Principal{UID: uid, Roles: []string{"admin"}}
}

func secretToDashView(sec *vault.Secret) dashboard.VaultSecretView {
	v := dashboard.VaultSecretView{
		ID: sec.ID, Name: sec.Name, Category: sec.Category, Scope: sec.Scope,
		Project: sec.Project, Description: sec.Description,
		RotationPolicy: sec.RotationPolicy, ExpiresAt: sec.ExpiresAt,
		CreatedAt: sec.CreatedAt, CreatedByUID: sec.CreatedByUID, IsActive: sec.IsActive,
	}
	if sec.ClientID != nil {
		v.ClientID = sec.ClientID.String()
	}
	return v
}

func accessEntryToDashView(e vault.AccessEntry, name string) dashboard.VaultAccessEntryView {
	return dashboard.VaultAccessEntryView{
		ID: e.ID, SecretID: e.SecretID, SecretName: name,
		AccessedByUID: e.AccessedByUID, Action: e.Action,
		Reason: e.Reason, CommandHash: e.CommandHash, AccessedAt: e.AccessedAt,
	}
}

// Compile-time interface assertions.
var (
	_ cloudserver.VaultService = (*vaultAdapter)(nil)
)

// dashboardVaultAdapter renombra los métodos *D para satisfacer la interface
// del dashboard (List/Create/Reveal/Rotate/Delete/AccessLog/GlobalAuditLog/Available).
type dashboardVaultAdapter struct {
	a *vaultAdapter
}

func (d dashboardVaultAdapter) List(ctx context.Context, ownerUID string, onlyOwned bool) ([]dashboard.VaultSecretView, error) {
	return d.a.ListD(ctx, ownerUID, onlyOwned)
}
func (d dashboardVaultAdapter) Create(ctx context.Context, name, category, scope, project, clientID, description, value, byUID string) (string, error) {
	return d.a.CreateD(ctx, name, category, scope, project, clientID, description, value, byUID)
}
func (d dashboardVaultAdapter) Reveal(ctx context.Context, id, byUID, reason string) (string, error) {
	return d.a.RevealD(ctx, id, byUID, reason)
}
func (d dashboardVaultAdapter) Rotate(ctx context.Context, id, newValue, byUID string) error {
	return d.a.RotateD(ctx, id, newValue, byUID)
}
func (d dashboardVaultAdapter) Delete(ctx context.Context, id, byUID string) error {
	return d.a.DeleteD(ctx, id, byUID)
}
func (d dashboardVaultAdapter) AccessLog(ctx context.Context, secretID string, limit int) ([]dashboard.VaultAccessEntryView, error) {
	return d.a.AccessLogD(ctx, secretID, limit)
}
func (d dashboardVaultAdapter) GlobalAuditLog(ctx context.Context, limit, offset int) ([]dashboard.VaultAccessEntryView, error) {
	return d.a.GlobalAuditLogD(ctx, limit, offset)
}
func (d dashboardVaultAdapter) Available() bool { return d.a.AvailableD() }

var _ dashboard.VaultDashboardService = dashboardVaultAdapter{}
