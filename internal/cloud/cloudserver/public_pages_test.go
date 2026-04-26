package cloudserver

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/attachments"
)

// stubShareStore is a minimal in-memory PageShareService for routing tests.
type stubShareStore struct {
	links            map[string]*attachments.ShareLink
	resolveErr       error
	resolveCallCount int
}

func newStubShareStore() *stubShareStore {
	return &stubShareStore{links: map[string]*attachments.ShareLink{}}
}

func (s *stubShareStore) Create(ctx context.Context, p attachments.CreateShareParams) (*attachments.ShareLink, error) {
	link := &attachments.ShareLink{ID: "id-" + p.PageID, PageID: p.PageID, Token: "tok-" + p.PageID, CreatedAt: time.Now()}
	if p.Password != "" {
		link.HasPassword = true
	}
	s.links[link.Token] = link
	return link, nil
}
func (s *stubShareStore) Get(ctx context.Context, id string) (*attachments.ShareLink, error) {
	for _, l := range s.links {
		if l.ID == id {
			return l, nil
		}
	}
	return nil, attachments.ErrShareNotFound
}
func (s *stubShareStore) GetByToken(ctx context.Context, token string) (*attachments.ShareLink, error) {
	if l, ok := s.links[token]; ok {
		return l, nil
	}
	return nil, attachments.ErrShareNotFound
}
func (s *stubShareStore) ListByPage(ctx context.Context, pageID string, includeRevoked bool) ([]*attachments.ShareLink, error) {
	out := []*attachments.ShareLink{}
	for _, l := range s.links {
		if l.PageID == pageID {
			out = append(out, l)
		}
	}
	return out, nil
}
func (s *stubShareStore) Revoke(ctx context.Context, id, byUID string) error {
	for _, l := range s.links {
		if l.ID == id {
			l.IsRevoked = true
			return nil
		}
	}
	return attachments.ErrShareNotFound
}
func (s *stubShareStore) Resolve(ctx context.Context, token, password, ip, ua string) (*attachments.ShareLink, error) {
	s.resolveCallCount++
	if s.resolveErr != nil {
		return nil, s.resolveErr
	}
	l, ok := s.links[token]
	if !ok {
		return nil, attachments.ErrShareNotFound
	}
	if l.IsRevoked {
		return nil, attachments.ErrShareNotFound
	}
	if l.ExpiresAt != nil && l.ExpiresAt.Before(time.Now().UTC()) {
		return nil, attachments.ErrShareExpired
	}
	if l.HasPassword && password != "secret" {
		return nil, attachments.ErrSharePasswordRequired
	}
	l.ViewCount++
	return l, nil
}

// stubPagePublicView returns a deterministic page for a known id.
type stubPagePublicView struct {
	pageID      string
	title       string
	contentMD   string
	sensitivity string
	missing     bool
}

func (s *stubPagePublicView) PageMarkdown(ctx context.Context, pageID string) (string, string, string, error) {
	if s.missing || pageID != s.pageID {
		return "", "", "", errors.New("not found")
	}
	return s.title, s.contentMD, s.sensitivity, nil
}
func (s *stubPagePublicView) AttachmentsForPage(ctx context.Context, pageID string) ([]*attachments.Attachment, error) {
	return nil, nil
}

// newPublicTestServer constructs a CloudServer with the public route mounted
// for a fixed share token.
func newPublicTestServer(t *testing.T, link *attachments.ShareLink, view *stubPagePublicView) (*CloudServer, *stubShareStore) {
	t.Helper()
	share := newStubShareStore()
	share.links[link.Token] = link
	srv := &CloudServer{}
	srv.pageShares = share
	srv.pagePublicView = view
	srv.routes()
	return srv, share
}

