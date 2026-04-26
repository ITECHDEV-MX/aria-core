package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleProfilePage — GET /dashboard/me/profile
func (h *handlers) handleProfilePage(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Redirect(w, r, "/dashboard/login?next=/dashboard/me/profile", http.StatusSeeOther)
		return
	}
	var view *UserProfileView
	if h.cfg.Profile != nil {
		v, err := h.cfg.Profile.GetProfile(r.Context(), p.UID())
		if err == nil {
			view = v
		}
	}
	component := ProfilePage(view, "", "")
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Mi cuenta · Perfil", p.DisplayName(), "me", p.Roles(), component))
}

// handleProfileUpdate — POST /dashboard/me/profile
func (h *handlers) handleProfileUpdate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Profile == nil {
		http.Error(w, "profile service not configured", http.StatusServiceUnavailable)
		return
	}
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	upd := UserProfileUpdate{
		Name:      strings.TrimSpace(r.PostForm.Get("name")),
		Phone:     strings.TrimSpace(r.PostForm.Get("phone")),
		Timezone:  strings.TrimSpace(r.PostForm.Get("timezone")),
		Language:  strings.TrimSpace(r.PostForm.Get("language")),
		JobTitle:  strings.TrimSpace(r.PostForm.Get("job_title")),
		Bio:       strings.TrimSpace(r.PostForm.Get("bio")),
		AvatarURL: strings.TrimSpace(r.PostForm.Get("avatar_url")),
	}
	if err := h.cfg.Profile.UpdateProfile(r.Context(), p.UID(), upd); err != nil {
		view, _ := h.cfg.Profile.GetProfile(r.Context(), p.UID())
		renderComponent(w, r, ProfilePage(view, err.Error(), ""))
		return
	}
	view, _ := h.cfg.Profile.GetProfile(r.Context(), p.UID())
	renderComponent(w, r, ProfilePage(view, "", "Perfil actualizado correctamente."))
}

// handleNotificationsPage — GET /dashboard/me/notifications
func (h *handlers) handleNotificationsPage(w http.ResponseWriter, r *http.Request) {
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Redirect(w, r, "/dashboard/login?next=/dashboard/me/notifications", http.StatusSeeOther)
		return
	}
	prefs := map[string]any{}
	if h.cfg.Profile != nil {
		view, err := h.cfg.Profile.GetProfile(r.Context(), p.UID())
		if err == nil && view != nil && view.Preferences != nil {
			prefs = view.Preferences
		}
	}
	component := NotificationsPage(prefs, "", "")
	if isHTMXRequest(r) {
		renderComponent(w, r, component)
		return
	}
	renderComponent(w, r, Layout("Mi cuenta · Notificaciones", p.DisplayName(), "me", p.Roles(), component))
}

// handleNotificationsUpdate — POST /dashboard/me/notifications
func (h *handlers) handleNotificationsUpdate(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Profile == nil {
		http.Error(w, "profile service not configured", http.StatusServiceUnavailable)
		return
	}
	p := h.principalFromRequest(r)
	if p.UID() == "" {
		http.Redirect(w, r, "/dashboard/login", http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}

	notifs := map[string]bool{
		"mentions":         r.PostForm.Get("notif_mentions") == "on",
		"quote_events":     r.PostForm.Get("notif_quote_events") == "on",
		"share_links":      r.PostForm.Get("notif_share_links") == "on",
		"weekly_digest":    r.PostForm.Get("notif_weekly_digest") == "on",
		"zombie_sessions":  r.PostForm.Get("notif_zombie_sessions") == "on",
	}
	prefs := map[string]any{
		"notifications": notifs,
	}
	body, _ := json.Marshal(prefs)
	if err := h.cfg.Profile.UpdatePreferences(r.Context(), p.UID(), body); err != nil {
		renderComponent(w, r, NotificationsPage(prefs, err.Error(), ""))
		return
	}
	renderComponent(w, r, NotificationsPage(prefs, "", "Preferencias guardadas."))
}

// prefBool extrae un boolean de prefs[notifications.<key>] con fallback default.
// Acepta path "notifications.mentions" o llave plana "mentions".
func prefBool(prefs map[string]any, path string, defaultVal bool) bool {
	if prefs == nil {
		return defaultVal
	}
	parts := strings.Split(path, ".")
	cur := any(prefs)
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return defaultVal
		}
		cur = m[p]
		if cur == nil {
			return defaultVal
		}
	}
	if b, ok := cur.(bool); ok {
		return b
	}
	return defaultVal
}

// roleBadgeVariant retorna la clase badge apropiada por rol.
func roleBadgeVariant(role string) string {
	switch role {
	case "admin":
		return "danger"
	case "agent":
		return "warning"
	case "cotizador":
		return "success"
	case "project_admin":
		return "info"
	default:
		return "muted"
	}
}
