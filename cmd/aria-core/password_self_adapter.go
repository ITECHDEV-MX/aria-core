package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudusers"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboard"
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

// profileAdapter implementa dashboard.ProfileService.
type profileAdapter struct {
	users *cloudusers.Store
}

func newProfileAdapter(users *cloudusers.Store) *profileAdapter {
	return &profileAdapter{users: users}
}

func (a *profileAdapter) GetProfile(ctx context.Context, uid string) (*dashboard.UserProfileView, error) {
	if a == nil || a.users == nil {
		return nil, errors.New("profile not configured")
	}
	u, err := a.users.GetProfile(ctx, uid)
	if err != nil {
		return nil, err
	}
	view := &dashboard.UserProfileView{
		UID: u.UID, Email: u.Email, Name: u.Name,
		Phone: u.Phone, Timezone: u.Timezone, Language: u.Language,
		JobTitle: u.JobTitle, Bio: u.Bio, AvatarURL: u.AvatarURL,
		Roles: u.Roles, CreatedAt: u.CreatedAt,
	}
	if u.LastActiveAt.Valid {
		t := u.LastActiveAt.Time
		view.LastActive = &t
	}
	if len(u.Preferences) > 0 {
		var prefs map[string]any
		if err := json.Unmarshal(u.Preferences, &prefs); err == nil {
			view.Preferences = prefs
		}
	}
	return view, nil
}

func (a *profileAdapter) UpdateProfile(ctx context.Context, uid string, p dashboard.UserProfileUpdate) error {
	if a == nil || a.users == nil {
		return errors.New("profile not configured")
	}
	return a.users.UpdateProfile(ctx, uid, cloudusers.ProfileUpdate{
		Name: p.Name, Phone: p.Phone, Timezone: p.Timezone, Language: p.Language,
		JobTitle: p.JobTitle, Bio: p.Bio, AvatarURL: p.AvatarURL,
	})
}

func (a *profileAdapter) UpdatePreferences(ctx context.Context, uid string, prefsJSON []byte) error {
	if a == nil || a.users == nil {
		return errors.New("profile not configured")
	}
	return a.users.UpdatePreferences(ctx, uid, prefsJSON)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
