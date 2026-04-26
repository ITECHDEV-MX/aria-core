package main

import (
	"context"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudusers"
)

// dashboardUserAdapter conecta el cloudusers.Store con el contrato cloudserver.DashboardUserService.
type dashboardUserAdapter struct {
	store *cloudusers.Store
}

func newDashboardUserAdapter(cs *cloudstore.CloudStore) *dashboardUserAdapter {
	return &dashboardUserAdapter{store: cloudusers.New(cs.DB())}
}

func (a *dashboardUserAdapter) VerifyPassword(ctx context.Context, email, password string) (*cloudserver.UserPrincipal, error) {
	u, err := a.store.VerifyPassword(ctx, email, password)
	if err != nil {
		return nil, err
	}
	return toPrincipal(u), nil
}

func (a *dashboardUserAdapter) GetByUID(ctx context.Context, uid string) (*cloudserver.UserPrincipal, error) {
	u, err := a.store.GetByUID(ctx, uid)
	if err != nil {
		return nil, err
	}
	return toPrincipal(u), nil
}

func (a *dashboardUserAdapter) List(ctx context.Context) ([]*cloudserver.UserPrincipal, error) {
	users, err := a.store.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*cloudserver.UserPrincipal, 0, len(users))
	for _, u := range users {
		out = append(out, toPrincipal(u))
	}
	return out, nil
}

func (a *dashboardUserAdapter) Create(ctx context.Context, email, name, role, password string) (*cloudserver.UserPrincipal, error) {
	u, err := a.store.Create(ctx, email, name, role, password)
	if err != nil {
		return nil, err
	}
	return toPrincipal(u), nil
}

func (a *dashboardUserAdapter) SetRole(ctx context.Context, uid, role string) error {
	return a.store.SetRole(ctx, uid, role)
}

func (a *dashboardUserAdapter) SetActive(ctx context.Context, uid string, active bool) error {
	return a.store.SetActive(ctx, uid, active)
}

func (a *dashboardUserAdapter) ChangePassword(ctx context.Context, uid, newPassword string) error {
	return a.store.ChangePassword(ctx, uid, newPassword)
}

func toPrincipal(u *cloudusers.User) *cloudserver.UserPrincipal {
	return &cloudserver.UserPrincipal{
		UID:       u.UID,
		Email:     u.Email,
		Name:      u.Name,
		Role:      u.Role,
		IsActive:  u.IsActive,
		CreatedAt: u.CreatedAt,
	}
}
