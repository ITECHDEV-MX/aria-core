package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudusers"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
)

// inviteDashboardAdapter implementa dashboard.InviteDashboardService —
// expone un wrapper en alto nivel sobre cloudusers + email.Service para
// el handler /dashboard/admin/users/invite.
type inviteDashboardAdapter struct {
	users     *cloudusers.Store
	email     *email.Service
	publicURL string
}

func newInviteDashboardAdapter(users *cloudusers.Store, emailSvc *email.Service, publicURL string) *inviteDashboardAdapter {
	return &inviteDashboardAdapter{users: users, email: emailSvc, publicURL: strings.TrimSpace(publicURL)}
}

func (a *inviteDashboardAdapter) CreateAndSend(ctx context.Context, emailAddr string, roles []string, invitedByUID, invitedByEmail string) (string, bool, string, error) {
	if a == nil || a.users == nil {
		return "", false, "", fmt.Errorf("invite store not configured")
	}
	emailAddr = strings.TrimSpace(strings.ToLower(emailAddr))
	if emailAddr == "" {
		return "", false, "", fmt.Errorf("email is required")
	}
	cleaned := make([]string, 0, len(roles))
	for _, r := range roles {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		cleaned = append(cleaned, r)
	}
	inv, err := a.users.CreateInvite(ctx, emailAddr, cleaned, invitedByUID)
	if err != nil {
		return "", false, "", err
	}
	publicURL := strings.TrimRight(a.publicURL, "/")
	if publicURL == "" {
		publicURL = "https://ariacore.itechdev.com.mx"
	}
	link := fmt.Sprintf("%s/dashboard/invite/%s", publicURL, inv.Token)

	if a.email == nil || !a.email.IsConfigured() {
		return link, false, "Email no configurado en el servidor. Copiá el link manualmente:", nil
	}
	expiresAt := inv.ExpiresAt.Format("2006-01-02 15:04 MST")
	if err := a.email.SendInvite(ctx, email.InviteContext{
		Email:     inv.Email,
		Link:      link,
		ExpiresAt: expiresAt,
		InvitedBy: invitedByEmail,
		Roles:     inv.Roles,
	}); err != nil {
		return link, false, fmt.Sprintf("Email falló: %v. Copiá el link manualmente:", err), nil
	}
	return link, true, "", nil
}
