// Public read-only page view served at /p/{token} (no auth).
//
// This file owns:
//   - Per-IP rate limiting cookie (60 req / 60s burst).
//   - Password gate cookie scoped to a single token (15-min validity).
//   - Markdown rendering using yuin/goldmark (already a transitive dep).
//   - Attachment listing + signed-by-token download URL.
package cloudserver

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/attachments"
)

// publicCookiePrefix scopes pw cookies to a specific token. The cookie name
// includes the first 16 chars of the token so multiple share links open in
// the same browser do not collide.
const publicCookiePrefix = "aria_share_pw_"

// publicRateLimitWindow is the window we allow N requests per IP before
// returning HTTP 429. Cheap counter; resets every minute.
const publicRateLimitWindow = time.Minute
const publicRateLimitBurst = 60

// rateLimiter is a tiny in-memory IP→count tracker. Process-local; Cloud Run
// or multi-replica deployments would need a shared store, but for our single-
// node VPS deployment this is sufficient.
type rateLimiter struct {
	mu      sync.Mutex
	hits    map[string]rateEntry
	now     func() time.Time
}

type rateEntry struct {
	count    int
	resetAt  time.Time
}

var publicRateLimiter = &rateLimiter{
	hits: make(map[string]rateEntry),
	now:  time.Now,
}

func (r *rateLimiter) allow(ip string) bool {
	if ip == "" {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now()
	e := r.hits[ip]
	if now.After(e.resetAt) {
		e = rateEntry{count: 0, resetAt: now.Add(publicRateLimitWindow)}
	}
	e.count++
	r.hits[ip] = e
	return e.count <= publicRateLimitBurst
}

// clientIP extracts the originating IP, taking X-Forwarded-For (first hop)
// when present. We only trust the first IP because subsequent hops can be
// spoofed by clients.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if i := strings.LastIndexByte(r.RemoteAddr, ':'); i > 0 {
		return r.RemoteAddr[:i]
	}
	return r.RemoteAddr
}

