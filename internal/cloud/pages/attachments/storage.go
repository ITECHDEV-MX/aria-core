package attachments

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// FileStorage abstracts the filesystem layer. The default implementation writes
// to a configured root, but the interface lets tests swap in an in-memory backend.
type FileStorage interface {
	// Save persists the body of r under {root}/{yyyy}/{mm}/{id}.{ext}. The hash is
	// computed during the write so callers can implement deduplication. Returns the
	// final on-disk path (relative to root), the sha256, and the byte count.
	Save(r io.Reader, id, ext string) (storagePath, sha256hex string, size int64, err error)
	// SaveAt writes data to a path computed by the caller (used for thumbnails).
	SaveAt(relPath string, data []byte) error
	// Open returns an os.File-like reader for an existing relPath.
	Open(relPath string) (io.ReadCloser, int64, error)
	// SoftDelete moves a file under {root}/tombstone/. Janitor purges after retention.
	SoftDelete(relPath string) error
	// Root reports the absolute root path (for debug/CLI feedback).
	Root() string
}

// FilesystemStorage is the default disk-backed FileStorage.
type FilesystemStorage struct {
	root string
	// dirMode controls perm bits on lazy-created directories. 0o750 by default
	// because we want the aria-core service user to read/write but no other users.
	dirMode os.FileMode
}

// NewFilesystemStorage constructs a FilesystemStorage at root, creating it
// (and the standard subdirs) if missing. dirMode defaults to 0o750.
func NewFilesystemStorage(root string) (*FilesystemStorage, error) {
	if root == "" {
		return nil, fmt.Errorf("attachments: storage root is required")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("attachments: resolve root: %w", err)
	}
	s := &FilesystemStorage{root: abs, dirMode: 0o750}
	for _, sub := range []string{"", "thumbnails", "tombstone"} {
		dir := filepath.Join(abs, sub)
		if err := os.MkdirAll(dir, s.dirMode); err != nil {
			return nil, fmt.Errorf("attachments: mkdir %q: %w", dir, err)
		}
	}
	return s, nil
}

func (s *FilesystemStorage) Root() string { return s.root }

// Save persists r under {root}/{yyyy}/{mm}/{id}{ext}. Uses os.Rename from a temp
// file in the same directory so the final write is atomic.
func (s *FilesystemStorage) Save(r io.Reader, id, ext string) (string, string, int64, error) {
	now := time.Now().UTC()
	rel := filepath.Join(fmt.Sprintf("%04d", now.Year()), fmt.Sprintf("%02d", int(now.Month())), id+ext)
	full := filepath.Join(s.root, rel)
	if err := os.MkdirAll(filepath.Dir(full), s.dirMode); err != nil {
		return "", "", 0, fmt.Errorf("attachments: mkdir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return "", "", 0, fmt.Errorf("attachments: open temp: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		// best-effort cleanup if rename fails
		_ = os.Remove(tmpPath)
	}()
	hasher := sha256.New()
	mw := io.MultiWriter(tmp, hasher)
	n, err := io.Copy(mw, r)
	if cerr := tmp.Close(); cerr != nil && err == nil {
		err = cerr
	}
	if err != nil {
		return "", "", 0, fmt.Errorf("attachments: write payload: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o640); err != nil {
		return "", "", 0, fmt.Errorf("attachments: chmod: %w", err)
	}
	if err := os.Rename(tmpPath, full); err != nil {
		return "", "", 0, fmt.Errorf("attachments: finalize rename: %w", err)
	}
	return rel, hex.EncodeToString(hasher.Sum(nil)), n, nil
}

// SaveAt writes data verbatim to relPath, used for thumbnails after rendering.
func (s *FilesystemStorage) SaveAt(relPath string, data []byte) error {
	if relPath == "" {
		return fmt.Errorf("attachments: SaveAt: empty path")
	}
	full := filepath.Join(s.root, relPath)
	if err := os.MkdirAll(filepath.Dir(full), s.dirMode); err != nil {
		return fmt.Errorf("attachments: mkdir: %w", err)
	}
	if err := os.WriteFile(full, data, 0o640); err != nil {
		return fmt.Errorf("attachments: write thumbnail: %w", err)
	}
	return nil
}

// Open returns a ReadCloser for relPath plus the file size for Content-Length.
func (s *FilesystemStorage) Open(relPath string) (io.ReadCloser, int64, error) {
	full := filepath.Join(s.root, relPath)
	f, err := os.Open(full)
	if err != nil {
		return nil, 0, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, 0, err
	}
	return f, info.Size(), nil
}

// SoftDelete moves the file to {root}/tombstone/{base} keeping the same basename
// so the janitor can later sweep by mtime. Idempotent: if the source no longer
// exists we return nil.
func (s *FilesystemStorage) SoftDelete(relPath string) error {
	if relPath == "" {
		return nil
	}
	src := filepath.Join(s.root, relPath)
	if _, err := os.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	dst := filepath.Join(s.root, "tombstone", filepath.Base(relPath))
	if err := os.MkdirAll(filepath.Dir(dst), s.dirMode); err != nil {
		return err
	}
	// If a file with the same basename already lives in tombstone (id collision
	// across years/months), suffix with timestamp so we don't blow it away.
	if _, err := os.Stat(dst); err == nil {
		dst = fmt.Sprintf("%s.%d", dst, time.Now().UTC().UnixNano())
	}
	return os.Rename(src, dst)
}

// HumanSize formats a byte count for log/UI output.
func HumanSize(n int64) string {
	const (
		k = 1 << 10
		m = 1 << 20
		g = 1 << 30
	)
	switch {
	case n >= g:
		return fmt.Sprintf("%.1f GB", float64(n)/float64(g))
	case n >= m:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(m))
	case n >= k:
		return fmt.Sprintf("%.1f KB", float64(n)/float64(k))
	default:
		return fmt.Sprintf("%d B", n)
	}
}
