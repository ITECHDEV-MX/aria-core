// Dashboard handlers for page attachments + share links.
//
// Following the dashboard_recipes.go pattern: we render raw HTML wrapped in
// dashboard.Layout via templ.Raw, so we don't need a `templ generate` step.
// The Agent PAGES will own /dashboard/pages itself; this file mounts only the
// drop-in components rendered as HTMX partials inside their page detail view.
package cloudserver

import (
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/attachments"
)

// newPageAttachmentsDashboard returns a mount function that registers attachment-
// and share-related dashboard routes if the cloudserver has the right services
// wired. Returns nil when nothing should be mounted.
func newPageAttachmentsDashboard(s *CloudServer) func(
	mux *http.ServeMux,
	requireSession func(r *http.Request) error,
	getDisplayName func(r *http.Request) string,
	getRoles func(r *http.Request) []string,
) {
	if s.pageAttachments == nil {
		return nil
	}
	return func(mux *http.ServeMux,
		requireSession func(r *http.Request) error,
		getDisplayName func(r *http.Request) string,
		getRoles func(r *http.Request) []string,
	) {
		guard := func(next http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				if requireSession != nil {
					if err := requireSession(r); err != nil {
						http.Redirect(w, r, "/dashboard/login?next="+r.URL.RequestURI(), http.StatusSeeOther)
						return
					}
				}
				next(w, r)
			}
		}

		// HTMX partials — these get embedded inside the PAGES detail view.
		mux.HandleFunc("GET /dashboard/pages/{pageID}/attachments", guard(func(w http.ResponseWriter, r *http.Request) {
			s.dashAttachmentsPartial(w, r)
		}))
		mux.HandleFunc("POST /dashboard/pages/{pageID}/attachments", guard(func(w http.ResponseWriter, r *http.Request) {
			s.dashAttachmentUpload(w, r)
		}))
		mux.HandleFunc("POST /dashboard/attachments/{id}/delete", guard(func(w http.ResponseWriter, r *http.Request) {
			s.dashAttachmentDelete(w, r)
		}))

		if s.pageShares != nil {
			mux.HandleFunc("GET /dashboard/pages/{pageID}/shares", guard(func(w http.ResponseWriter, r *http.Request) {
				s.dashSharesPartial(w, r)
			}))
			mux.HandleFunc("POST /dashboard/pages/{pageID}/shares", guard(func(w http.ResponseWriter, r *http.Request) {
				s.dashShareCreate(w, r)
			}))
			mux.HandleFunc("POST /dashboard/shares/{id}/revoke", guard(func(w http.ResponseWriter, r *http.Request) {
				s.dashShareRevoke(w, r)
			}))
		}
	}
}

// ─── handlers ───────────────────────────────────────────────────────────────

func (s *CloudServer) dashAttachmentsPartial(w http.ResponseWriter, r *http.Request) {
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	atts, err := s.pageAttachments.ListByPage(r.Context(), pageID)
	if err != nil {
		http.Error(w, "list attachments: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderAttachmentsCard(pageID, atts, s.pageAttachments.MaxBytes())))
}

func (s *CloudServer) dashAttachmentUpload(w http.ResponseWriter, r *http.Request) {
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	maxBytes := s.pageAttachments.MaxBytes()
	if maxBytes <= 0 {
		maxBytes = attachments.DefaultMaxBytes
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		http.Error(w, "parse: "+err.Error(), http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "file is required", http.StatusBadRequest)
		return
	}
	defer file.Close()
	uid := ""
	if claims, _ := claimsFromContext(r.Context()); claims != nil {
		uid = claims.UID
	}
	if _, err := s.pageAttachments.Upload(r.Context(), attachments.UploadParams{
		PageID:           pageID,
		OriginalFilename: header.Filename,
		Body:             file,
		UploadedByUID:    uid,
		Description:      strings.TrimSpace(r.FormValue("description")),
	}); err != nil {
		http.Error(w, "upload: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Return the freshly-rendered card so HTMX can swap in place.
	atts, _ := s.pageAttachments.ListByPage(r.Context(), pageID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderAttachmentsCard(pageID, atts, maxBytes)))
}

func (s *CloudServer) dashAttachmentDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	uid := ""
	if claims, _ := claimsFromContext(r.Context()); claims != nil {
		uid = claims.UID
	}
	att, err := s.pageAttachments.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.pageAttachments.Delete(r.Context(), id, uid); err != nil {
		http.Error(w, "delete: "+err.Error(), http.StatusInternalServerError)
		return
	}
	atts, _ := s.pageAttachments.ListByPage(r.Context(), att.PageID)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderAttachmentsCard(att.PageID, atts, s.pageAttachments.MaxBytes())))
}

