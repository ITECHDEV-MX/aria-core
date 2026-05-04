package obs

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"":          slog.LevelInfo,
		"info":      slog.LevelInfo,
		"DEBUG":     slog.LevelDebug,
		" warn ":    slog.LevelWarn,
		"warning":   slog.LevelWarn,
		"error":     slog.LevelError,
		"nonsense":  slog.LevelInfo, // fallback
	}
	for in, want := range cases {
		if got := parseLevel(in); got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestLoggerWithAndFromContext(t *testing.T) {
	ctx := context.Background()
	if FromContext(ctx) == nil {
		t.Fatal("default logger should not be nil")
	}

	l := L().With("scope", "test")
	ctx = LoggerWith(ctx, l)
	got := FromContext(ctx)
	if got != l {
		t.Errorf("expected the same logger to come back, got different")
	}
}

func TestFromContext_NilCtx(t *testing.T) {
	// Should not panic, and should return the package default.
	if FromContext(nil) == nil {
		t.Error("expected non-nil logger from nil ctx")
	}
}

func TestWithRequestID_PropagatesIncomingID(t *testing.T) {
	calledWith := ""
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calledWith = r.Header.Get(RequestIDHeader)
		w.WriteHeader(http.StatusOK)
	})
	wrapped := WithRequestID(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(RequestIDHeader, "abc123")
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	if calledWith != "abc123" {
		t.Errorf("inner saw %q, want abc123", calledWith)
	}
	if rec.Header().Get(RequestIDHeader) != "abc123" {
		t.Errorf("response header missing request id")
	}
}

func TestWithRequestID_GeneratesWhenAbsent(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := WithRequestID(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	id := rec.Header().Get(RequestIDHeader)
	if len(id) != 32 { // 16-byte hex
		t.Errorf("generated id length %d, want 32 (hex of 16 bytes): %q", len(id), id)
	}
}

func TestWithRequestID_RejectsHugeIncoming(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := WithRequestID(inner)
	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	req.Header.Set(RequestIDHeader, strings.Repeat("a", 200))
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	id := rec.Header().Get(RequestIDHeader)
	if len(id) != 32 {
		t.Errorf("expected generated id (32 hex chars), got len %d", len(id))
	}
}

func TestRecorderRW_CapturesStatus(t *testing.T) {
	w := httptest.NewRecorder()
	r := &recorderRW{ResponseWriter: w, status: http.StatusOK}
	r.WriteHeader(http.StatusTeapot)
	if r.status != http.StatusTeapot {
		t.Errorf("status: want 418, got %d", r.status)
	}
}
