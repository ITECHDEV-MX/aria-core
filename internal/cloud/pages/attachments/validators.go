package attachments

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"
)

// ErrMIMENotAllowed is returned when an uploaded file's detected MIME is not in
// the whitelist. The HTTP layer maps this to 415 Unsupported Media Type.
var ErrMIMENotAllowed = errors.New("attachments: mime type not allowed")

// ErrFileTooBig signals a payload exceeding MaxBytes.
var ErrFileTooBig = errors.New("attachments: file too big")

// ErrEmptyFile is returned when the upload has zero bytes.
var ErrEmptyFile = errors.New("attachments: empty file")

// allowedMIMEPrefixes covers families (image/*) without listing every codec.
var allowedMIMEPrefixes = []string{
	"image/",
	"text/",
}

// allowedMIMETypes is the explicit whitelist for non-prefix matches: documents,
// archives. Sniffed types are matched against this list AFTER prefix matching.
var allowedMIMETypes = map[string]struct{}{
	"application/pdf":    {},
	"application/json":   {},
	"application/zip":    {},
	"application/x-zip":  {},
	"application/gzip":   {},
	"application/x-gzip": {},
	"application/x-tar":  {},
	"application/x-7z-compressed":                                              {},
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document":  {}, // .docx
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":        {}, // .xlsx
	"application/vnd.openxmlformats-officedocument.presentationml.presentation": {}, // .pptx
	"application/msword":              {},
	"application/vnd.ms-excel":        {},
	"application/vnd.ms-powerpoint":   {},
	"application/octet-stream":        {}, // generic binary; allowed but not preferred
}

// extensionToMIME provides a tighter lookup for files whose body sniff returns
// a generic value (octet-stream / text/plain) but whose extension is well known.
// This is purely informative: filename extension is NEVER trusted as ground truth
// — it only serves to RECOVER a more specific MIME when sniffing is ambiguous.
var extensionToMIME = map[string]string{
	".jpg":   "image/jpeg",
	".jpeg":  "image/jpeg",
	".png":   "image/png",
	".webp":  "image/webp",
	".gif":   "image/gif",
	".svg":   "image/svg+xml",
	".pdf":   "application/pdf",
	".md":    "text/markdown",
	".txt":   "text/plain",
	".csv":   "text/csv",
	".json":  "application/json",
	".docx":  "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xlsx":  "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".pptx":  "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".doc":   "application/msword",
	".xls":   "application/vnd.ms-excel",
	".ppt":   "application/vnd.ms-powerpoint",
	".zip":   "application/zip",
	".tar":   "application/x-tar",
	".gz":    "application/gzip",
	".tgz":   "application/gzip",
	".7z":    "application/x-7z-compressed",
}

// SniffMIME inspects the first 512 bytes (per net/http convention) and returns
// the canonical MIME type. If sniffing yields a generic value, fallback to the
// filename extension for a tighter match. The returned MIME is the value to
// validate against the whitelist.
func SniffMIME(head []byte, filename string) string {
	sniffed := strings.TrimSpace(http.DetectContentType(head))
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = strings.TrimSpace(sniffed[:i])
	}
	// If the sniff is too generic, try the extension table.
	if sniffed == "" || sniffed == "application/octet-stream" || sniffed == "text/plain" {
		ext := strings.ToLower(filepath.Ext(filename))
		if alt, ok := extensionToMIME[ext]; ok {
			return alt
		}
	}
	return sniffed
}

// IsAllowedMIME returns true iff mime is whitelisted by prefix or exact match.
func IsAllowedMIME(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if mime == "" {
		return false
	}
	for _, p := range allowedMIMEPrefixes {
		if strings.HasPrefix(mime, p) {
			return true
		}
	}
	if _, ok := allowedMIMETypes[mime]; ok {
		return true
	}
	return false
}

// SanitizeFilename strips path components and unsafe characters. Result is
// at most 255 chars and is safe to embed in storage paths or HTTP headers.
func SanitizeFilename(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "file"
	}
	// Drop any directory portion; clients sometimes upload "foo/bar.pdf".
	name = filepath.Base(name)
	// Replace unsafe runes.
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '-', r == '_', r == '(', r == ')', r == ' ':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), " .")
	if out == "" {
		out = "file"
	}
	if len(out) > 255 {
		out = out[:255]
	}
	return out
}

// ShouldGenerateThumbnail returns true iff the MIME is one we know how to render.
func ShouldGenerateThumbnail(mime string) bool {
	mime = strings.ToLower(strings.TrimSpace(mime))
	if strings.HasPrefix(mime, "image/") {
		// SVGs we leave alone; rendering would require a headless browser.
		return mime != "image/svg+xml"
	}
	return mime == "application/pdf"
}