// TestPublicRouteRequiresValidToken: an unknown token returns 404.
func TestPublicRouteRequiresValidToken(t *testing.T) {
	srv, _ := newPublicTestServer(t, &attachments.ShareLink{ID: "x", Token: "valid", PageID: "p1"}, &stubPagePublicView{pageID: "p1", title: "T", contentMD: "body"})
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/p/unknown", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

// TestPublicRouteNoAuthForOpenLink: a token with no password returns 200 with
// the page markdown rendered, and no auth headers are required.
func TestPublicRouteNoAuthForOpenLink(t *testing.T) {
	link := &attachments.ShareLink{ID: "x", Token: "openlink", PageID: "p1"}
	view := &stubPagePublicView{pageID: "p1", title: "Hello", contentMD: "## heading\n\nbody"}
	srv, _ := newPublicTestServer(t, link, view)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/p/openlink", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Hello") {
		t.Error("expected page title in body")
	}
	if !strings.Contains(body, "<h2>heading") {
		t.Error("expected ## heading rendered as h2")
	}
	if !strings.Contains(body, "Powered by ARIA Core") {
		t.Error("expected footer attribution")
	}
	if v := w.Header().Get("Cache-Control"); v != "private, no-store" {
		t.Errorf("Cache-Control = %q, want private, no-store", v)
	}
}

// TestPublicRoutePasswordGate: a password-protected token returns the password
// form on first GET, then accepts the form post and redirects.
func TestPublicRoutePasswordGate(t *testing.T) {
	link := &attachments.ShareLink{ID: "x", Token: "pwlink", PageID: "p1", HasPassword: true}
	view := &stubPagePublicView{pageID: "p1", title: "Locked", contentMD: "secret"}
	srv, _ := newPublicTestServer(t, link, view)

	// GET without cookie shows password form.
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/p/pwlink", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("first GET status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Password required") {
		t.Errorf("expected password form, got %q", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "secret") {
		t.Errorf("password form leaked content")
	}

	// POST with wrong password renders form again.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/p/pwlink/auth",
		strings.NewReader("password=wrong"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), "Password required") {
		t.Errorf("wrong pw should re-render form")
	}

	// POST with correct password 303-redirects and sets cookie.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/p/pwlink/auth",
		strings.NewReader("password=secret"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("correct pw status = %d body=%s", w.Code, w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected pw cookie")
	}
	cookie := cookies[0]
	if !strings.HasPrefix(cookie.Name, publicCookiePrefix) {
		t.Errorf("cookie name = %q", cookie.Name)
	}

	// Subsequent GET with cookie reveals content.
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodGet, "/p/pwlink", nil)
	r.AddCookie(cookie)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("authed GET status = %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Locked") {
		t.Errorf("authed GET missing title")
	}
	if !strings.Contains(w.Body.String(), "secret") {
		t.Errorf("authed GET missing content")
	}
}

// TestPublicRouteBlocksConfidentialPage: a page with sensitivity=confidential
// is blocked even if a share link points to it (defense in depth).
func TestPublicRouteBlocksConfidentialPage(t *testing.T) {
	link := &attachments.ShareLink{ID: "x", Token: "conftok", PageID: "p1"}
	view := &stubPagePublicView{pageID: "p1", title: "X", contentMD: "x", sensitivity: "confidential"}
	srv, _ := newPublicTestServer(t, link, view)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/p/conftok", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

// TestPublicRouteExpiredLink returns 410 Gone.
func TestPublicRouteExpiredLink(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	link := &attachments.ShareLink{ID: "x", Token: "expired", PageID: "p1", ExpiresAt: &past}
	view := &stubPagePublicView{pageID: "p1"}
	srv, _ := newPublicTestServer(t, link, view)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/p/expired", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusGone {
		t.Errorf("status = %d, want 410", w.Code)
	}
}

// Public route never lives under /dashboard/. This test ensures the route
// table won't accidentally pick it up under the protected prefix.
func TestPublicRouteNotMountedUnderDashboard(t *testing.T) {
	link := &attachments.ShareLink{ID: "x", Token: "ok", PageID: "p1"}
	view := &stubPagePublicView{pageID: "p1", title: "T", contentMD: "x"}
	srv, _ := newPublicTestServer(t, link, view)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/p/ok", nil)
	srv.Handler().ServeHTTP(w, r)
	if w.Code == http.StatusOK {
		t.Errorf("public content should NOT be reachable under /dashboard/")
	}
}

// We import io/strings/testing only via package references; ensure the
// linter doesn't strip them.
var _ = io.Discard
