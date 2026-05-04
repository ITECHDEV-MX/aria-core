package dashboard

import (
	"github.com/ITECHDEV-MX/aria-core/internal/obs"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// handleAccountSecurityPage — GET /dashboard/me/security: form de cambio de password.
func (h *handlers) handleAccountSecurityPage(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Redirect(w, r, "/dashboard/login?next=/dashboard/me/security", http.StatusSeeOther)
		return
	}
	component := AccountSecurityPage(p.DisplayName(), "", "")
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Mi cuenta · Seguridad", p.DisplayName(), "me", p.Roles(), component))
}

// handleAccountPasswordChange — POST /dashboard/me/security/password: self-service change.
func (h *handlers) handleAccountPasswordChange(w http.ResponseWriter, r *http.Request) {
	if h.cfg.PasswordSelf == nil {
		http.Error(w, "password self-service not configured", http.StatusServiceUnavailable)
		return
	}
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}
	uid := p.UID()
	if uid == "" {
		http.Error(w, "no UID in session", http.StatusUnauthorized)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	current := r.PostForm.Get("current_password")
	newPwd := r.PostForm.Get("new_password")
	confirm := r.PostForm.Get("confirm_password")
	if newPwd != confirm {
		renderComponent(w, r, AccountSecurityPage(p.DisplayName(), "Las contraseñas no coinciden.", ""))
		return
	}
	if err := h.cfg.PasswordSelf.VerifyAndChangePassword(r.Context(), uid, current, newPwd); err != nil {
		msg := err.Error()
		if strings.Contains(msg, "invalid credentials") || strings.Contains(msg, "ErrInvalidCredential") {
			msg = "La contraseña actual no es correcta."
		}
		renderComponent(w, r, AccountSecurityPage(p.DisplayName(), msg, ""))
		return
	}
	renderComponent(w, r, AccountSecurityPage(p.DisplayName(), "", "Contraseña actualizada correctamente."))
}

// handleForgotPasswordPage — GET /dashboard/forgot-password: form anónimo.
func (h *handlers) handleForgotPasswordPage(w http.ResponseWriter, r *http.Request) {
	renderComponent(w, r, ForgotPasswordPage(false))
}

// handleForgotPasswordSubmit — POST /dashboard/forgot-password: trigger email.
// Anti-enumeration: siempre retorna "submitted" aunque el email no exista.
func (h *handlers) handleForgotPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(r.PostForm.Get("email")))
	if email == "" {
		renderComponent(w, r, ForgotPasswordPage(true))
		return
	}
	if h.cfg.PasswordSelf == nil {
		obs.L().Info(fmt.Sprintf("forgot-password: PasswordSelf no configurado, ignorando email=%s", email))
		renderComponent(w, r, ForgotPasswordPage(true))
		return
	}
	token, _, found, err := h.cfg.PasswordSelf.CreatePasswordResetToken(r.Context(), email)
	if err != nil {
		obs.L().Info(fmt.Sprintf("forgot-password: error creating token for %s: %v", email, err))
		// Aún así retornamos submitted (anti-enumeration)
		renderComponent(w, r, ForgotPasswordPage(true))
		return
	}
	if found && h.cfg.PasswordResetMailer != nil {
		base := strings.TrimRight(h.publicURL(), "/")
		if base == "" {
			base = "https://ariacore.itechdev.com.mx"
		}
		link := base + "/dashboard/reset-password/" + token
		if err := h.cfg.PasswordResetMailer.SendPasswordReset(r.Context(), email, link); err != nil {
			obs.L().Info(fmt.Sprintf("forgot-password: send email failed for %s: %v", email, err))
		} else {
			obs.L().Info(fmt.Sprintf("forgot-password: reset link sent to %s", email))
		}
	}
	renderComponent(w, r, ForgotPasswordPage(true))
}

// handleResetPasswordPage — GET /dashboard/reset-password/{token}: form si token válido.
func (h *handlers) handleResetPasswordPage(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	renderComponent(w, r, ResetPasswordPage(token, "", false))
}

// handleResetPasswordSubmit — POST /dashboard/reset-password/{token}: consume + actualiza.
func (h *handlers) handleResetPasswordSubmit(w http.ResponseWriter, r *http.Request) {
	if h.cfg.PasswordSelf == nil {
		http.Error(w, "password self-service not configured", http.StatusServiceUnavailable)
		return
	}
	token := r.PathValue("token")
	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	newPwd := r.PostForm.Get("new_password")
	confirm := r.PostForm.Get("confirm_password")
	if newPwd != confirm {
		renderComponent(w, r, ResetPasswordPage(token, "Las contraseñas no coinciden.", false))
		return
	}
	if len(newPwd) < 8 {
		renderComponent(w, r, ResetPasswordPage(token, "La contraseña debe tener al menos 8 caracteres.", false))
		return
	}
	uid, err := h.cfg.PasswordSelf.ConsumePasswordResetToken(r.Context(), token, newPwd)
	if err != nil {
		msg := "El link es inválido o expiró. Pedí uno nuevo desde Olvidé mi contraseña."
		if !errors.Is(err, ErrPasswordResetTokenInvalid) {
			obs.L().Info(fmt.Sprintf("reset-password: consume token failed: %v", err))
		}
		renderComponent(w, r, ResetPasswordPage(token, msg, false))
		return
	}
	_ = uid
	// Successful reset → render success page con auto-redirect (script en template)
	renderComponent(w, r, ResetPasswordPage(token, "", true))
}

// publicURL returns the configured PublicURL or fallback.
func (h *handlers) publicURL() string {
	if h == nil || h.cfg.PasswordResetMailer == nil {
		return ""
	}
	type publicURLer interface {
		PublicURL() string
	}
	if p, ok := h.cfg.PasswordResetMailer.(publicURLer); ok {
		return p.PublicURL()
	}
	return ""
}

// ErrPasswordResetTokenInvalid centralizes the common error string for UI.
var ErrPasswordResetTokenInvalid = fmt.Errorf("token invalid or expired")
