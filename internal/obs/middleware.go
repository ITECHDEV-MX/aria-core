package obs

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"time"
)

// RequestIDHeader is the HTTP header (and slog attribute key) for the
// per-request id. Cloudflare adds CF-Ray; we expose request_id under
// our own header so internal services can correlate even outside CF.
const RequestIDHeader = "X-Request-Id"

// WithRequestID wraps next so every request:
//   - has a logger in its context tagged with request_id, method, path
//   - emits an "http_request" log line at the end with status + duration
//   - sets the X-Request-Id response header
//
// Inbound X-Request-Id is honored if present (max 128 chars), otherwise
// we generate a 16-byte hex id.
func WithRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if len(id) == 0 || len(id) > 128 {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)

		l := L().With(
			"request_id", id,
			"method", r.Method,
			"path", r.URL.Path,
		)
		ctx := LoggerWith(r.Context(), l)

		rw := &recorderRW{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(rw, r.WithContext(ctx))
		l.Info("http_request",
			"status", rw.status,
			"duration_ms", time.Since(start).Milliseconds(),
		)
	})
}

func newRequestID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// Last-ditch fallback: timestamp.
		return hex.EncodeToString([]byte(time.Now().Format(time.RFC3339Nano)))
	}
	return hex.EncodeToString(b[:])
}

// recorderRW is a tiny ResponseWriter that captures the status code
// for logging. We do not buffer the body (no perf hit).
type recorderRW struct {
	http.ResponseWriter
	status int
}

func (r *recorderRW) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}
