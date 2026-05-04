// Package obs provides cross-cutting observability primitives for
// ARIA Core: a configured slog logger, a request-id middleware, and
// helpers for adding structured fields to log lines.
//
// Configuration via environment variables (read once at Init):
//
//	ARIA_LOG_FORMAT  json | text  (default: text)
//	ARIA_LOG_LEVEL   debug | info | warn | error (default: info)
//
// All cloud-server packages are migrating from stdlib log.Printf to
// the slog logger returned here. Migration is incremental — keeping
// the stdlib log compatible is fine until each module is touched.
package obs

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"sync"
)

var (
	once   sync.Once
	logger *slog.Logger
)

// Init configures the package logger based on environment variables.
// Idempotent — only the first call has effect; subsequent calls are
// no-ops so re-init from tests is safe.
func Init() *slog.Logger {
	once.Do(func() {
		level := parseLevel(os.Getenv("ARIA_LOG_LEVEL"))
		opts := &slog.HandlerOptions{Level: level}
		var h slog.Handler
		switch strings.ToLower(strings.TrimSpace(os.Getenv("ARIA_LOG_FORMAT"))) {
		case "json":
			h = slog.NewJSONHandler(os.Stderr, opts)
		default:
			h = slog.NewTextHandler(os.Stderr, opts)
		}
		logger = slog.New(h)
		slog.SetDefault(logger)
	})
	return logger
}

// L returns the package logger, initializing with defaults if Init
// was never called.
func L() *slog.Logger {
	if logger == nil {
		_ = Init()
	}
	return logger
}

func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

type ctxKey struct{}

// LoggerWith returns a copy of ctx that carries logger as request-scoped
// observability. If ctx already carries a logger, it is replaced.
func LoggerWith(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// FromContext extracts a request-scoped logger from ctx, falling back
// to the package default. Always returns a non-nil logger.
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return L()
	}
	if v, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok && v != nil {
		return v
	}
	return L()
}
