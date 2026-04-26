package attachments

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ErrAttachmentNotFound is returned when an id does not match an extant row.
var ErrAttachmentNotFound = errors.New("attachments: not found")

// DefaultMaxBytes is the default upload ceiling (25 MB) when the runtime config
// does not specify ARIA_CORE_ATTACHMENTS_MAX_MB.
const DefaultMaxBytes = 25 * 1024 * 1024

// Attachment is the runtime representation of an aria_page_attachments row.
type Attachment struct {
	ID                string
	PageID            string
	Filename          string
	OriginalFilename  string
	MIMEType          string
	SizeBytes         int64
	StoragePath       string
	ThumbnailPath     string
	SHA256            string
	UploadedByUID     string
	Description       string
	IsDeleted         bool
	CreatedAt         time.Time
}

// UploadParams is the argument tuple for AttachmentStore.Upload.
type UploadParams struct {
	PageID           string
	OriginalFilename string
	Body             io.Reader
	UploadedByUID    string
	Description      string
}

// Config holds tunables for the AttachmentStore. MaxBytes <= 0 falls back to
// DefaultMaxBytes; ThumbnailGenerator nil disables async thumbnail rendering.
type Config struct {
	MaxBytes int64
	// ThumbnailGenerator is invoked in a goroutine after a successful upload.
	// It is set by the wiring layer to a function that calls GenerateThumbnail
	// + SaveAt + UpdateThumbnailPath. Tests can stub it to nil.
	ThumbnailGenerator func(att *Attachment)
}

// AttachmentStore is the orchestration layer that combines DB persistence,
// filesystem storage, MIME validation, and dedup-by-sha256.
type AttachmentStore struct {
	db      *sql.DB
	storage FileStorage
	cfg     Config
}

// NewAttachmentStore wires the given DB + storage. cfg is normalized in-place.
func NewAttachmentStore(db *sql.DB, storage FileStorage, cfg Config) *AttachmentStore {
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultMaxBytes
	}
	return &AttachmentStore{db: db, storage: storage, cfg: cfg}
}

// Upload buffers the first 512 bytes for MIME sniffing, then streams the body
// through to disk while computing sha256. Enforces MaxBytes and the MIME
// whitelist. On success the attachment row is inserted and a goroutine kicks
// off thumbnail generation (when the MIME is image/* or application/pdf).
//
// Dedup: if a row with the same sha256 + page_id already exists, we return that
// row instead of writing a duplicate file. The byte stream is still consumed
// (caller's reader position advances) but the file copy is short-circuited.
func (s *AttachmentStore) Upload(ctx context.Context, p UploadParams) (*Attachment, error) {
	if strings.TrimSpace(p.PageID) == "" {
		return nil, fmt.Errorf("attachments: upload: page_id required")
	}
	if strings.TrimSpace(p.UploadedByUID) == "" {
		return nil, fmt.Errorf("attachments: upload: uploaded_by_uid required")
	}
	if p.Body == nil {
		return nil, ErrEmptyFile
	}

	limited := io.LimitReader(p.Body, s.cfg.MaxBytes+1)

	// Sniff MIME from the first 512 bytes.
	head := make([]byte, 512)
	n, err := io.ReadFull(limited, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("attachments: read head: %w", err)
	}
	head = head[:n]
	if n == 0 {
		return nil, ErrEmptyFile
	}
	mime := SniffMIME(head, p.OriginalFilename)
	if !IsAllowedMIME(mime) {
		return nil, fmt.Errorf("%w: %s", ErrMIMENotAllowed, mime)
	}

	// Stitch head back onto the rest of the stream so the storage layer sees
	// the full payload. This double-buffers the first 512 bytes only — the rest
	// is streamed.
	full := io.MultiReader(bytes.NewReader(head), limited)

	id := uuid.NewString()
	sanitizedName := SanitizeFilename(p.OriginalFilename)
	ext := strings.ToLower(filepath.Ext(sanitizedName))
	if ext == "" {
		ext = mimeToExt(mime)
	}
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	// Wrap the storage writer with a size cap so an oversized payload aborts
	// before disk fills up. We pre-cap at MaxBytes+1 with the io.LimitReader,
	// then verify the actual byte count after the write completes.
	relPath, sha, written, err := s.storage.Save(full, id, ext)
	if err != nil {
		return nil, err
	}
	if written > s.cfg.MaxBytes {
		_ = s.storage.SoftDelete(relPath)
		return nil, fmt.Errorf("%w: limit=%d got=%d", ErrFileTooBig, s.cfg.MaxBytes, written)
	}

	// Dedup: if (page_id, sha256) already exists & not deleted, drop new file.
	if existing, err := s.findBySHA(ctx, p.PageID, sha); err == nil && existing != nil {
		_ = s.storage.SoftDelete(relPath)
		return existing, nil
	}

	// Persist row. We don't open a transaction because the storage write is the
	// source of truth — if the INSERT fails after, the file becomes orphan and
	// the janitor sweeps it on next pass.
	row := s.db.QueryRowContext(ctx, `
		INSERT INTO aria_page_attachments
		  (id, page_id, filename, original_filename, mime_type, size_bytes,
		   storage_path, sha256, uploaded_by_uid, description)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5, $6, $7, $8, $9::uuid, NULLIF($10,''))
		RETURNING id::text, page_id::text, filename, original_filename, mime_type,
		          size_bytes, storage_path, COALESCE(thumbnail_path,''), sha256,
		          uploaded_by_uid::text, COALESCE(description,''), is_deleted, created_at
	`, id, p.PageID, sanitizedName, p.OriginalFilename, mime, written,
		relPath, sha, p.UploadedByUID, strings.TrimSpace(p.Description))
	att, err := scanAttachment(row)
	if err != nil {
		_ = s.storage.SoftDelete(relPath)
		return nil, fmt.Errorf("attachments: insert row: %w", err)
	}

	// Async thumbnail.
	if ShouldGenerateThumbnail(mime) && s.cfg.ThumbnailGenerator != nil {
		go s.cfg.ThumbnailGenerator(att)
	}
	return att, nil
}

