package dashboard

import (
	"strings"
	"testing"
)

func TestEnsureProposalPDFAssets(t *testing.T) {
	if err := EnsureProposalPDFAssets(); err != nil {
		t.Fatalf("EnsureProposalPDFAssets: %v", err)
	}
	cssText, err := loadEmbeddedCSS()
	if err != nil {
		t.Fatalf("loadEmbeddedCSS: %v", err)
	}
	if !strings.Contains(cssText, "Orbitron") {
		t.Errorf("expected Orbitron reference in embedded CSS")
	}
	fontFaces, err := loadEmbeddedFontFaces()
	if err != nil {
		t.Fatalf("loadEmbeddedFontFaces: %v", err)
	}
	if !strings.Contains(fontFaces, "data:font/ttf;base64,") {
		t.Errorf("expected base64 font data url")
	}
	if !strings.Contains(fontFaces, "Orbitron") {
		t.Errorf("expected Orbitron @font-face")
	}
	if !strings.Contains(fontFaces, "OpenSans") {
		t.Errorf("expected OpenSans @font-face")
	}
}

func TestStripExternalFontFaces(t *testing.T) {
	in := `@font-face { font-family: 'Orbitron'; src: url('/dashboard/static/fonts/Orbitron-Regular.ttf') format('truetype'); }
.body { color: red; }
@font-face { font-family: 'Other'; src: url(data:font/ttf;base64,AAA); }`
	out := stripExternalFontFaces(in)
	if strings.Contains(out, "/dashboard/static/fonts/") {
		t.Errorf("external @font-face block was not stripped: %s", out)
	}
	if !strings.Contains(out, "Other") {
		t.Errorf("data-url @font-face block should be preserved: %s", out)
	}
	if !strings.Contains(out, ".body { color: red; }") {
		t.Errorf("non font-face CSS lost: %s", out)
	}
}

func TestProposalPDFFilename(t *testing.T) {
	cases := map[string]string{
		"":                              "propuesta-sin-folio.pdf",
		"ITD-2026-PARK-001":             "propuesta-ITD-2026-PARK-001.pdf",
		"ITD 2026 Mercedes/RPA":         "propuesta-ITD-2026-MercedesRPA.pdf",
		"../../../etc/passwd":           "propuesta-...etcpasswd.pdf",
	}
	for in, want := range cases {
		got := proposalPDFFilename(in)
		if got != want {
			t.Errorf("proposalPDFFilename(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildProposalPDFDocumentEmbedsAssets(t *testing.T) {
	html, err := buildProposalPDFDocument(`<section class="proposal">body</section>`, "ITD-TEST")
	if err != nil {
		t.Fatalf("buildProposalPDFDocument: %v", err)
	}
	checks := []string{
		"<!DOCTYPE html>",
		"data:font/ttf;base64,",
		"@page { size: A4",
		`<title>Propuesta ITD-TEST</title>`,
		`<section class="proposal">body</section>`,
		".proposal-actions { display: none",
	}
	for _, want := range checks {
		if !strings.Contains(html, want) {
			t.Errorf("expected %q in PDF document", want)
		}
	}
	if strings.Contains(html, "/dashboard/static/fonts/") {
		t.Errorf("PDF document should not reference external font URLs")
	}
}