func (s *CloudServer) dashSharesPartial(w http.ResponseWriter, r *http.Request) {
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	shares, err := s.pageShares.ListByPage(r.Context(), pageID, false)
	if err != nil {
		http.Error(w, "list shares: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderSharesCard(pageID, shares, s.publicURL)))
}

func (s *CloudServer) dashShareCreate(w http.ResponseWriter, r *http.Request) {
	pageID := strings.TrimSpace(r.PathValue("pageID"))
	if err := r.ParseForm(); err != nil {
		http.Error(w, "parse form", http.StatusBadRequest)
		return
	}
	uid := ""
	if claims, _ := claimsFromContext(r.Context()); claims != nil {
		uid = claims.UID
	}
	expiresIn := strings.TrimSpace(r.PostForm.Get("expires_in"))
	password := strings.TrimSpace(r.PostForm.Get("password"))
	var expiresAt *time.Time
	if d, ok := parseExpiry(expiresIn); ok {
		t := time.Now().UTC().Add(d)
		expiresAt = &t
	}
	if _, err := s.pageShares.Create(r.Context(), attachments.CreateShareParams{
		PageID:       pageID,
		Password:     password,
		ExpiresAt:    expiresAt,
		CreatedByUID: uid,
	}); err != nil {
		http.Error(w, "share: "+err.Error(), http.StatusBadRequest)
		return
	}
	shares, _ := s.pageShares.ListByPage(r.Context(), pageID, false)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderSharesCard(pageID, shares, s.publicURL)))
}

func (s *CloudServer) dashShareRevoke(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	uid := ""
	if claims, _ := claimsFromContext(r.Context()); claims != nil {
		uid = claims.UID
	}
	link, err := s.pageShares.Get(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := s.pageShares.Revoke(r.Context(), id, uid); err != nil {
		http.Error(w, "revoke: "+err.Error(), http.StatusInternalServerError)
		return
	}
	shares, _ := s.pageShares.ListByPage(r.Context(), link.PageID, false)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(renderSharesCard(link.PageID, shares, s.publicURL)))
}

// ─── HTML rendering ─────────────────────────────────────────────────────────