// Get returns metadata for a single attachment. Soft-deleted rows are also
// returned (with IsDeleted=true) so the dashboard can surface tombstones if
// needed.
func (s *AttachmentStore) Get(ctx context.Context, id string) (*Attachment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, page_id::text, filename, original_filename, mime_type,
		       size_bytes, storage_path, COALESCE(thumbnail_path,''), sha256,
		       uploaded_by_uid::text, COALESCE(description,''), is_deleted, created_at
		FROM aria_page_attachments WHERE id = $1::uuid`, id)
	att, err := scanAttachment(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrAttachmentNotFound
	}
	return att, err
}

// ListByPage returns alive attachments for the page, newest first.
func (s *AttachmentStore) ListByPage(ctx context.Context, pageID string) ([]*Attachment, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id::text, page_id::text, filename, original_filename, mime_type,
		       size_bytes, storage_path, COALESCE(thumbnail_path,''), sha256,
		       uploaded_by_uid::text, COALESCE(description,''), is_deleted, created_at
		FROM aria_page_attachments
		WHERE page_id = $1::uuid AND NOT is_deleted
		ORDER BY created_at DESC LIMIT 500`, pageID)
	if err != nil {
		return nil, fmt.Errorf("attachments: list: %w", err)
	}
	defer rows.Close()
	out := []*Attachment{}
	for rows.Next() {
		att, err := scanAttachment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, att)
	}
	return out, rows.Err()
}

// OpenContent returns a streaming reader for the original payload + size.
func (s *AttachmentStore) OpenContent(att *Attachment) (io.ReadCloser, int64, error) {
	if att == nil {
		return nil, 0, ErrAttachmentNotFound
	}
	return s.storage.Open(att.StoragePath)
}

// OpenThumbnail returns the cached thumbnail or (nil, 0, os.ErrNotExist).
func (s *AttachmentStore) OpenThumbnail(att *Attachment) (io.ReadCloser, int64, error) {
	if att == nil || att.ThumbnailPath == "" {
		return nil, 0, ErrAttachmentNotFound
	}
	return s.storage.Open(att.ThumbnailPath)
}

// Delete soft-deletes the row and moves the on-disk file to a tombstone dir.
// The thumbnail is also tombstoned.
func (s *AttachmentStore) Delete(ctx context.Context, id, byUID string) error {
	att, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if att.IsDeleted {
		return nil
	}
	if _, err := s.db.ExecContext(ctx, `
		UPDATE aria_page_attachments
		SET is_deleted = TRUE, deleted_at = NOW()
		WHERE id = $1::uuid`, id); err != nil {
		return fmt.Errorf("attachments: soft delete: %w", err)
	}
	_ = s.storage.SoftDelete(att.StoragePath)
	if att.ThumbnailPath != "" {
		_ = s.storage.SoftDelete(att.ThumbnailPath)
	}
	return nil
}

// UpdateThumbnailPath is invoked by the async generator goroutine to record the
// finished thumbnail location. Calling it for a row that no longer exists is
// silently no-op.
func (s *AttachmentStore) UpdateThumbnailPath(ctx context.Context, id, relPath string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE aria_page_attachments SET thumbnail_path = $1 WHERE id = $2::uuid`,
		relPath, id,
	)
	return err
}

// Storage exposes the underlying FileStorage so the wiring layer's thumbnail
// callback can reach the disk to write its output.
func (s *AttachmentStore) Storage() FileStorage { return s.storage }

// MaxBytes returns the effective upload ceiling.
func (s *AttachmentStore) MaxBytes() int64 { return s.cfg.MaxBytes }

func (s *AttachmentStore) findBySHA(ctx context.Context, pageID, sha string) (*Attachment, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id::text, page_id::text, filename, original_filename, mime_type,
		       size_bytes, storage_path, COALESCE(thumbnail_path,''), sha256,
		       uploaded_by_uid::text, COALESCE(description,''), is_deleted, created_at
		FROM aria_page_attachments
		WHERE page_id = $1::uuid AND sha256 = $2 AND NOT is_deleted
		LIMIT 1`, pageID, sha)
	return scanAttachment(row)
}

func mimeToExt(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/svg+xml":
		return ".svg"
	case "application/pdf":
		return ".pdf"
	case "application/json":
		return ".json"
	case "text/markdown":
		return ".md"
	case "text/plain":
		return ".txt"
	case "text/csv":
		return ".csv"
	case "application/zip":
		return ".zip"
	case "application/gzip":
		return ".gz"
	case "application/x-tar":
		return ".tar"
	}
	return ""
}

type rowScanner interface {
	Scan(...any) error
}

func scanAttachment(r rowScanner) (*Attachment, error) {
	var a Attachment
	if err := r.Scan(
		&a.ID, &a.PageID, &a.Filename, &a.OriginalFilename, &a.MIMEType,
		&a.SizeBytes, &a.StoragePath, &a.ThumbnailPath, &a.SHA256,
		&a.UploadedByUID, &a.Description, &a.IsDeleted, &a.CreatedAt,
	); err != nil {
		return nil, err
	}
	return &a, nil
}
