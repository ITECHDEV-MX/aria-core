package email

import (
	"github.com/ITECHDEV-MX/aria-core/internal/obs"
	"context"
	"errors"
	"fmt"
	"strings"
)

// Service wraps the low-level Microsoft Graph client and exposes high-level
// methods for ARIA Core notifications (quote events + magic-link invites).
//
// All Send* methods are safe to call when the underlying client is not
// configured: they simply log a warning and return nil so callers (e.g.
// async hooks in cotizador) don't have to gate every call.
type Service struct {
	client *Client
}

// NewService builds a Service from a configured Client.
// If client is nil or not configured, the Service degrades to log-only mode.
func NewService(c *Client) *Service {
	return &Service{client: c}
}

// IsConfigured reports whether the underlying Graph client is fully wired up.
func (s *Service) IsConfigured() bool {
	return s != nil && s.client != nil && s.client.IsConfigured()
}

// PublicURL exposes the configured PublicURL for callers that need to build
// magic-link URLs even when email is disabled.
func (s *Service) PublicURL() string {
	if s == nil || s.client == nil {
		return ""
	}
	return s.client.PublicURL()
}

// QuoteContext carries the data needed to render quote-event emails.
type QuoteContext struct {
	QuoteID                 string
	Folio                   string
	ProductName             string
	PreparedForCompany      string
	PreparedForContactName  string
	PreparedForContactEmail string
	Total                   float64
	Currency                string
	Status                  string
	ValidUntil              string
	PreparedByName          string
	PreparedByEmail         string
	Notes                   string
	PublicURL               string
}

// InviteContext carries the data for the magic-link invite email.
type InviteContext struct {
	Email     string
	Link      string
	ExpiresAt string
	InvitedBy string
	Roles     []string
}

// WelcomeContext carries the data for the welcome email sent when an admin
// creates a user manually with a temporary password.
type WelcomeContext struct {
	Name          string
	Email         string
	Password      string   // temporary password — user should change after first login
	Roles         []string
	CreatedBy     string   // display name or email of the admin who created the user
	LoginURL      string   // full URL to the dashboard login page
	GuideURL      string   // full URL to /dashboard/ayuda
	DashboardHost string   // bare host (e.g. ariacore.itechdev.com.mx)
}

// SendQuoteSent dispatches the "propuesta enviada" email to the contact.
func (s *Service) SendQuoteSent(ctx context.Context, qc QuoteContext) error {
	return s.send(ctx, qc.PreparedForContactEmail, "[iTechDev] Propuesta enviada", "quote_sent", qc, nil)
}

// SendQuoteApproved dispatches the "propuesta aprobada" email and BCCs the creator.
func (s *Service) SendQuoteApproved(ctx context.Context, qc QuoteContext, bccCreator string) error {
	bcc := []string{}
	if strings.TrimSpace(bccCreator) != "" {
		bcc = []string{strings.TrimSpace(bccCreator)}
	}
	return s.send(ctx, qc.PreparedForContactEmail, "[iTechDev] Propuesta aprobada", "quote_approved", qc, bcc)
}

// SendQuoteRejected dispatches the "propuesta rechazada" email to the creator.
func (s *Service) SendQuoteRejected(ctx context.Context, qc QuoteContext, creatorEmail string) error {
	return s.send(ctx, creatorEmail, "[iTechDev] Propuesta rechazada", "quote_rejected", qc, nil)
}

// SendQuoteExpiring dispatches the "vence pronto" warning to the creator.
func (s *Service) SendQuoteExpiring(ctx context.Context, qc QuoteContext, creatorEmail string) error {
	return s.send(ctx, creatorEmail, "[iTechDev] Propuesta vence pronto", "quote_expiring", qc, nil)
}

// SendInvite dispatches the magic-link invite email.
func (s *Service) SendInvite(ctx context.Context, ic InviteContext) error {
	return s.send(ctx, ic.Email, "[iTechDev] Invitación a ARIA Core", "magic_link", ic, nil)
}

// PasswordResetContext es lo que usa el template password_reset.html.
type PasswordResetContext struct {
	Email         string
	Link          string
	DashboardHost string
}

// SendPasswordReset dispatches the magic-link reset password email.
func (s *Service) SendPasswordReset(ctx context.Context, rc PasswordResetContext) error {
	return s.send(ctx, rc.Email, "[iTechDev] Restablecer contraseña — ARIA Core", "password_reset", rc, nil)
}

// SendWelcome dispatches the welcome email when an admin creates a user with
// a temporary password (manual-create flow, not magic-link). Includes the
// password in clear (one-time, user must change after first login per copy
// in the template) and a 5-step onboarding guide.
func (s *Service) SendWelcome(ctx context.Context, wc WelcomeContext) error {
	return s.send(ctx, wc.Email, "[iTechDev] Bienvenido a ARIA Core — tu cuenta + guía", "welcome_user", wc, nil)
}

// SendRawHTML dispatches a manually-prepared HTML email. Used by quote-chat
// where the operator builds + reviews + confirms the body before send. CC is
// translated to BCC so the recipient does not see the cc list (matches
// internal use-case).
func (s *Service) SendRawHTML(ctx context.Context, to string, bcc []string, subject, htmlBody string) error {
	if s == nil || s.client == nil || !s.client.IsConfigured() {
		obs.L().Info(fmt.Sprintf("email: SKIP raw send (not configured) to=%s subject=%q", to, subject))
		return ErrNotConfigured
	}
	if strings.TrimSpace(to) == "" {
		return fmt.Errorf("email: recipient is required")
	}
	if strings.TrimSpace(subject) == "" {
		return fmt.Errorf("email: subject is required")
	}
	if strings.TrimSpace(htmlBody) == "" {
		return fmt.Errorf("email: body is required")
	}
	return s.client.SendMailBCC(ctx, to, bcc, subject, htmlBody, "")
}

// send is the internal helper that renders a template and dispatches via Graph.
// When the client is not configured, it logs at INFO and returns nil so callers
// can continue (per spec: "los hooks loggean info y siguen").
func (s *Service) send(ctx context.Context, to, subject, templateName string, data any, bcc []string) error {
	if s == nil || s.client == nil || !s.client.IsConfigured() {
		obs.L().Info(fmt.Sprintf("email: SKIP send (not configured) to=%s subject=%q template=%s", to, subject, templateName))
		return nil
	}
	if strings.TrimSpace(to) == "" {
		obs.L().Info(fmt.Sprintf("email: SKIP send (empty recipient) subject=%q template=%s", subject, templateName))
		return nil
	}
	htmlBody, err := s.client.Render(templateName, data)
	if err != nil {
		return fmt.Errorf("email service: render: %w", err)
	}
	if len(bcc) > 0 {
		if err := s.client.SendMailBCC(ctx, to, bcc, subject, htmlBody, ""); err != nil {
			if errors.Is(err, ErrNotConfigured) {
				return nil
			}
			return err
		}
		return nil
	}
	if err := s.client.SendMail(ctx, to, subject, htmlBody, ""); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return nil
		}
		return err
	}
	return nil
}