func renderAttachmentsCard(pageID string, atts []*attachments.Attachment, maxBytes int64) string {
	var sb strings.Builder
	sb.WriteString(`<section class="frame-section" id="attachments-card">`)
	sb.WriteString(`<p class="section-kicker">PAGE / ATTACHMENTS</p>`)
	sb.WriteString(`<h3>Attachments</h3>`)
	sb.WriteString(fmt.Sprintf(`<form method="post" action="/dashboard/pages/%s/attachments" enctype="multipart/form-data" hx-post="/dashboard/pages/%s/attachments" hx-target="#attachments-card" hx-swap="outerHTML" hx-encoding="multipart/form-data" style="margin:0.5rem 0">`,
		html.EscapeString(pageID), html.EscapeString(pageID)))
	sb.WriteString(`<input type="file" name="file" required style="margin-right:0.5rem">`)
	sb.WriteString(`<input type="text" name="description" placeholder="Optional description" style="margin-right:0.5rem">`)
	sb.WriteString(`<button type="submit">Upload</button>`)
	sb.WriteString(fmt.Sprintf(`<small style="margin-left:0.5rem;color:#888">Max %s</small>`, attachments.HumanSize(maxBytes)))
	sb.WriteString(`</form>`)

	if len(atts) == 0 {
		sb.WriteString(`<div class="empty-state"><p>No attachments yet. Upload a file to get started.</p></div>`)
	} else {
		sb.WriteString(`<table class="data-table"><thead><tr><th></th><th>File</th><th>Type</th><th>Size</th><th>Uploaded</th><th>Actions</th></tr></thead><tbody>`)
		for _, a := range atts {
			thumb := ""
			if a.ThumbnailPath != "" {
				thumb = fmt.Sprintf(`<img src="/v1/attachments/%s/thumbnail" alt="" style="width:48px;height:48px;object-fit:cover;border-radius:3px">`, html.EscapeString(a.ID))
			}
			sb.WriteString(`<tr>`)
			fmt.Fprintf(&sb, `<td>%s</td>`, thumb)
			fmt.Fprintf(&sb, `<td><a href="/v1/attachments/%s/download">%s</a></td>`,
				html.EscapeString(a.ID), html.EscapeString(a.OriginalFilename))
			fmt.Fprintf(&sb, `<td><code>%s</code></td>`, html.EscapeString(a.MIMEType))
			fmt.Fprintf(&sb, `<td>%s</td>`, html.EscapeString(attachments.HumanSize(a.SizeBytes)))
			fmt.Fprintf(&sb, `<td>%s</td>`, html.EscapeString(a.CreatedAt.Format(time.RFC3339)))
			fmt.Fprintf(&sb, `<td><button hx-post="/dashboard/attachments/%s/delete" hx-target="#attachments-card" hx-swap="outerHTML" hx-confirm="Delete this attachment?" style="background:#a00;color:#fff;border:0;padding:0.2rem 0.5rem">Delete</button></td>`,
				html.EscapeString(a.ID))
			sb.WriteString(`</tr>`)
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`</section>`)
	return sb.String()
}

func renderSharesCard(pageID string, links []*attachments.ShareLink, publicURL string) string {
	var sb strings.Builder
	sb.WriteString(`<section class="frame-section" id="shares-card">`)
	sb.WriteString(`<p class="section-kicker">PAGE / SHARE LINKS</p>`)
	sb.WriteString(`<h3>Public share links</h3>`)
	sb.WriteString(fmt.Sprintf(`<form method="post" action="/dashboard/pages/%s/shares" hx-post="/dashboard/pages/%s/shares" hx-target="#shares-card" hx-swap="outerHTML" style="margin:0.5rem 0">`,
		html.EscapeString(pageID), html.EscapeString(pageID)))
	sb.WriteString(`<label>Expires <select name="expires_in"><option value="">Never</option><option value="1h">1 hour</option><option value="24h" selected>24 hours</option><option value="7d">7 days</option><option value="30d">30 days</option></select></label> `)
	sb.WriteString(`<label>Password <input type="password" name="password" placeholder="(optional)"></label> `)
	sb.WriteString(`<button type="submit">Generate link</button>`)
	sb.WriteString(`</form>`)

	if len(links) == 0 {
		sb.WriteString(`<div class="empty-state"><p>No active share links.</p></div>`)
	} else {
		sb.WriteString(`<table class="data-table"><thead><tr><th>URL</th><th>Password?</th><th>Views</th><th>Expires</th><th>Created</th><th>Actions</th></tr></thead><tbody>`)
		for _, link := range links {
			publicLink := strings.TrimRight(publicURL, "/") + "/p/" + link.Token
			pwBadge := "no"
			if link.HasPassword {
				pwBadge = "yes"
			}
			expiry := "never"
			if link.ExpiresAt != nil {
				expiry = link.ExpiresAt.Format(time.RFC3339)
			}
			sb.WriteString(`<tr>`)
			fmt.Fprintf(&sb, `<td><a href="%s" target="_blank" rel="noopener">%s</a></td>`,
				html.EscapeString(publicLink), html.EscapeString(publicLink))
			fmt.Fprintf(&sb, `<td>%s</td>`, pwBadge)
			fmt.Fprintf(&sb, `<td>%d</td>`, link.ViewCount)
			fmt.Fprintf(&sb, `<td>%s</td>`, html.EscapeString(expiry))
			fmt.Fprintf(&sb, `<td>%s</td>`, html.EscapeString(link.CreatedAt.Format(time.RFC3339)))
			fmt.Fprintf(&sb, `<td><button hx-post="/dashboard/shares/%s/revoke" hx-target="#shares-card" hx-swap="outerHTML" hx-confirm="Revoke this link?" style="background:#a00;color:#fff;border:0;padding:0.2rem 0.5rem">Revoke</button></td>`,
				html.EscapeString(link.ID))
			sb.WriteString(`</tr>`)
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`</section>`)
	return sb.String()
}
