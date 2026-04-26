package dashboard

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a-h/templ"
)

// handleCotizadorQuoteProposalPDF serves the proposal as a real PDF rendered
// via gotenberg. The handler renders the same templ component used by the
// HTML proposal view, wraps it in a self-contained <html> document with
// inlined CSS + base64-embedded fonts (gotenberg runs in a separate
// container with no access to the host filesystem), and POSTs the resulting
// HTML to gotenberg's /forms/chromium/convert/html endpoint.
//
// Page setup: A4 (8.27 x 11.7 inches), 0.5 inch margins, printBackground=true
// so the dark theme is preserved. Generation timeout: 30s.
func (h *handlers) handleCotizadorQuoteProposalPDF(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Cotizador == nil {
		http.Error(w, "cotizador module not configured", http.StatusServiceUnavailable)
		return
	}
	if h.cfg.PDFClient == nil {
		http.Error(w, "PDF export no configurado", http.StatusServiceUnavailable)
		return
	}
	quoteID := r.PathValue("quoteID")
	q, err := h.cfg.Cotizador.GetQuote(r.Context(), quoteID)
	if err != nil {
		http.Error(w, "quote not found", http.StatusNotFound)
		return
	}
	items, _ := h.cfg.Cotizador.ListQuoteItems(r.Context(), quoteID)
	sections, _ := h.cfg.Cotizador.ListSections(r.Context(), quoteID)

	rendered := make([]renderedSection, 0, len(sections))
	for _, s := range sections {
		rendered = append(rendered, renderedSection{
			Key:   s.Key,
			Title: s.Title,
			HTML:  renderMarkdown(s.ContentMD),
			RawMD: s.ContentMD,
		})
	}

	component := CotizadorProposalView(q, items, rendered)

	// Render the proposal component into a buffer.
	var bodyBuf bytes.Buffer
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := component.Render(ctx, &bodyBuf); err != nil {
		http.Error(w, fmt.Sprintf("render proposal: %v", err), http.StatusInternalServerError)
		return
	}

	// Wrap into a full HTML document with inlined CSS and embedded fonts.
	htmlDoc, err := buildProposalPDFDocument(bodyBuf.String(), q.Folio)
	if err != nil {
		http.Error(w, fmt.Sprintf("build PDF document: %v", err), http.StatusInternalServerError)
		return
	}

	pdfBytes, err := h.cfg.PDFClient.ConvertHTML(ctx, []byte(htmlDoc), PDFConvertOptions{
		PaperWidth:      8.27, // A4 width in inches
		PaperHeight:     11.7, // A4 height in inches
		MarginTop:       0.5,
		MarginBottom:    0.5,
		MarginLeft:      0.5,
		MarginRight:     0.5,
		PrintBackground: true,
	})
	if err != nil {
		http.Error(w, fmt.Sprintf("gotenberg convert: %v", err), http.StatusBadGateway)
		return
	}

	filename := proposalPDFFilename(q.Folio)
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(pdfBytes)))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(pdfBytes)
}

// proposalPDFFilename returns a safe filename based on the folio.
func proposalPDFFilename(folio string) string {
	clean := strings.TrimSpace(folio)
	if clean == "" {
		clean = "sin-folio"
	}
	// Pass 1: keep ASCII alnum, dash, underscore, dot; spaces become dashes;
	// everything else (slashes, etc) is dropped.
	var first strings.Builder
	for _, r := range clean {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			first.WriteRune(r)
		case r == ' ':
			first.WriteRune('-')
		}
	}
	// Pass 2: collapse runs of consecutive dots (after dropping slashes)
	// to max 3 dots, neutralizing path-traversal payloads.
	var b strings.Builder
	dotRun := 0
	for _, r := range first.String() {
		if r == '.' {
			dotRun++
			if dotRun <= 3 {
				b.WriteRune(r)
			}
		} else {
			dotRun = 0
			b.WriteRune(r)
		}
	}
	cleaned := b.String()
	if cleaned == "" {
		cleaned = "sin-folio"
	}
	return "propuesta-" + cleaned + ".pdf"
}

