package pdf

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestConvertHTMLPostsMultipart(t *testing.T) {
	var receivedCT string
	var receivedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/forms/chromium/convert/html" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Errorf("unexpected method: %s", r.Method)
		}
		receivedCT = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.Header().Set("Content-Type", "application/pdf")
		_, _ = w.Write([]byte("%PDF-1.4 stub"))
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pdfBytes, err := c.ConvertHTML(ctx, []byte("<h1>hi</h1>"), DefaultOptions())
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if !strings.HasPrefix(string(pdfBytes), "%PDF") {
		t.Fatalf("expected PDF bytes, got: %q", string(pdfBytes))
	}
	if !strings.HasPrefix(receivedCT, "multipart/form-data") {
		t.Errorf("expected multipart content-type, got: %s", receivedCT)
	}
	if !strings.Contains(string(receivedBody), `filename="index.html"`) {
		t.Errorf("expected index.html part, body=%s", string(receivedBody))
	}
	if !strings.Contains(string(receivedBody), "paperWidth") {
		t.Errorf("expected paperWidth field in body")
	}
	if !strings.Contains(string(receivedBody), "<h1>hi</h1>") {
		t.Errorf("expected html content in body")
	}
}

func TestConvertHTMLEmptyInput(t *testing.T) {
	c := NewClient("http://example.invalid")
	_, err := c.ConvertHTML(context.Background(), nil, DefaultOptions())
	if err == nil {
		t.Fatalf("expected error for empty input")
	}
}

func TestConvertHTMLPropagatesNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	c := NewClient(srv.URL)
	_, err := c.ConvertHTML(context.Background(), []byte("<h1>x</h1>"), DefaultOptions())
	if err == nil {
		t.Fatal("expected error from gotenberg 5xx")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected status code in err, got: %v", err)
	}
}
