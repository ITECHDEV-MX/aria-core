// Wiring for the page attachments + share modules.
//
// The cloudserver expects a PageAttachmentService and PageShareService; we
// provide thin pass-through adapters around the in-package stores. The
// adapters also own:
//   - Resolving ARIA_CORE_ATTACHMENTS_DIR / ARIA_CORE_ATTACHMENTS_MAX_MB.
//   - Async thumbnail generator goroutine.
//   - Page sensitivity resolver (best-effort SELECT against aria_pages —
//     tolerant when the table is missing).
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudserver"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/attachments"
)

// pageAttachmentsAdapter wraps the persistence + storage layers and satisfies
// cloudserver.PageAttachmentService. Same instance is held by the wiring code
// so the thumbnail callback can call back into UpdateThumbnailPath.
type pageAttachmentsAdapter struct {
	store *attachments.AttachmentStore
}

// newPageAttachmentsAdapter constructs the storage + store wiring. Returns nil
// when attachments are disabled (root unset and default fallback unavailable).
func newPageAttachmentsAdapter(cs *cloudstore.CloudStore) (*pageAttachmentsAdapter, error) {
	root := strings.TrimSpace(os.Getenv("ARIA_CORE_ATTACHMENTS_DIR"))
	if root == "" {
		root = defaultAttachmentsRoot()
	}
	storage, err := attachments.NewFilesystemStorage(root)
	if err != nil {
		return nil, fmt.Errorf("attachments: %w", err)
	}
	maxBytes := int64(attachments.DefaultMaxBytes)
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_ATTACHMENTS_MAX_MB")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxBytes = int64(n) * 1024 * 1024
		}
	}
	a := &pageAttachmentsAdapter{}
	cfg := attachments.Config{
		MaxBytes: maxBytes,
		ThumbnailGenerator: func(att *attachments.Attachment) {
			generateThumbnailAsync(a, att)
		},
	}
	a.store = attachments.NewAttachmentStore(cs.DB(), storage, cfg)
	log.Printf("[aria-core-cloud] page attachments ready (root=%s, max=%s)", root, attachments.HumanSize(maxBytes))
	return a, nil
}

// defaultAttachmentsRoot returns the most appropriate path when no env var is
// set: /var/lib/aria-core/attachments on Linux, $HOME/.aria-core/attachments
// elsewhere. Tests use t.TempDir directly so they never hit this.
func defaultAttachmentsRoot() string {
	if _, err := os.Stat("/var/lib/aria-core"); err == nil {
		return "/var/lib/aria-core/attachments"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "aria-core-attachments")
	}
	return filepath.Join(home, ".aria-core", "attachments")
}

// generateThumbnailAsync is invoked by AttachmentStore in a goroutine. It
// renders a JPEG thumb to {root}/thumbnails/{id}.jpg and updates the DB row.
func generateThumbnailAsync(a *pageAttachmentsAdapter, att *attachments.Attachment) {
	if a == nil || att == nil {
		return
	}
	storage := a.store.Storage()
	fs, ok := storage.(*attachments.FilesystemStorage)
	if !ok {
		return
	}
	src := filepath.Join(fs.Root(), att.StoragePath)
	dst := filepath.Join(fs.Root(), "thumbnails", att.ID+".jpg")
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		log.Printf("attachments: thumbnail mkdir: %v", err)
		return
	}
	if err := attachments.GenerateThumbnail(context.Background(), att.MIMEType, src, dst); err != nil {
		if !errors.Is(err, attachments.ErrNoPDFRenderer) {
			log.Printf("attachments: thumbnail %s: %v", att.ID, err)
		}
		return
	}
	rel := filepath.Join("thumbnails", att.ID+".jpg")
	if err := a.store.UpdateThumbnailPath(context.Background(), att.ID, rel); err != nil {
		log.Printf("attachments: update thumbnail_path: %v", err)
	}
}

// PageAttachmentService implementation ───────────────────────────────────────

func (a *pageAttachmentsAdapter) Upload(ctx context.Context, p attachments.UploadParams) (*attachments.Attachment, error) {
	return a.store.Upload(ctx, p)
}
func (a *pageAttachmentsAdapter) Get(ctx context.Context, id string) (*attachments.Attachment, error) {
	return a.store.Get(ctx, id)
}
func (a *pageAttachmentsAdapter) ListByPage(ctx context.Context, pageID string) ([]*attachments.Attachment, error) {
	return a.store.ListByPage(ctx, pageID)
}
func (a *pageAttachmentsAdapter) OpenContent(att *attachments.Attachment) (io.ReadCloser, int64, error) {
	return a.store.OpenContent(att)
}
func (a *pageAttachmentsAdapter) OpenThumbnail(att *attachments.Attachment) (io.ReadCloser, int64, error) {
	return a.store.OpenThumbnail(att)
}
func (a *pageAttachmentsAdapter) Delete(ctx context.Context, id, byUID string) error {
	return a.store.Delete(ctx, id, byUID)
}
func (a *pageAttachmentsAdapter) MaxBytes() int64 { return a.store.MaxBytes() }