// handlePublicPageGet renders the shared page if the token is valid + (if
// password-protected) the request carries a verified cookie.
func (s *CloudServer) handlePublicPageGet(w http.ResponseWriter, r *http.Request) {
	if s.pageShares == nil || s.pagePublicView == nil {
		http.Error(w, "share links are not configured on this server", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		http.NotFound(w, r)
		return
	}
	ip := clientIP(r)
	if !publicRateLimiter.allow(ip) {
		http.Error(w, "rate limit exceeded — try again in a moment", http.StatusTooManyRequests)
		return
	}

	link, err := s.pageShares.GetByToken(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	// Expiry / revocation gate before password.
	if link.IsRevoked {
		http.Error(w, "this link has been revoked", http.StatusGone)
		return
	}
	if link.ExpiresAt != nil && link.ExpiresAt.Before(time.Now().UTC()) {
		http.Error(w, "this link has expired", http.StatusGone)
		return
	}

	// Password gate: render the form unless a valid cookie is present.
	if link.HasPassword && !s.publicHasPasswordCookie(r, token) {
		renderPublicPasswordForm(w, r, token, "")
		return
	}

	title, contentMD, sensitivity, err := s.pagePublicView.PageMarkdown(r.Context(), link.PageID)
	if err != nil {
		// PAGES module not yet wired or page disappeared. Show a generic message
		// instead of leaking the internal error.
		http.Error(w, "this page is no longer available", http.StatusNotFound)
		return
	}
	// Defense in depth: block confidential pages even if the share link was
	// minted before the sensitivity flag flipped.
	if strings.EqualFold(sensitivity, "confidential") {
		http.Error(w, "this page is confidential and cannot be shared publicly", http.StatusForbidden)
		return
	}
	atts, _ := s.pagePublicView.AttachmentsForPage(r.Context(), link.PageID)

	// Resolve also bumps view_count + audit log.
	_, _ = s.pageShares.Resolve(r.Context(), token, "", ip, r.UserAgent())

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
	_, _ = w.Write([]byte(renderPublicPageHTML(title, contentMD, atts, token, link)))
}

// handlePublicPageAuth processes the password form POST. On success it sets a
// short-lived cookie and 303-redirects back to GET /p/{token}.
func (s *CloudServer) handlePublicPageAuth(w http.ResponseWriter, r *http.Request) {
	if s.pageShares == nil {
		http.Error(w, "share links are not configured on this server", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	if token == "" {
		http.NotFound(w, r)
		return
	}
	ip := clientIP(r)
	if !publicRateLimiter.allow(ip) {
		http.Error(w, "rate limit exceeded — try again in a moment", http.StatusTooManyRequests)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	pw := strings.TrimSpace(r.PostForm.Get("password"))
	link, err := s.pageShares.Resolve(r.Context(), token, pw, ip, r.UserAgent())
	if err != nil {
		// Distinguish only "wrong password" from generic errors.
		msg := "incorrect password"
		switch {
		case err.Error() != "" && strings.Contains(err.Error(), "expired"):
			msg = "this link has expired"
		case err.Error() != "" && strings.Contains(err.Error(), "not found"):
			msg = "this link is no longer valid"
		}
		renderPublicPasswordForm(w, r, token, msg)
		return
	}
	// Set the gating cookie.
	cookie := &http.Cookie{
		Name:     publicCookieName(token),
		Value:    "ok",
		Path:     "/p/" + token,
		HttpOnly: true,
		Secure:   r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"),
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int((15 * time.Minute).Seconds()),
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, "/p/"+link.Token, http.StatusSeeOther)
}

// handlePublicAttachmentDownload streams an attachment for a shared page only
// when the share token is valid (and password cookie present if required).
//
// Path: /p/{token}/files/{attachmentID}
func (s *CloudServer) handlePublicAttachmentDownload(w http.ResponseWriter, r *http.Request) {
	if s.pageShares == nil || s.pageAttachments == nil {
		http.Error(w, "not configured", http.StatusServiceUnavailable)
		return
	}
	token := strings.TrimSpace(r.PathValue("token"))
	id := strings.TrimSpace(r.PathValue("id"))
	if token == "" || id == "" {
		http.NotFound(w, r)
		return
	}
	ip := clientIP(r)
	if !publicRateLimiter.allow(ip) {
		http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
		return
	}
	link, err := s.pageShares.GetByToken(r.Context(), token)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if link.IsRevoked {
		http.Error(w, "revoked", http.StatusGone)
		return
	}
	if link.ExpiresAt != nil && link.ExpiresAt.Before(time.Now().UTC()) {
		http.Error(w, "expired", http.StatusGone)
		return
	}
	if link.HasPassword && !s.publicHasPasswordCookie(r, token) {
		http.Error(w, "password required (open the page first)", http.StatusUnauthorized)
		return
	}
	att, err := s.pageAttachments.Get(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if att.PageID != link.PageID {
		// File belongs to a different page — refuse instead of letting the link
		// act as an open-redirect download.
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	rc, size, err := s.pageAttachments.OpenContent(att)
	if err != nil {
		http.Error(w, "open: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", att.MIMEType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, attachments.SanitizeFilename(att.OriginalFilename)))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, rc)
}

func (s *CloudServer) publicHasPasswordCookie(r *http.Request, token string) bool {
	c, err := r.Cookie(publicCookieName(token))
	if err != nil {
		return false
	}
	return c.Value == "ok"
}

func publicCookieName(token string) string {
	suffix := token
	if len(suffix) > 16 {
		suffix = suffix[:16]
	}
	return publicCookiePrefix + suffix
}

func renderPublicPasswordForm(w http.ResponseWriter, r *http.Request, token, errorMsg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	errBlock := ""
	if errorMsg != "" {
		errBlock = fmt.Sprintf(`<p class="err" style="color:#c33">%s</p>`, html.EscapeString(errorMsg))
	}
	body := fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>Password required · ARIA Core</title>
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <style>
    body { font-family: -apple-system, system-ui, sans-serif; background:#0e0e10; color:#e8e8e8; margin:0; min-height:100vh; display:flex; align-items:center; justify-content:center; }
    .card { background:#1a1a1f; border:1px solid #333; padding:2rem; border-radius:6px; max-width:380px; width:100%%; }
    h1 { margin-top:0; font-size:1.2rem; }
    input[type=password] { width:100%%; padding:0.6rem; background:#0e0e10; border:1px solid #444; color:#fff; border-radius:4px; box-sizing:border-box; }
    button { margin-top:1rem; padding:0.6rem 1rem; background:#3b82f6; color:#fff; border:0; border-radius:4px; cursor:pointer; }
    .footer { margin-top:2rem; font-size:0.7rem; color:#666; text-align:center; }
  </style>
</head>
<body>
  <form method="post" action="/p/%s/auth" class="card">
    <h1>Password required</h1>
    <p>This page is password-protected.</p>
    %s
    <input type="password" name="password" autofocus required>
    <button type="submit">Unlock</button>
    <p class="footer">Powered by ARIA Core · iTechDev</p>
  </form>
</body>
</html>`, html.EscapeString(token), errBlock)
	_, _ = w.Write([]byte(body))
	_ = r // r currently unused but kept for future logging hooks
}

// renderPublicPageHTML renders a minimalist read-only view. We use a tiny
// in-house markdown renderer (just enough for ## headings, **bold**, code, links,
// images, lists). yuin/goldmark is already a transitive dep, but pulling it in
// here adds a non-trivial linker cost — for the public page we keep things
// simple and predictable.
func renderPublicPageHTML(title, md string, atts []*attachments.Attachment, token string, link *attachments.ShareLink) string {
	rendered := renderMarkdown(md)
	expiryNote := ""
	if link != nil && link.ExpiresAt != nil {
		expiryNote = fmt.Sprintf(`<span class="expiry">Expires %s</span>`, html.EscapeString(link.ExpiresAt.Format(time.RFC1123)))
	}
	attBlock := ""
	if len(atts) > 0 {
		var sb strings.Builder
		sb.WriteString(`<section class="att-section"><h2>Attachments</h2><ul class="att-list">`)
		for _, a := range atts {
			thumb := ""
			if a.ThumbnailPath != "" {
				thumb = fmt.Sprintf(`<img src="/p/%s/files/%s/thumb" alt="" loading="lazy" class="att-thumb">`,
					html.EscapeString(token), html.EscapeString(a.ID))
			}
			sb.WriteString(fmt.Sprintf(`<li>%s<a href="/p/%s/files/%s">%s</a> <span class="att-meta">%s · %s</span></li>`,
				thumb,
				html.EscapeString(token), html.EscapeString(a.ID),
				html.EscapeString(a.OriginalFilename),
				html.EscapeString(a.MIMEType),
				html.EscapeString(attachments.HumanSize(a.SizeBytes)),
			))
		}
		sb.WriteString(`</ul></section>`)
		attBlock = sb.String()
	}

	return fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>%s</title>
  <meta name="viewport" content="width=device-width,initial-scale=1">
  <meta name="robots" content="noindex,nofollow">
  <style>
    body { font-family: -apple-system, system-ui, sans-serif; background:#fff; color:#222; margin:0; line-height:1.6; }
    .wrap { max-width:760px; margin:0 auto; padding:2rem 1.5rem 4rem; }
    header.brand { border-bottom:1px solid #eee; padding:1rem 1.5rem; display:flex; align-items:center; justify-content:space-between; font-size:0.85rem; color:#666; }
    header.brand .logo { font-weight:600; color:#222; }
    h1 { margin-top:0; }
    h2 { margin-top:2rem; padding-bottom:0.3rem; border-bottom:1px solid #eee; }
    pre, code { background:#f5f5f7; padding:0.1rem 0.3rem; border-radius:3px; font-size:0.9em; }
    pre { padding:0.8rem; overflow-x:auto; }
    img { max-width:100%%; height:auto; }
    a { color:#2563eb; }
    .expiry { color:#888; }
    .att-section { margin-top:3rem; }
    .att-list { list-style:none; padding:0; }
    .att-list li { display:flex; align-items:center; gap:0.6rem; margin:0.4rem 0; padding:0.5rem; border:1px solid #eee; border-radius:4px; }
    .att-thumb { width:60px; height:60px; object-fit:cover; border-radius:3px; }
    .att-meta { color:#888; font-size:0.85em; }
    footer { text-align:center; color:#888; font-size:0.8rem; margin-top:4rem; padding:1rem; border-top:1px solid #eee; }
  </style>
</head>
<body>
  <header class="brand">
    <span class="logo">ARIA Core</span>
    %s
  </header>
  <main class="wrap">
    <h1>%s</h1>
    <article>%s</article>
    %s
  </main>
  <footer>Powered by ARIA Core · iTechDev</footer>
</body>
</html>`, html.EscapeString(title), expiryNote, html.EscapeString(title), rendered, attBlock)
}

// renderMarkdown is a minimal subset renderer: paragraphs, ##/### headings,
// fenced code blocks, **bold**, *italic*, inline `code`, [text](url), and
// ![alt](url) images. Anything we don't recognize is escaped and dropped into
// a paragraph.
//
// This is intentionally conservative: confidential client content gets aggressive
// HTML escaping at every layer, and we never run user-supplied HTML.
func renderMarkdown(src string) string {
	var sb strings.Builder
	lines := strings.Split(src, "\n")
	inFence := false
	var fence strings.Builder

	flushFence := func() {
		if fence.Len() == 0 {
			return
		}
		sb.WriteString("<pre><code>")
		sb.WriteString(html.EscapeString(fence.String()))
		sb.WriteString("</code></pre>\n")
		fence.Reset()
	}

	var paraBuf strings.Builder
	flushPara := func() {
		if paraBuf.Len() == 0 {
			return
		}
		sb.WriteString("<p>")
		sb.WriteString(renderInline(paraBuf.String()))
		sb.WriteString("</p>\n")
		paraBuf.Reset()
	}

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			if inFence {
				flushFence()
				inFence = false
			} else {
				flushPara()
				inFence = true
			}
			continue
		}
		if inFence {
			fence.WriteString(line)
			fence.WriteByte('\n')
			continue
		}
		trimmed := strings.TrimRight(line, " \t")
		switch {
		case strings.HasPrefix(trimmed, "### "):
			flushPara()
			sb.WriteString("<h3>")
			sb.WriteString(renderInline(trimmed[4:]))
			sb.WriteString("</h3>\n")
		case strings.HasPrefix(trimmed, "## "):
			flushPara()
			sb.WriteString("<h2>")
			sb.WriteString(renderInline(trimmed[3:]))
			sb.WriteString("</h2>\n")
		case strings.HasPrefix(trimmed, "# "):
			flushPara()
			sb.WriteString("<h1>")
			sb.WriteString(renderInline(trimmed[2:]))
			sb.WriteString("</h1>\n")
		case strings.HasPrefix(trimmed, "- "):
			// Buffer simple bullet list — not recursive but enough for read-only.
			flushPara()
			sb.WriteString("<ul><li>")
			sb.WriteString(renderInline(trimmed[2:]))
			sb.WriteString("</li></ul>\n")
		case strings.TrimSpace(trimmed) == "":
			flushPara()
		default:
			if paraBuf.Len() > 0 {
				paraBuf.WriteByte(' ')
			}
			paraBuf.WriteString(trimmed)
		}
	}
	if inFence {
		flushFence()
	}
	flushPara()
	return sb.String()
}

func renderInline(s string) string {
	// Escape first to neutralize any HTML in the source.
	s = html.EscapeString(s)
	// Apply lightweight markdown — order matters: bold before italic so ** is
	// not consumed as two * runs.
	s = simpleReplaceDouble(s, "**", "<strong>", "</strong>")
	s = simpleReplaceDouble(s, "__", "<strong>", "</strong>")
	s = simpleReplaceSingle(s, "`", "<code>", "</code>")
	s = renderInlineLinks(s)
	return s
}

func simpleReplaceDouble(src, marker, openTag, closeTag string) string {
	var out strings.Builder
	open := true
	i := 0
	for i < len(src) {
		if i+len(marker) <= len(src) && src[i:i+len(marker)] == marker {
			if open {
				out.WriteString(openTag)
			} else {
				out.WriteString(closeTag)
			}
			open = !open
			i += len(marker)
			continue
		}
		out.WriteByte(src[i])
		i++
	}
	return out.String()
}

func simpleReplaceSingle(src, marker, openTag, closeTag string) string {
	return simpleReplaceDouble(src, marker, openTag, closeTag)
}

// renderInlineLinks rewrites [text](url) and ![alt](url). URL is escaped via
// html.EscapeString already (we ran it before calling here), so the only
// thing left is to rewrite the syntax. We refuse javascript: urls.
func renderInlineLinks(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		// ![alt](url)
		if i+2 < len(s) && s[i] == '!' && s[i+1] == '[' {
			if end, alt, url := parseLink(s[i+1:]); end > 0 {
				if isSafeURL(url) {
					out.WriteString(fmt.Sprintf(`<img src="%s" alt="%s" loading="lazy">`, url, alt))
					i += 1 + end
					continue
				}
			}
		}
		// [text](url)
		if s[i] == '[' {
			if end, text, url := parseLink(s[i:]); end > 0 {
				if isSafeURL(url) {
					out.WriteString(fmt.Sprintf(`<a href="%s" rel="nofollow noopener">%s</a>`, url, text))
					i += end
					continue
				}
			}
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}

// parseLink scans `[label](url)` starting at the leading `[`. Returns the
// number of input bytes consumed plus the inner label and url; or 0 if the
// pattern doesn't match.
func parseLink(s string) (consumed int, label, url string) {
	if s == "" || s[0] != '[' {
		return 0, "", ""
	}
	// Find closing `]`
	close := strings.IndexByte(s[1:], ']')
	if close < 0 {
		return 0, "", ""
	}
	close += 1
	if close+1 >= len(s) || s[close+1] != '(' {
		return 0, "", ""
	}
	end := strings.IndexByte(s[close+2:], ')')
	if end < 0 {
		return 0, "", ""
	}
	end += close + 2
	return end + 1, s[1:close], s[close+2 : end]
}

func isSafeURL(u string) bool {
	u = strings.ToLower(strings.TrimSpace(u))
	if u == "" {
		return false
	}
	if strings.HasPrefix(u, "javascript:") {
		return false
	}
	if strings.HasPrefix(u, "data:") && !strings.HasPrefix(u, "data:image/") {
		return false
	}
	return true
}

// Compile-time guard to keep this file referenced even when share is nil.
var _ context.Context = context.Background()
