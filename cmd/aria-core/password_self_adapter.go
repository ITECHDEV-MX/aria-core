package main

import (
	"context"
	"errors"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudusers"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
)

// passwordSelfAdapter implementa dashboard.PasswordSelfService envolviendo
// cloudusers.Store. Sin chain a través de cloudserver — directo, simple.
type passwordSelfAdapter struct {
	users *cloudusers.Store
}

func newPasswordSelfAdapter(users *cloudusers.Store) *passwordSelfAdapter {
	return &passwordSelfAdapter{users: users}
}

func (a *passwordSelfAdapter) VerifyAndChangePassword(ctx context.Context, uid, current, newPwd string) error {
	if a == nil || a.users == nil {
		return errors.New("password self-service not configured")
	}
	return a.users.VerifyAndChangePassword(ctx, uid, current, newPwd)
}

func (a *passwordSelfAdapter) CreatePasswordResetToken(ctx context.Context, emailAddr string) (string, time.Time, bool, error) {
	if a == nil || a.users == nil {
		return "", time.Time{}, false, errors.New("password self-service not configured")
	}
	return a.users.CreatePasswordResetToken(ctx, emailAddr)
}

func (a *passwordSelfAdapter) ConsumePasswordResetToken(ctx context.Context, token, newPwd string) (string, error) {
	if a == nil || a.users == nil {
		return "", errors.New("password self-service not configured")
	}
	return a.users.ConsumePasswordResetToken(ctx, token, newPwd)
}

// passwordResetMailerAdapter implementa dashboard.PasswordResetMailerService.
type passwordResetMailerAdapter struct {
	email     *email.Service
	publicURL string
}

func newPasswordResetMailerAdapter(emailSvc *email.Service, publicURL string) *passwordResetMailerAdapter {
	host := publicURL
	for {
		if i := indexOf(host, "://"); i >= 0 {
			host = host[i+3:]
			break
		}
		break
	}
	return &passwordResetMailerAdapter{email: emailSvc, publicURL: publicURL}
}

func (a *passwordResetMailerAdapter) PublicURL() string {
	if a == nil {
		return ""
	}
	return a.publicURL
}

func (a *passwordResetMailerAdapter) SendPasswordReset(ctx context.Context, emailAddr, link string) error {
	if a == nil || a.email == nil {
		return errors.New("email not configured")
	}
	host := a.publicURL
	if i := indexOf(host, "://"); i >= 0 {
		host = host[i+3:]
	}
	return a.email.SendPasswordReset(ctx, email.PasswordResetContext{
		Email:         emailAddr,
		Link:          link,
		DashboardHost: host,
	})
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