var _ cloudserver.PageAttachmentService = (*pageAttachmentsAdapter)(nil)

// pageSharesAdapter wraps the share store. Sensitivity resolver queries
// aria_pages.sensitivity if the table exists; on any error we let the share
// proceed (the dashboard layer is the second line of defense).
type pageSharesAdapter struct {
	store *attachments.PgShareStore
}

func newPageSharesAdapter(cs *cloudstore.CloudStore) *pageSharesAdapter {
	st := attachments.NewPgShareStore(cs.DB())
	st.SetSensitivityResolver(makeSensitivityResolver(cs.DB()))
	return &pageSharesAdapter{store: st}
}

func makeSensitivityResolver(db *sql.DB) func(ctx context.Context, pageID string) (string, error) {
	return func(ctx context.Context, pageID string) (string, error) {
		var sens sql.NullString
		err := db.QueryRowContext(ctx, `SELECT COALESCE(sensitivity, '') FROM aria_pages WHERE id = $1::uuid`, pageID).Scan(&sens)
		if err != nil {
			// Treat missing table / row as "unknown" — share creation proceeds.
			if strings.Contains(err.Error(), "aria_pages") || errors.Is(err, sql.ErrNoRows) {
				return "", nil
			}
			return "", err
		}
		return sens.String, nil
	}
}

// PageShareService implementation ────────────────────────────────────────────

func (a *pageSharesAdapter) Create(ctx context.Context, p attachments.CreateShareParams) (*attachments.ShareLink, error) {
	return a.store.Create(ctx, p)
}
func (a *pageSharesAdapter) Get(ctx context.Context, id string) (*attachments.ShareLink, error) {
	return a.store.Get(ctx, id)
}
func (a *pageSharesAdapter) GetByToken(ctx context.Context, token string) (*attachments.ShareLink, error) {
	return a.store.GetByToken(ctx, token)
}
func (a *pageSharesAdapter) ListByPage(ctx context.Context, pageID string, includeRevoked bool) ([]*attachments.ShareLink, error) {
	return a.store.ListByPage(ctx, pageID, includeRevoked)
}
func (a *pageSharesAdapter) Revoke(ctx context.Context, id, byUID string) error {
	return a.store.Revoke(ctx, id, byUID)
}
func (a *pageSharesAdapter) Resolve(ctx context.Context, token, password, ip, ua string) (*attachments.ShareLink, error) {
	return a.store.Resolve(ctx, token, password, ip, ua)
}

var _ cloudserver.PageShareService = (*pageSharesAdapter)(nil)

// pagePublicViewAdapter satisfies cloudserver.PagePublicViewService. The
// implementation queries aria_pages directly. If the table does not yet exist
// (PAGES module not deployed) we return a "not available" error so the public
// route degrades gracefully.
type pagePublicViewAdapter struct {
	db          *sql.DB
	attachments *pageAttachmentsAdapter
}

func newPagePublicViewAdapter(cs *cloudstore.CloudStore, atts *pageAttachmentsAdapter) *pagePublicViewAdapter {
	return &pagePublicViewAdapter{db: cs.DB(), attachments: atts}
}

func (a *pagePublicViewAdapter) PageMarkdown(ctx context.Context, pageID string) (string, string, string, error) {
	var title, content, sens sql.NullString
	err := a.db.QueryRowContext(ctx, `
		SELECT COALESCE(title,''), COALESCE(content_md,''), COALESCE(sensitivity,'')
		FROM aria_pages
		WHERE id = $1::uuid
	`, pageID).Scan(&title, &content, &sens)
	if err != nil {
		if strings.Contains(err.Error(), "aria_pages") {
			return "", "", "", fmt.Errorf("public pages not available (aria_pages table not present): %w", err)
		}
		return "", "", "", err
	}
	return title.String, content.String, sens.String, nil
}

func (a *pagePublicViewAdapter) AttachmentsForPage(ctx context.Context, pageID string) ([]*attachments.Attachment, error) {
	if a.attachments == nil {
		return nil, nil
	}
	return a.attachments.ListByPage(ctx, pageID)
}

var _ cloudserver.PagePublicViewService = (*pagePublicViewAdapter)(nil)

// pageAttachmentsServiceOrNil returns nil interface (not nil pointer wrapped in
// interface) when the adapter is unavailable. This avoids the classic Go
// gotcha where (interface{}{}(*T)(nil)) is non-nil.
func pageAttachmentsServiceOrNil(a *pageAttachmentsAdapter) cloudserver.PageAttachmentService {
	if a == nil {
		return nil
	}
	return a
}

func pagePublicViewServiceOrNil(a *pagePublicViewAdapter) cloudserver.PagePublicViewService {
	if a == nil {
		return nil
	}
	return a
}

