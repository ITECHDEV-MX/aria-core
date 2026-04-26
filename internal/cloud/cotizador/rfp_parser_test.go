package cotizador

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAttachments struct {
	filename string
	mime     string
	size     int64
	body     string
	err      error
}

func (f *fakeAttachments) GetMeta(_ context.Context, _ string) (string, string, int64, error) {
	if f.err != nil {
		return "", "", 0, f.err
	}
	return f.filename, f.mime, f.size, nil
}

func (f *fakeAttachments) OpenContent(_ context.Context, _ string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader(f.body)), nil
}

type fakeSummarizer struct{ result string; err error }

func (f *fakeSummarizer) Summarize(_ context.Context, _ string, _ string) (string, error) {
	return f.result, f.err
}

type fakeExtractor struct{ result string; err error; called bool }

func (f *fakeExtractor) ExtractText(_ context.Context, _ string, body io.Reader) (string, error) {
	f.called = true
	_, _ = io.Copy(io.Discard, body)
	if f.err != nil {
		return "", f.err
	}
	return f.result, nil
}

func TestRFPParserTextMime(t *testing.T) {
	att := &fakeAttachments{
		filename: "rfp.txt",
		mime:     "text/plain",
		body:     "Necesitamos integración SAP con SF, deadline Q3.",
	}
	sum := &fakeSummarizer{result: "RFP integración SAP-SF, Q3."}
	p := NewRFPParser(RFPParserConfig{Attachments: att, Summarizer: sum})
	text, summary, err := p.ParseRFP(context.Background(), "abc")
	if err != nil {
		t.Fatalf("ParseRFP: %v", err)
	}
	if !strings.Contains(text, "SAP") {
		t.Errorf("text missing source content: %q", text)
	}
	if !strings.Contains(summary, "SAP") {
		t.Errorf("summary missing summarized content: %q", summary)
	}
}

func TestRFPParserPDFRoutesToExtractor(t *testing.T) {
	att := &fakeAttachments{
		filename: "rfp.pdf",
		mime:     "application/pdf",
		body:     "<binary pdf bytes>",
	}
	ext := &fakeExtractor{result: "Extracted text from PDF"}
	p := NewRFPParser(RFPParserConfig{Attachments: att, Extractor: ext})
	text, _, err := p.ParseRFP(context.Background(), "abc")
	if err != nil {
		t.Fatalf("ParseRFP: %v", err)
	}
	if !ext.called {
		t.Errorf("expected extractor to be called")
	}
	if !strings.Contains(text, "Extracted") {
		t.Errorf("text wrong: %q", text)
	}
}

func TestRFPParserDOCXRoutesToExtractor(t *testing.T) {
	att := &fakeAttachments{
		filename: "rfp.docx",
		mime:     "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		body:     "<binary docx bytes>",
	}
	ext := &fakeExtractor{result: "Hola from DOCX"}
	p := NewRFPParser(RFPParserConfig{Attachments: att, Extractor: ext})
	text, _, err := p.ParseRFP(context.Background(), "abc")
	if err != nil {
		t.Fatalf("ParseRFP: %v", err)
	}
	if !strings.Contains(text, "DOCX") {
		t.Errorf("text wrong: %q", text)
	}
}

func TestRFPParserUnsupportedMIME(t *testing.T) {
	att := &fakeAttachments{
		filename: "img.png",
		mime:     "image/png",
		body:     "binary",
	}
	p := NewRFPParser(RFPParserConfig{Attachments: att})
	_, _, err := p.ParseRFP(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "unsupported MIME") {
		t.Fatalf("expected unsupported MIME error, got %v", err)
	}
}

func TestRFPParserPDFWithoutExtractor(t *testing.T) {
	att := &fakeAttachments{
		filename: "rfp.pdf",
		mime:     "application/pdf",
		body:     "binary",
	}
	p := NewRFPParser(RFPParserConfig{Attachments: att})
	_, _, err := p.ParseRFP(context.Background(), "abc")
	if err == nil || !strings.Contains(err.Error(), "extractor not wired") {
		t.Fatalf("expected extractor-not-wired error, got %v", err)
	}
}

func TestRFPParserNilAttachments(t *testing.T) {
	p := NewRFPParser(RFPParserConfig{})
	_, _, err := p.ParseRFP(context.Background(), "abc")
	if err == nil {
		t.Fatal("expected error when attachments not wired")
	}
}

func TestRFPParserSummarizerFailureReturnsTextAndError(t *testing.T) {
	att := &fakeAttachments{
		filename: "rfp.txt",
		mime:     "text/plain",
		body:     "Hola",
	}
	sum := &fakeSummarizer{err: errors.New("LLM down")}
	p := NewRFPParser(RFPParserConfig{Attachments: att, Summarizer: sum})
	text, summary, err := p.ParseRFP(context.Background(), "abc")
	if err == nil {
		t.Errorf("expected wrapped summarizer error")
	}
	if !strings.Contains(text, "Hola") {
		t.Errorf("text should still come back: %q", text)
	}
	if summary != "" {
		t.Errorf("summary should be empty when summarizer fails: %q", summary)
	}
}

func TestLibreOfficeExtractorReturnsText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/forms/libreoffice/convert") {
			t.Errorf("path = %s", r.URL.Path)
		}
		// Read multipart body to advance.
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("Texto extraído del PDF"))
	}))
	defer srv.Close()

	ext := NewLibreOfficeExtractor(srv.URL, nil)
	text, err := ext.ExtractText(context.Background(), "rfp.pdf", strings.NewReader("binary"))
	if err != nil {
		t.Fatalf("ExtractText: %v", err)
	}
	if !strings.Contains(text, "Texto extraído") {
		t.Errorf("text = %q", text)
	}
}

func TestLibreOfficeExtractorReturnsPDFFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 fake"))
	}))
	defer srv.Close()

	ext := NewLibreOfficeExtractor(srv.URL, nil)
	_, err := ext.ExtractText(context.Background(), "rfp.docx", strings.NewReader("binary"))
	if err == nil || !strings.Contains(err.Error(), "txt output not supported") {
		t.Fatalf("expected PDF-not-supported error, got %v", err)
	}
}

func TestLibreOfficeExtractorHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ext := NewLibreOfficeExtractor(srv.URL, nil)
	_, err := ext.ExtractText(context.Background(), "rfp.pdf", strings.NewReader("binary"))
	if err == nil {
		t.Fatal("expected http error")
	}
}
