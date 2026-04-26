package cloudserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"
)

const defaultPublicURL = "https://ariacore.itechdev.com.mx"

// publicURLOrDefault returns the configured public URL or the default
// production URL if none was set.
func (s *CloudServer) publicURLOrDefault() string {
	u := strings.TrimSpace(s.publicURL)
	if u != "" {
		return u
	}
	if s.email != nil && strings.TrimSpace(s.email.PublicURL()) != "" {
		return strings.TrimSpace(s.email.PublicURL())
	}
	return defaultPublicURL
}

// inviteRequest is the body of POST /v1/admin/invites.
type inviteRequest struct {
	Email string   `json:"email"`
	Roles []string `json:"roles"`
}

type inviteResponse struct {
	Token     string    `json:"token"`
	Email     string    `json:"email"`
	Roles     []string  `json:"roles"`
	ExpiresAt time.Time `json:"expires_at"`
	Link      string    `json:"link"`
	EmailSent bool      `json:"email_sent"`
	EmailWarn string    `json:"email_warn,omitempty"`
}

// handleV1AdminInviteCreate POST /v1/admin/invites.
// Auth: admin role required (wired via withJWTRole).
func (s *CloudServer) handleV1AdminInviteCreate(w http.ResponseWriter, r *http.Request) {
	if s.invites == nil {
		http.Error(w, `{"error":"invite module not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req inviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(strings.ToLower(req.Email))
	if email == "" {
		http.Error(w, `{"error":"email is required"}`, http.StatusBadRequest)
		return
	}
	roles := normalizeInviteRoles(req.Roles)
	if len(roles) == 0 {
		http.Error(w, `{"error":"at least one role is required"}`, http.StatusBadRequest)
		return
	}
	claims, _ := claimsFromContext(r.Context())
	byUID := ""
	invitedBy := ""
	if claims != nil {
		byUID = claims.UID
		invitedBy = claims.Email
	}
	inv, err := s.invites.CreateInvite(r.Context(), email, roles, byUID)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	link := fmt.Sprintf("%s/dashboard/invite/%s", strings.TrimRight(s.publicURLOrDefault(), "/"), inv.Token)
	resp := inviteResponse{
		Token:     inv.Token,
		Email:     inv.Email,
		Roles:     inv.Roles,
		ExpiresAt: inv.ExpiresAt,
		Link:      link,
		EmailSent: false,
	}
	if s.email != nil && s.email.IsConfigured() {
		err := s.email.SendInvite(r.Context(), EmailInviteContext{
			Email:     inv.Email,
			Link:      link,
			ExpiresAt: inv.ExpiresAt.Format("2006-01-02 15:04 MST"),
			InvitedBy: invitedBy,
			Roles:     inv.Roles,
		})
		if err != nil {
			resp.EmailWarn = err.Error()
		} else {
			resp.EmailSent = true
		}
	} else {
		resp.EmailWarn = "email service not configured; copy the link above and send it manually"
	}
	jsonResponse(w, http.StatusOK, resp)
}

// handleDashboardInviteGet GET /dashboard/invite/{token} — sin auth.
// Renderea un form simple con HTML inline (no usa templ para evitar acoplamiento extra).
func (s *CloudServer) handleDashboardInviteGet(w http.ResponseWriter, r *http.Request) {
	if s.invites == nil {
		http.Error(w, "invite module not configured", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	inv, err := s.invites.GetInvite(r.Context(), token)
	if err != nil {
		renderInviteError(w, "Invitación no encontrada o ya consumida.")
		return
	}
	if !inv.IsUsable(time.Now()) {
		renderInviteError(w, "Esta invitación ya fue usada o expiró.")
		return
	}
	renderInviteForm(w, token, inv.Email, inv.Roles)
}

// handleDashboardInviteAccept POST /dashboard/invite/{token}/accept.
func (s *CloudServer) handleDashboardInviteAccept(w http.ResponseWriter, r *http.Request) {
	if s.invites == nil {
		http.Error(w, "invite module not configured", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		http.Error(w, "token is required", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	password := r.PostForm.Get("password")
	if len(password) < 8 {
		renderInviteError(w, "La contraseña debe tener al menos 8 caracteres.")
		return
	}
	if err := s.invites.ConsumeInvite(r.Context(), token, password); err != nil {
		msg := "No se pudo activar la cuenta."
		if errors.Is(err, ErrInviteExpired) {
			msg = "La invitación expiró o ya fue usada."
		} else {
			msg = msg + " " + err.Error()
		}
		renderInviteError(w, msg)
		return
	}
	http.Redirect(w, r, "/dashboard/login?activated=1", http.StatusSeeOther)
}

// ErrInviteExpired signals a consumed/expired magic link. Adapters in cmd/
// translate cloudusers.ErrInviteExpired/ErrInviteNotFound into this sentinel
// so the cloudserver layer doesn't import cloudusers types.
var ErrInviteExpired = errors.New("invite expired or already used")

func normalizeInviteRoles(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, r := range in {
		r = strings.TrimSpace(strings.ToLower(r))
		if r == "" {
			continue
		}
		if _, ok := seen[r]; ok {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out
}

func renderInviteError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	out := strings.ReplaceAll(inviteErrorHTML, "{{MSG}}", html.EscapeString(msg))
	_, _ = w.Write([]byte(out))
}

func renderInviteForm(w http.ResponseWriter, token, email string, roles []string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	rolesStr := strings.Join(roles, ", ")
	out := inviteFormHTML
	out = strings.ReplaceAll(out, "{{EMAIL}}", html.EscapeString(email))
	out = strings.ReplaceAll(out, "{{ROLES}}", html.EscapeString(rolesStr))
	out = strings.ReplaceAll(out, "{{TOKEN}}", html.EscapeString(token))
	_, _ = w.Write([]byte(out))
}

const inviteFormHTML = `<!doctype html>
<html lang="es"><head><meta charset="utf-8"><title>Activar cuenta — ARIA Core</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#0b1933;color:#e7eefa;margin:0;padding:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{background:#11203c;border:1px solid #1f3157;border-radius:8px;padding:32px;width:420px;max-width:92vw;box-shadow:0 8px 24px rgba(0,0,0,0.3)}
h1{margin:0 0 4px 0;font-size:20px}
p.muted{color:#9aa6c7;font-size:13px;margin:0 0 24px 0}
label{display:block;font-size:13px;margin:14px 0 6px 0;color:#cdd6ec}
input[type=password]{width:100%;box-sizing:border-box;padding:10px;background:#0b1933;border:1px solid #2a3d6a;color:#e7eefa;border-radius:4px;font-size:14px}
button{margin-top:18px;width:100%;padding:12px;background:#2563eb;color:#fff;border:none;border-radius:4px;font-size:14px;font-weight:600;cursor:pointer}
button:hover{background:#1d4ed8}
.email{background:#0b1933;border-radius:4px;padding:8px 12px;font-family:monospace;font-size:13px;color:#9aa6c7}
.roles{font-size:12px;color:#9aa6c7;margin-top:6px}
</style></head>
<body><div class="card">
<h1>Activá tu cuenta</h1>
<p class="muted">iTechDev · ARIA Core</p>
<div class="email">{{EMAIL}}</div>
<div class="roles">Roles: {{ROLES}}</div>
<form method="post" action="/dashboard/invite/{{TOKEN}}/accept">
<label>Elegí tu contraseña (mín. 8 caracteres)</label>
<input type="password" name="password" minlength="8" required autofocus>
<button type="submit">Activar cuenta</button>
</form>
</div></body></html>`

const inviteErrorHTML = `<!doctype html>
<html lang="es"><head><meta charset="utf-8"><title>Invitación no válida</title>
<style>
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#0b1933;color:#e7eefa;margin:0;padding:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
.card{background:#11203c;border:1px solid #4a1d1d;border-radius:8px;padding:32px;max-width:480px;text-align:center}
h1{margin:0 0 16px 0;font-size:20px;color:#f5b8b8}
p{color:#cdd6ec;margin:0}
a{color:#9aa6c7;display:inline-block;margin-top:18px;font-size:13px}
</style></head>
<body><div class="card">
<h1>Invitación no válida</h1>
<p>{{MSG}}</p>
<a href="/dashboard/login">Ir al login</a>
</div></body></html>`
