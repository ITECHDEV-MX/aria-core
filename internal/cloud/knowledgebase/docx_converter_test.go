package knowledgebase

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakePandoc struct {
	available  error
	calls      []fakePandocCall
	returnData []byte
	failOn     string // failOn=="docx" → fail when toFormat=="docx"
}

type fakePandocCall struct {
	From, To, Ref string
	Input         []byte
}

func (f *fakePandoc) Available(ctx context.Context) error { return f.available }
func (f *fakePandoc) Run(ctx context.Context, from, to string, input []byte, ref string) ([]byte, error) {
	f.calls = append(f.calls, fakePandocCall{From: from, To: to, Ref: ref, Input: append([]byte(nil), input...)})
	if f.failOn != "" && to == f.failOn {
		return nil, errors.New("pandoc failed (test)")
	}
	if f.returnData != nil {
		return f.returnData, nil
	}
	return []byte("FAKE-DOCX-BYTES"), nil
}

func TestDOCXConverterUsesReferenceDoc(t *testing.T) {
	tmp := t.TempDir()
	refPath := filepath.Join(tmp, "ref.docx")
	if err := os.WriteFile(refPath, []byte("PK..."), 0o644); err != nil {
		t.Fatal(err)
	}
	c := NewDOCXConverter(refPath, "")
	fp := &fakePandoc{}
	c.withRunner(fp)

	out, err := c.ConvertMarkdown(context.Background(), []byte("# hola"))
	if err != nil {
		t.Fatalf("ConvertMarkdown: %v", err)
	}
	if string(out) != "FAKE-DOCX-BYTES" {
		t.Fatalf("unexpected output: %q", string(out))
	}
	if len(fp.calls) != 1 {
		t.Fatalf("expected 1 pandoc call, got %d", len(fp.calls))
	}
	got := fp.calls[0]
	if got.From != "markdown" || got.To != "docx" {
		t.Fatalf("from/to = %q/%q", got.From, got.To)
	}
	if got.Ref != refPath {
		t.Fatalf("expected reference doc %q, got %q", refPath, got.Ref)
	}
}

func TestDOCXConverterAutoBootstrapsReference(t *testing.T) {
	tmp := t.TempDir()
	bootstrap := filepath.Join(tmp, "auto", "ref.docx")
	c := NewDOCXConverter("", bootstrap)
	fp := &fakePandoc{returnData: []byte("REFDOCX")}
	c.withRunner(fp)

	if _, err := os.Stat(bootstrap); err == nil {
		t.Fatal("bootstrap should not exist yet")
	}
	out, err := c.ConvertMarkdown(context.Background(), []byte("# hola"))
	if err != nil {
		t.Fatalf("ConvertMarkdown: %v", err)
	}
	if _, err := os.Stat(bootstrap); err != nil {
		t.Fatalf("bootstrap reference not created: %v", err)
	}
	// Two pandoc calls: one to bootstrap, one to convert.
	if len(fp.calls) != 2 {
		t.Fatalf("expected 2 pandoc calls (bootstrap + convert), got %d", len(fp.calls))
	}
	if fp.calls[0].Ref != "" {
		t.Errorf("bootstrap call should have empty Ref, got %q", fp.calls[0].Ref)
	}
	if fp.calls[1].Ref != bootstrap {
		t.Errorf("convert call should use bootstrapped ref %q, got %q", bootstrap, fp.calls[1].Ref)
	}
	if string(out) != "REFDOCX" {
		t.Errorf("unexpected output: %q", string(out))
	}
}

func TestDOCXConverterFailsWhenPandocUnavailable(t *testing.T) {
	c := NewDOCXConverter("", "")
	fp := &fakePandoc{available: ErrPandocNotAvailable}
	c.withRunner(fp)
	if _, err := c.ConvertMarkdown(context.Background(), []byte("x")); err == nil {
		t.Fatal("expected unavailable error")
	}
}

func TestDOCXConverterConvertHTML(t *testing.T) {
	c := NewDOCXConverter("", "")
	fp := &fakePandoc{}
	c.withRunner(fp)
	if _, err := c.ConvertHTML(context.Background(), []byte("<p>hola</p>")); err != nil {
		t.Fatalf("ConvertHTML: %v", err)
	}
	if len(fp.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fp.calls))
	}
	if fp.calls[0].From != "html" || fp.calls[0].To != "docx" {
		t.Fatalf("html→docx expected, got %s→%s", fp.calls[0].From, fp.calls[0].To)
	}
	if !strings.Contains(string(fp.calls[0].Input), "<p>hola</p>") {
		t.Fatalf("missing input passthrough")
	}
}

func TestVerifyAvailableNilSafe(t *testing.T) {
	var c *DOCXConverter
	if err := c.VerifyAvailable(context.Background()); err == nil {
		t.Fatal("expected error on nil converter")
	}
}
