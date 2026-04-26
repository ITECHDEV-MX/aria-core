package main

import (
	"context"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
)

// welcomeMailerAdapter implementa dashboard.UserWelcomeMailer envolviendo
// email.Service. Renderiza welcome_user.html con login + guide URLs derivados
// del PublicURL configurado en el cliente Graph.
type welcomeMailerAdapter struct {
	email     *email.Service
	publicURL string
}

func newWelcomeMailerAdapter(emailSvc *email.Service, publicURL string) *welcomeMailerAdapter {
	return &welcomeMailerAdapter{email: emailSvc, publicURL: strings.TrimRight(strings.TrimSpace(publicURL), "/")}
}

func (a *welcomeMailerAdapter) SendWelcome(ctx context.Context, params dashboard.WelcomeParams) error {
	if a == nil || a.email == nil {
		return nil
	}
	base := a.publicURL
	if base == "" {
		base = "https://ariacore.itechdev.com.mx"
	}
	host := base
	if i := strings.Index(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	return a.email.SendWelcome(ctx, email.WelcomeContext{
		Name:          params.Name,
		Email:         params.Email,
		Password:      params.Password,
		Roles:         params.Roles,
		CreatedBy:     params.CreatedBy,
		LoginURL:      base + "/dashboard/login",
		GuideURL:      base + "/dashboard/ayuda",
		DashboardHost: host,
	})
}
