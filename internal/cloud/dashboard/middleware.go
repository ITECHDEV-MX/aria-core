package dashboard

import (
	"errors"
	"net/http"
	"strings"
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

// requireAnyRole envuelve requireSession y permite acceso si el usuario tiene
// al menos uno de los roles dados. Admin siempre tiene acceso (super-rol).
func (h *handlers) requireAnyRole(allowed []string, next http.HandlerFunc) http.HandlerFunc {
	return h.requireSession(func(w http.ResponseWriter, r *http.Request) {
		var userRoles []string
		if h.cfg.GetRoles != nil {
			userRoles = h.cfg.GetRoles(r)
		}
		ok := false
		for _, want := range allowed {
			for _, got := range userRoles {
				if got == want {
					ok = true
					break
				}
			}
			if ok {
				break
			}
		}
		if !ok {
			if isHTMXRequest(r) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`<div class="login-error" role="alert">Forbidden — required role not found</div>`))
				return
			}
			http.Error(w, "forbidden: required role not assigned", http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// ─── Request body size limits ───────────────────────────────────────────────
//
// Without a cap, a logged-in user (or an attacker after a stolen JWT)
// could DoS the box with a 10 GB form post. http.MaxBytesReader returns
// 413 Request Entity Too Large to the client on overflow.

// DefaultBodyLimit is the cap applied to every POST/PUT/PATCH dashboard
// request unless a route-specific override applies. 32 MiB.
const DefaultBodyLimit int64 = 32 << 20

// UploadBodyLimit is the cap applied to attachment / RFP upload routes
// (paths matching /pages/attachments and /cotizador/upload). 128 MiB.
const UploadBodyLimit int64 = 128 << 20

// uploadPathPrefixes lists the URL path prefixes that get the higher
// UploadBodyLimit. Match is via strings.Contains so it survives any
// /dashboard prefix or trailing path segments.
var uploadPathPrefixes = []string{
	"/pages/attachments",
	"/cotizador/upload",
	"/quote/upload",
}

// limitFor returns the byte cap appropriate for the given request URL
// path. Falls back to DefaultBodyLimit.
func limitFor(path string) int64 {
	for _, p := range uploadPathPrefixes {
		if strings.Contains(path, p) {
			return UploadBodyLimit
		}
	}
	return DefaultBodyLimit
}

// methodHasBody returns true for HTTP methods that typically carry a
// body. We only wrap r.Body for these — wrapping a GET's empty body
// is harmless but pollutes the trace.
func methodHasBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// limitBody wraps r.Body in http.MaxBytesReader. Returns 413 when the
// request exceeds the cap. Skipped for methods without bodies (GET,
// HEAD, OPTIONS) so static asset serving isn't penalized.
func limitBody(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && methodHasBody(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, limitFor(r.URL.Path))
		}
		next(w, r)
	}
}

// WrapWithBodyLimit returns h with every request body capped per
// limitFor (DefaultBodyLimit, with overrides for upload paths). Apply
// once at the cloudserver mount level for blanket protection.
//
// Public form of limitBody for callers outside this package.
func WrapWithBodyLimit(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil && methodHasBody(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, limitFor(r.URL.Path))
		}
		h.ServeHTTP(w, r)
	})
}
