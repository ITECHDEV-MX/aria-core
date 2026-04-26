package main

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudusers"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/email"
)

// emailServiceAdapter conecta internal/cloud/email.Service al contrato
// cloudserver.EmailService sin acoplar el cloudserver al paquete email.
type emailServiceAdapter struct {
	svc *email.Service
}

func newEmailServiceAdapter(svc *email.Service) *emailServiceAdapter {
	return &emailServiceAdapter{svc: svc}
}

func (a *emailServiceAdapter) IsConfigured() bool {
	return a != nil && a.svc != nil && a.svc.IsConfigured()
}

func (a *emailServiceAdapter) PublicURL() string {
	if a == nil || a.svc == nil {
		return ""
	}
	return a.svc.PublicURL()
}

func toEmailQuoteContext(qc cloudserver.EmailQuoteContext) email.QuoteContext {
	return email.QuoteContext{
		QuoteID:                 qc.QuoteID,
		Folio:                   qc.Folio,
		ProductName:             qc.ProductName,
		PreparedForCompany:      qc.PreparedForCompany,
		PreparedForContactName:  qc.PreparedForContactName,
		PreparedForContactEmail: qc.PreparedForContactEmail,
		Total:                   qc.Total,
		Currency:                qc.Currency,
		Status:                  qc.Status,
		ValidUntil:              qc.ValidUntil,
		PreparedByName:          qc.PreparedByName,
		PreparedByEmail:         qc.PreparedByEmail,
		Notes:                   qc.Notes,
		PublicURL:               qc.PublicURL,
	}
}

func (a *emailServiceAdapter) SendQuoteSent(ctx context.Context, qc cloudserver.EmailQuoteContext) error {
	if a == nil || a.svc == nil {
		return nil
	}
	return a.svc.SendQuoteSent(ctx, toEmailQuoteContext(qc))
}

func (a *emailServiceAdapter) SendQuoteApproved(ctx context.Context, qc cloudserver.EmailQuoteContext, bccCreator string) error {
	if a == nil || a.svc == nil {
		return nil
	}
	return a.svc.SendQuoteApproved(ctx, toEmailQuoteContext(qc), bccCreator)
}

func (a *emailServiceAdapter) SendQuoteRejected(ctx context.Context, qc cloudserver.EmailQuoteContext, creatorEmail string) error {
	if a == nil || a.svc == nil {
		return nil
	}
	return a.svc.SendQuoteRejected(ctx, toEmailQuoteContext(qc), creatorEmail)
}

func (a *emailServiceAdapter) SendQuoteExpiring(ctx context.Context, qc cloudserver.EmailQuoteContext, creatorEmail string) error {
	if a == nil || a.svc == nil {
		return nil
	}
	return a.svc.SendQuoteExpiring(ctx, toEmailQuoteContext(qc), creatorEmail)
}

func (a *emailServiceAdapter) SendInvite(ctx context.Context, ic cloudserver.EmailInviteContext) error {
	if a == nil || a.svc == nil {
		return nil
	}
	return a.svc.SendInvite(ctx, email.InviteContext{
		Email:     ic.Email,
		Link:      ic.Link,
		ExpiresAt: ic.ExpiresAt,
		InvitedBy: ic.InvitedBy,
		Roles:     ic.Roles,
	})
}

// inviteServiceAdapter conecta cloudusers.Store al contrato cloudserver.InviteService.
type inviteServiceAdapter struct {
	users *cloudusers.Store
}

func newInviteServiceAdapter(users *cloudusers.Store) *inviteServiceAdapter {
	return &inviteServiceAdapter{users: users}
}

func toInviteRecord(inv *cloudusers.Invite) *cloudserver.InviteRecord {
	if inv == nil {
		return nil
	}
	rec := &cloudserver.InviteRecord{
		Token:     inv.Token,
		Email:     inv.Email,
		Roles:     inv.Roles,
		ExpiresAt: inv.ExpiresAt,
	}
	if inv.UsedAt.Valid {
		t := inv.UsedAt.Time
		rec.UsedAt = &t
	}
	if inv.InvitedByUID.Valid {
		rec.InvitedByUID = inv.InvitedByUID.String
	}
	return rec
}

func (a *inviteServiceAdapter) CreateInvite(ctx context.Context, email string, roles []string, invitedByUID string) (*cloudserver.InviteRecord, error) {
	if a == nil || a.users == nil {
		return nil, errors.New("invite store not configured")
	}
	// Filter empty/invalid roles before passing to store.
	cleaned := make([]string, 0, len(roles))
	for _, r := range roles {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		cleaned = append(cleaned, r)
	}
	inv, err := a.users.CreateInvite(ctx, email, cleaned, invitedByUID)
	if err != nil {
		return nil, err
	}
	return toInviteRecord(inv), nil
}

func (a *inviteServiceAdapter) GetInvite(ctx context.Context, token string) (*cloudserver.InviteRecord, error) {
	if a == nil || a.users == nil {
		return nil, errors.New("invite store not configured")
	}
	inv, err := a.users.GetInvite(ctx, token)
	if err != nil {
		return nil, err
	}
	return toInviteRecord(inv), nil
}

func (a *inviteServiceAdapter) ConsumeInvite(ctx context.Context, token, password string) error {
	if a == nil || a.users == nil {
		return errors.New("invite store not configured")
	}
	if _, err := a.users.ConsumeInvite(ctx, token, password); err != nil {
		if errors.Is(err, cloudusers.ErrInviteExpired) || errors.Is(err, cloudusers.ErrInviteNotFound) {
			return cloudserver.ErrInviteExpired
		}
		return err
	}
	return nil
}

// (timing helper to suppress unused import "time" if needed by future signatures)
var _ = time.Now
