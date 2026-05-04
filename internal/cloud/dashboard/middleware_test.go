package dashboard

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLimitFor_DefaultAndOverrides(t *testing.T) {
	cases := map[string]int64{
		"/dashboard/login":                       DefaultBodyLimit,
		"/dashboard/admin/users":                 DefaultBodyLimit,
		"/dashboard/pages/attachments/upload":    UploadBodyLimit,
		"/dashboard/cotizador/upload/foo":        UploadBodyLimit,
		"/dashboard/quote/upload/123":            UploadBodyLimit,
	}
	for path, want := range cases {
		got := limitFor(path)
		if got != want {
			t.Errorf("limitFor(%q) = %d, want %d", path, got, want)
		}
	}
}

func TestMethodHasBody(t *testing.T) {
	yes := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}
	no := []string{http.MethodGet, http.MethodHead, http.MethodOptions}
	for _, m := range yes {
		if !methodHasBody(m) {
			t.Errorf("%s should have body", m)
		}
	}
	for _, m := range no {
		if methodHasBody(m) {
			t.Errorf("%s should NOT have body", m)
		}
	}
}

// TestWrapWithBodyLimit_AcceptsSmallBody confirms a request well under
// the cap passes through unmodified.
func TestWrapWithBodyLimit_AcceptsSmallBody(t *testing.T) {
	got := ""
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		got = string(buf)
		w.WriteHeader(http.StatusOK)
	})
	wrapped := WrapWithBodyLimit(inner)

	body := strings.NewReader("hello")
	req := httptest.NewRequest(http.MethodPost, "/dashboard/login", body)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status: want 200, got %d", w.Code)
	}
	if got != "hello" {
		t.Errorf("body: want %q, got %q", "hello", got)
	}
}

// TestWrapWithBodyLimit_RejectsOversize confirms a body larger than
// the default cap triggers MaxBytesReader and the handler sees an
// error reading the body.
func TestWrapWithBodyLimit_RejectsOversize(t *testing.T) {
	// Build a body strictly larger than the default cap.
	huge := bytes.Repeat([]byte("a"), int(DefaultBodyLimit)+1024)

	bodyExceeded := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			// http.MaxBytesError is the canonical error type since Go 1.19.
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				bodyExceeded = true
			}
		}
		w.WriteHeader(http.StatusRequestEntityTooLarge)
	})
	wrapped := WrapWithBodyLimit(inner)

	req := httptest.NewRequest(http.MethodPost, "/dashboard/login", bytes.NewReader(huge))
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	if !bodyExceeded {
		t.Error("expected MaxBytesError when body exceeds DefaultBodyLimit")
	}
}

// TestWrapWithBodyLimit_GETIsNotWrapped confirms that GET requests
// pass through without the Body being replaced (they have no body to
// limit anyway, and wrapping nil bodies would panic in older Go).
func TestWrapWithBodyLimit_GETIsNotWrapped(t *testing.T) {
	called := false
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	wrapped := WrapWithBodyLimit(inner)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/health", nil)
	w := httptest.NewRecorder()
	wrapped.ServeHTTP(w, req)

	if !called || w.Code != http.StatusOK {
		t.Errorf("GET should pass through unchanged, code=%d called=%v", w.Code, called)
	}
}