// buildProposalPDFDocument wraps the rendered proposal body in a full HTML5
// document with inlined dashboard CSS and base64-embedded TTF fonts so that
// gotenberg (in a separate container) does not need filesystem access.
func buildProposalPDFDocument(body, folio string) (string, error) {
	cssText, err := loadEmbeddedCSS()
	if err != nil {
		return "", err
	}
	fontFaceCSS, err := loadEmbeddedFontFaces()
	if err != nil {
		return "", err
	}

	// Strip the @font-face blocks from aria-styles.css that reference
	// /dashboard/static/fonts/* — gotenberg cannot fetch them. The base64
	// versions emitted by loadEmbeddedFontFaces replace them.
	cssText = stripExternalFontFaces(cssText)

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>` + "\n")
	sb.WriteString(`<html lang="es"><head>` + "\n")
	sb.WriteString(`<meta charset="utf-8"/>` + "\n")
	sb.WriteString(`<title>Propuesta ` + templ.EscapeString(folio) + `</title>` + "\n")
	sb.WriteString(`<style>` + "\n")
	sb.WriteString(fontFaceCSS)
	sb.WriteString("\n")
	sb.WriteString(cssText)
	sb.WriteString("\n")
	sb.WriteString(pdfPrintOverridesCSS)
	sb.WriteString("</style>\n")
	sb.WriteString(`</head><body class="proposal-pdf-body">` + "\n")
	sb.WriteString(body)
	sb.WriteString(`</body></html>` + "\n")
	return sb.String(), nil
}

// pdfPrintOverridesCSS adds a minimal set of print-friendly overrides that
// only apply when rendered to PDF. We hide the in-page action bar, remove
// box-shadows that look bad on paper, and pin @page size to A4.
const pdfPrintOverridesCSS = `
@page { size: A4; margin: 0.5in; }
html, body { background: #0d0d0d; }
.proposal-pdf-body { margin: 0; padding: 0; font-family: 'OpenSans', system-ui, sans-serif; }
.proposal-actions { display: none !important; }
.proposal { max-width: 100% !important; padding: 0 !important; margin: 0 !important; }
.proposal-section { page-break-inside: avoid; }
`

// stripExternalFontFaces removes @font-face blocks from the dashboard CSS
// that point to /dashboard/static/fonts/*.ttf — gotenberg has no access to
// those URLs, so we replace them with base64-embedded data: URLs.
func stripExternalFontFaces(css string) string {
	const marker = "/dashboard/static/fonts/"
	if !strings.Contains(css, marker) {
		return css
	}
	var out strings.Builder
	out.Grow(len(css))
	i := 0
	for i < len(css) {
		idx := strings.Index(css[i:], "@font-face")
		if idx < 0 {
			out.WriteString(css[i:])
			break
		}
		blockStart := i + idx
		// Write everything before the @font-face block.
		out.WriteString(css[i:blockStart])
		// Find the matching closing brace for this @font-face block.
		braceStart := strings.Index(css[blockStart:], "{")
		if braceStart < 0 {
			// Malformed; bail out and keep the rest verbatim.
			out.WriteString(css[blockStart:])
			break
		}
		braceStart += blockStart
		depth := 0
		end := -1
		for j := braceStart; j < len(css); j++ {
			switch css[j] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = j + 1
				}
			}
			if end > 0 {
				break
			}
		}
		if end < 0 {
			out.WriteString(css[blockStart:])
			break
		}
		block := css[blockStart:end]
		if !strings.Contains(block, marker) {
			// Unrelated @font-face block — keep it.
			out.WriteString(block)
		}
		// else: drop the block entirely; its replacement is emitted by
		// loadEmbeddedFontFaces.
		i = end
	}
	return out.String()
}

// embeddedFontFamily ties a font filename inside static/fonts/ to its
// CSS @font-face descriptor.
type embeddedFontFamily struct {
	Filename string
	Family   string
	Weight   int
	Style    string // "normal" or "italic"
}

var proposalEmbeddedFonts = []embeddedFontFamily{
	{"fonts/Orbitron-Regular.ttf", "Orbitron", 400, "normal"},
	{"fonts/Orbitron-Medium.ttf", "Orbitron", 500, "normal"},
	{"fonts/Orbitron-SemiBold.ttf", "Orbitron", 600, "normal"},
	{"fonts/Orbitron-Bold.ttf", "Orbitron", 700, "normal"},
	{"fonts/OpenSans-Regular.ttf", "OpenSans", 400, "normal"},
	{"fonts/OpenSans-Medium.ttf", "OpenSans", 500, "normal"},
	{"fonts/OpenSans-SemiBold.ttf", "OpenSans", 600, "normal"},
	{"fonts/OpenSans-Bold.ttf", "OpenSans", 700, "normal"},
}

var (
	embeddedAssetsCache struct {
		sync.Once
		css       string
		fontFaces string
		err       error
	}
)

func loadEmbeddedCSS() (string, error) {
	primeEmbeddedAssets()
	return embeddedAssetsCache.css, embeddedAssetsCache.err
}

func loadEmbeddedFontFaces() (string, error) {
	primeEmbeddedAssets()
	return embeddedAssetsCache.fontFaces, embeddedAssetsCache.err
}

func primeEmbeddedAssets() {
	embeddedAssetsCache.Do(func() {
		f, err := StaticFS.Open("static/aria-styles.css")
		if err != nil {
			embeddedAssetsCache.err = fmt.Errorf("open aria-styles.css: %w", err)
			return
		}
		defer f.Close()
		cssBytes, err := io.ReadAll(f)
		if err != nil {
			embeddedAssetsCache.err = fmt.Errorf("read aria-styles.css: %w", err)
			return
		}
		embeddedAssetsCache.css = string(cssBytes)

		var fb strings.Builder
		for _, font := range proposalEmbeddedFonts {
			ttf, err := StaticFS.ReadFile("static/" + font.Filename)
			if err != nil {
				embeddedAssetsCache.err = fmt.Errorf("read embedded font %s: %w", font.Filename, err)
				return
			}
			b64 := base64.StdEncoding.EncodeToString(ttf)
			fb.WriteString("@font-face {\n")
			fb.WriteString(fmt.Sprintf("  font-family: '%s';\n", font.Family))
			fb.WriteString(fmt.Sprintf("  font-weight: %d;\n", font.Weight))
			fb.WriteString(fmt.Sprintf("  font-style: %s;\n", font.Style))
			fb.WriteString("  font-display: swap;\n")
			fb.WriteString(fmt.Sprintf("  src: url(data:font/ttf;base64,%s) format('truetype');\n", b64))
			fb.WriteString("}\n")
		}
		embeddedAssetsCache.fontFaces = fb.String()
	})
}

// EnsureProposalPDFAssets warms up the embedded asset cache. Useful for
// tests that want to surface load errors deterministically.
func EnsureProposalPDFAssets() error {
	primeEmbeddedAssets()
	return embeddedAssetsCache.err
}

