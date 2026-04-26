package dashboard

import (
	"errors"
	"net/http"
)

var errUnauthorized = errors.New("dashboard: unauthorized")

func (h *handlers) requireSession(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.RequireSession != nil {
			if err := h.cfg.RequireSession(r); err != nil {
				loginPath := dashboardLoginPathWithNext(r.URL.RequestURI())
				if isHTMXRequest(r) {
					w.Header().Set("HX-Redirect", loginPath)
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				http.Redirect(w, r, loginPath, http.StatusSeeOther)
				return
			}
		}
		next(w, r)
	}
}

// requireAdmin envuelve requireSession y agrega chequeo de role=admin.
// Devuelve 403 con mensaje si el user autenticado no es admin.
func (h *handlers) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		if h.cfg.IsAdmin == nil || !h.cfg.IsAdmin(r) {
			if isHTMXRequest(r) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<div class="login-error" role="alert">Forbidden — admin role required</div>`))
				return
			}
			http.Error(w, "forbidden: admin role required", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}
