// Package pdf provides a thin HTTP client for the gotenberg HTML→PDF service.
//
// The aria-core cloud dashboard renders propuestas as full HTML pages and
// delegates PDF rasterization to a colocated gotenberg container reachable
// at http://127.0.0.1:3001 by default. The client only knows how to send a
// single self-contained HTML document (with all CSS/fonts inlined) and read
// the resulting PDF bytes back.
package pdf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// Client is a small wrapper around gotenberg's chromium HTML→PDF endpoint.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient builds a Client targeting the given gotenberg base URL (e.g.
// http://127.0.0.1:3001). The default timeout is 30s as per spec.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// BaseURL returns the configured gotenberg base URL.
func (c *Client) BaseURL() string {
	if c == nil {
		return ""
	}
	return c.baseURL
}

// ConvertOptions controls page sizing/margins for the generated PDF.
// All values are expressed in inches; zero values mean "use gotenberg defaults".
type ConvertOptions struct {
	PaperWidth   float64 // inches; default 8.27 (A4)
	PaperHeight  float64 // inches; default 11.7 (A4)
	MarginTop    float64 // inches; default 0.5
	MarginBottom float64 // inches; default 0.5
	MarginLeft   float64 // inches; default 0.5
	MarginRight  float64 // inches; default 0.5
	// PreferCSSPageSize tells chromium to honour @page CSS rules over the
	// paperWidth/paperHeight form fields when both are present.
	PreferCSSPageSize bool
	// PrintBackground keeps CSS backgrounds (colors/images) in the rendered PDF.
	PrintBackground bool
}

// DefaultOptions returns A4 page size with 0.5 inch margins and printBackground=true.
func DefaultOptions() ConvertOptions {
	return ConvertOptions{
		PaperWidth:      8.27,
		PaperHeight:     11.7,
		MarginTop:       0.5,
		MarginBottom:    0.5,
		MarginLeft:      0.5,
		MarginRight:     0.5,
		PrintBackground: true,
	}
}

// ConvertHTML POSTs the given HTML bytes to gotenberg's
// /forms/chromium/convert/html endpoint and returns the resulting PDF bytes.
//
// gotenberg requires the HTML form file to be named exactly "index.html".
func (c *Client) ConvertHTML(ctx context.Context, htmlBytes []byte, opts ConvertOptions) ([]byte, error) {
	if c == nil {
		return nil, errors.New("pdf: client is nil")
	}
	if c.baseURL == "" {
		return nil, errors.New("pdf: base URL not configured")
	}
	if len(htmlBytes) == 0 {
		return nil, errors.New("pdf: empty html input")
	}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// gotenberg expects the main HTML file under "files" with filename index.html.
	htmlHeader := make(textproto.MIMEHeader)
	htmlHeader.Set("Content-Disposition", `form-data; name="files"; filename="index.html"`)
	htmlHeader.Set("Content-Type", "text/html; charset=utf-8")
	htmlPart, err := writer.CreatePart(htmlHeader)
	if err != nil {
		return nil, fmt.Errorf("pdf: create multipart part: %w", err)
	}
	if _, err := htmlPart.Write(htmlBytes); err != nil {
		return nil, fmt.Errorf("pdf: write html part: %w", err)
	}

	// Page sizing fields — only emit non-zero values to let gotenberg defaults apply.
	if opts.PaperWidth > 0 {
		_ = writer.WriteField("paperWidth", strconv.FormatFloat(opts.PaperWidth, 'f', -1, 64))
	}
	if opts.PaperHeight > 0 {
		_ = writer.WriteField("paperHeight", strconv.FormatFloat(opts.PaperHeight, 'f', -1, 64))
	}
	if opts.MarginTop > 0 {
		_ = writer.WriteField("marginTop", strconv.FormatFloat(opts.MarginTop, 'f', -1, 64))
	}
	if opts.MarginBottom > 0 {
		_ = writer.WriteField("marginBottom", strconv.FormatFloat(opts.MarginBottom, 'f', -1, 64))
	}
	if opts.MarginLeft > 0 {
		_ = writer.WriteField("marginLeft", strconv.FormatFloat(opts.MarginLeft, 'f', -1, 64))
	}
	if opts.MarginRight > 0 {
		_ = writer.WriteField("marginRight", strconv.FormatFloat(opts.MarginRight, 'f', -1, 64))
	}
	if opts.PreferCSSPageSize {
		_ = writer.WriteField("preferCssPageSize", "true")
	}
	if opts.PrintBackground {
		_ = writer.WriteField("printBackground", "true")
	}

	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("pdf: close multipart writer: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/forms/chromium/convert/html", body)
	if err != nil {
		return nil, fmt.Errorf("pdf: build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Accept", "application/pdf")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("pdf: gotenberg request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Read up to 4 KiB of error body for diagnostics.
		errBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("pdf: gotenberg returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(errBody)))
	}

	pdfBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("pdf: read response body: %w", err)
	}
	if len(pdfBytes) == 0 {
		return nil, errors.New("pdf: gotenberg returned empty body")
	}
	return pdfBytes, nil
}
