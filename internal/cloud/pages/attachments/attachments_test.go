package attachments

import (
	"path/filepath"
	"strings"
	"testing"
)

// MIME validation never trusts the filename extension, but the SniffMIME helper
// can use the extension as a tiebreaker when the body is too generic to
// distinguish (e.g. a markdown file sniffs as text/plain). These tests
// document which combinations are allowed by the whitelist.
func TestIsAllowedMIME(t *testing.T) {
	cases := map[string]bool{
		"image/jpeg":               true,
		"image/png":                true,
		"image/webp":               true,
		"image/gif":                true,
		"image/svg+xml":            true,
		"text/plain":               true,
		"text/markdown":            true,
		"text/csv":                 true,
		"application/pdf":          true,
		"application/json":         true,
		"application/zip":          true,
		"application/gzip":         true,
		"application/x-tar":        true,
		"application/octet-stream": true,
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": true,
		// Disallowed:
		"video/mp4":                false,
		"audio/mpeg":               false,
		"application/x-executable": false,
		"":                         false,
	}
	for mime, want := range cases {
		got := IsAllowedMIME(mime)
		if got != want {
			t.Errorf("IsAllowedMIME(%q) = %v, want %v", mime, got, want)
		}
	}
}

// SniffMIME prefers the body sniff but falls back to the filename extension
// for octet-stream / text-plain to recover specific types.
func TestSniffMIMEPrefersExtensionForGeneric(t *testing.T) {
	// PDF magic bytes guarantee a non-generic sniff.
	pdfHead := []byte("%PDF-1.4\n1 0 obj")
	if got := SniffMIME(pdfHead, "x.pdf"); got != "application/pdf" {
		t.Errorf("pdf sniff = %q, want application/pdf", got)
	}
	// Plain text body but .md filename should resolve to markdown.
	txtHead := []byte("# heading\nsome markdown\n")
	if got := SniffMIME(txtHead, "notes.md"); got != "text/markdown" {
		t.Errorf("md fallback = %q, want text/markdown", got)
	}
	// Unknown extension keeps the body sniff.
	if got := SniffMIME(txtHead, "notes"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("unknown extension fallback = %q, want text/plain prefix", got)
	}
}

// SanitizeFilename must strip path components and unsafe characters but keep
// the extension intact for the on-disk storage path.
func TestSanitizeFilename(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"hello.pdf", "hello.pdf"},
		{"../etc/passwd", "passwd"},
		{"foo/bar.md", "bar.md"},
		{"with spaces.png", "with spaces.png"},
		{"weird💥name.png", "weird_name.png"},
		{"", "file"},
		{"   ", "file"},
	}
	for _, c := range cases {
		got := SanitizeFilename(c.in)
		if got != c.want {
			t.Errorf("SanitizeFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	// Length cap: anything over 255 gets truncated.
	long := strings.Repeat("a", 300) + ".png"
	got := SanitizeFilename(long)
	if len(got) != 255 {
		t.Errorf("SanitizeFilename long len = %d, want 255", len(got))
	}
}

func TestShouldGenerateThumbnail(t *testing.T) {
	yes := []string{"image/jpeg", "image/png", "image/gif", "image/webp", "application/pdf"}
	no := []string{"image/svg+xml", "application/zip", "text/plain", "video/mp4"}
	for _, m := range yes {
		if !ShouldGenerateThumbnail(m) {
			t.Errorf("ShouldGenerateThumbnail(%q) want true", m)
		}
	}
	for _, m := range no {
		if ShouldGenerateThumbnail(m) {
			t.Errorf("ShouldGenerateThumbnail(%q) want false", m)
		}
	}
}

// FilesystemStorage round-trips a small payload and lays it out under
// {root}/{yyyy}/{mm}/{id}{ext} as documented.
func TestFilesystemStorageRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFilesystemStorage(dir)
	if err != nil {
		t.Fatalf("NewFilesystemStorage: %v", err)
	}
	body := strings.NewReader("hello world")
	rel, sha, n, err := st.Save(body, "abc123", ".txt")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	if n != int64(len("hello world")) {
		t.Fatalf("size = %d, want 11", n)
	}
	if !strings.HasSuffix(rel, "abc123.txt") {
		t.Errorf("rel path = %q, want suffix abc123.txt", rel)
	}
	if !strings.Contains(rel, string(filepath.Separator)) {
		t.Errorf("rel path = %q, want yyyy/mm prefix", rel)
	}
	if sha == "" {
		t.Errorf("sha256 must not be empty")
	}

	// Read it back.
	f, size, err := st.Open(rel)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer f.Close()
	if size != n {
		t.Errorf("Open size = %d, want %d", size, n)
	}

	// Soft-delete moves the file to the tombstone subdir; opening it again fails.
	if err := st.SoftDelete(rel); err != nil {
		t.Fatalf("SoftDelete: %v", err)
	}
	if _, _, err := st.Open(rel); err == nil {
		t.Errorf("Open after SoftDelete: want error, got nil")
	}
}

// SaveAt writes raw bytes to a precomputed thumbnail path.
func TestFilesystemStorageSaveAt(t *testing.T) {
	dir := t.TempDir()
	st, err := NewFilesystemStorage(dir)
	if err != nil {
		t.Fatalf("NewFilesystemStorage: %v", err)
	}
	if err := st.SaveAt(filepath.Join("thumbnails", "deadbeef.jpg"), []byte("\xff\xd8\xff fake jpeg")); err != nil {
		t.Fatalf("SaveAt: %v", err)
	}
	rc, _, err := st.Open(filepath.Join("thumbnails", "deadbeef.jpg"))
	if err != nil {
		t.Fatalf("Open thumbnail: %v", err)
	}
	rc.Close()
}

// generateToken yields 64-char hex strings (32 bytes encoded) and they should
// not collide across many calls.
func TestGenerateTokenUniqueness(t *testing.T) {
	seen := map[string]struct{}{}
	for i := 0; i < 1000; i++ {
		tok, err := generateToken()
		if err != nil {
			t.Fatalf("generateToken: %v", err)
		}
		if len(tok) != 64 {
			t.Fatalf("token length = %d, want 64", len(tok))
		}
		if _, dup := seen[tok]; dup {
			t.Fatalf("duplicate token at i=%d", i)
		}
		seen[tok] = struct{}{}
	}
}

// HumanSize formats common boundaries.
func TestHumanSize(t *testing.T) {
	cases := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, c := range cases {
		if got := HumanSize(c.n); got != c.want {
			t.Errorf("HumanSize(%d) = %q, want %q", c.n, got, c.want)
		}
	}
}
