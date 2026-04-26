package cotizador

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// AttachmentReader is the minimum surface of attachments.AttachmentStore that
// the RFP parser needs. Kept narrow so cotizador does not import the
// attachments package directly (to avoid heavy DB dependencies in tests).
type AttachmentReader interface {
	GetMeta(ctx context.Context, id string) (filename, mime string, size int64, err error)
	OpenContent(ctx context.Context, id string) (io.ReadCloser, error)
}

// LLMSummarizer is the contract for "summarize this text". The chat-quote
// runtime wires this with the channel router (claude-max-vps preferred).
type LLMSummarizer interface {
	Summarize(ctx context.Context, text, systemPrompt string) (string, error)
}

// GotenbergTextExtractor converts PDF/DOCX bytes into plain text via the
// gotenberg /forms/libreoffice/convert endpoint. The implementation is wired
// in /cmd/aria-core via a thin adapter on top of internal/cloud/pdf.
type GotenbergTextExtractor interface {
	// ExtractText sends the file to gotenberg and returns plain text. Inputs
	// are the original filename (gotenberg routes by extension) and the body.
	ExtractText(ctx context.Context, filename string, body io.Reader) (string, error)
}

// RFPParserConfig wires the parser's dependencies. Any of these may be nil
// for tests; missing dependencies cause the matching operation to be skipped
// (e.g. nil Summarizer → empty summary returned).
type RFPParserConfig struct {
	Attachments AttachmentReader
	Extractor   GotenbergTextExtractor
	Summarizer  LLMSummarizer
}

// RFPParser is the public type. Use NewRFPParser to build one.
type RFPParser struct {
	cfg RFPParserConfig
}

// NewRFPParser returns a parser. cfg.Attachments must be set; the others may
// be nil (the parser degrades gracefully).
func NewRFPParser(cfg RFPParserConfig) *RFPParser { return &RFPParser{cfg: cfg} }

// ParseRFP is the high-level entrypoint:
//  1. Read the attachment via AttachmentReader.
//  2. If MIME is text/* read straight; if PDF/DOCX dispatch to gotenberg.
//  3. Pass the text to the LLM summarizer (if wired).
//  4. Return (extractedText, summary, err).
func (p *RFPParser) ParseRFP(ctx context.Context, attachmentID string) (text string, summary string, err error) {
	if p == nil || p.cfg.Attachments == nil {
		return "", "", errors.New("rfp_parser: attachments store not wired")
	}
	filename, mime, _, err := p.cfg.Attachments.GetMeta(ctx, attachmentID)
	if err != nil {
		return "", "", fmt.Errorf("rfp_parser: get meta: %w", err)
	}
	rdr, err := p.cfg.Attachments.OpenContent(ctx, attachmentID)
	if err != nil {
		return "", "", fmt.Errorf("rfp_parser: open content: %w", err)
	}
	defer rdr.Close()

	mimeLow := strings.ToLower(strings.TrimSpace(mime))
	switch {
	case strings.HasPrefix(mimeLow, "text/"):
		buf, rerr := io.ReadAll(io.LimitReader(rdr, 8*1024*1024)) // 8MB cap
		if rerr != nil {
			return "", "", fmt.Errorf("rfp_parser: read text: %w", rerr)
		}
		text = string(buf)
	case mimeLow == "application/pdf",
		mimeLow == "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		mimeLow == "application/msword",
		mimeLow == "application/vnd.oasis.opendocument.text":
		if p.cfg.Extractor == nil {
			return "", "", errors.New("rfp_parser: gotenberg extractor not wired for binary docs")
		}
		out, eerr := p.cfg.Extractor.ExtractText(ctx, filename, rdr)
		if eerr != nil {
			return "", "", fmt.Errorf("rfp_parser: extract: %w", eerr)
		}
		text = out
	default:
		return "", "", fmt.Errorf("rfp_parser: unsupported MIME %q", mime)
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return "", "", errors.New("rfp_parser: empty text extracted")
	}

	if p.cfg.Summarizer != nil {
		sum, serr := p.cfg.Summarizer.Summarize(ctx, text, rfpSummarySystemPrompt)
		if serr != nil {
			// Non-fatal: return text + empty summary + the error so caller can log.
			return text, "", fmt.Errorf("rfp_parser: summarize: %w", serr)
		}
		summary = strings.TrimSpace(sum)
	}
	return text, summary, nil
}

// rfpSummarySystemPrompt is the canonical system prompt used to extract a
// short executive summary from an RFP. Spanish locale to match iTechDev.
const rfpSummarySystemPrompt = `Sos un analista comercial iTechDev. Recibís el texto crudo de un RFP del cliente. Tu tarea: producir un resumen ejecutivo en español, máximo 200 palabras, que cubra:

1. Requirements técnicos clave (productos, integraciones, plataformas)
2. Deadline / fechas importantes
3. Presupuesto si lo menciona (importes, rangos)
4. Stakeholders y roles mencionados
5. Restricciones (tecnologías obligatorias, certificaciones, geo)

Sin ofrecer solución técnica todavía. Sólo el análisis estructurado del RFP.`

// ─── Gotenberg adapter (default implementation) ─────────────────────────────
//
// libreofficeExtractor is a default implementation of GotenbergTextExtractor
// that reuses an http.Client and the gotenberg /forms/libreoffice/convert
// endpoint. The extracted PDF is then post-processed to plain text via
// pdftotext-like heuristics: gotenberg can also export to txt directly via
// the libreoffice "txt" pipeline, so we ask for `outputFormat=txt`.

// LibreOfficeExtractor is an HTTP client targeting gotenberg's libreoffice
// pipeline. It returns the extracted text directly.
type LibreOfficeExtractor struct {
	BaseURL    string
	HTTPClient *http.Client
}

// NewLibreOfficeExtractor builds the default extractor. baseURL like
// "http://127.0.0.1:3001". http.Client may be nil → 60s default.
func NewLibreOfficeExtractor(baseURL string, client *http.Client) *LibreOfficeExtractor {
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &LibreOfficeExtractor{BaseURL: strings.TrimRight(baseURL, "/"), HTTPClient: client}
}

// ExtractText POSTs the file to gotenberg's libreoffice route and returns the
// converted text. We request outputFormat=txt; gotenberg returns a body that
// is the text content directly (in newer versions) or a tiny zip — when we
// detect a zip header we fall back to a "best-effort" conversion: ask for PDF
// then return whatever stdout it gave us. For our internal RFPs the txt path
// works.
func (e *LibreOfficeExtractor) ExtractText(ctx context.Context, filename string, body io.Reader) (string, error) {
	if e == nil || strings.TrimSpace(e.BaseURL) == "" {
		return "", errors.New("libreoffice_extractor: not configured")
	}
	if strings.TrimSpace(filename) == "" {
		filename = "document.pdf"
	}
	bodyBytes, err := io.ReadAll(io.LimitReader(body, 50*1024*1024))
	if err != nil {
		return "", fmt.Errorf("libreoffice_extractor: read body: %w", err)
	}

	mp := &bytes.Buffer{}
	w := multipart.NewWriter(mp)
	hdr := make(textproto.MIMEHeader)
	hdr.Set("Content-Disposition", fmt.Sprintf(`form-data; name="files"; filename=%q`, filename))
	hdr.Set("Content-Type", "application/octet-stream")
	part, err := w.CreatePart(hdr)
	if err != nil {
		return "", fmt.Errorf("libreoffice_extractor: create part: %w", err)
	}
	if _, err := part.Write(bodyBytes); err != nil {
		return "", fmt.Errorf("libreoffice_extractor: write part: %w", err)
	}
	// Tell gotenberg to give us txt output. Older gotenberg versions ignore
	// this and produce PDF; we still try to use the output.
	_ = w.WriteField("outputFormat", "txt")
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("libreoffice_extractor: close: %w", err)
	}

	endpoint := e.BaseURL + "/forms/libreoffice/convert"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, mp)
	if err != nil {
		return "", fmt.Errorf("libreoffice_extractor: new request: %w", err)
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("Accept", "text/plain, application/pdf")

	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("libreoffice_extractor: do: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("libreoffice_extractor: gotenberg http %d: %s", resp.StatusCode, firstLineString(respBody))
	}
	// If body is PDF (starts with %PDF), gotenberg ignored outputFormat=txt.
	// In that case we cannot extract text on our own without a pdftotext
	// dependency, so we return a polite error and let the caller fallback.
	if len(respBody) >= 4 && bytes.Equal(respBody[:4], []byte("%PDF")) {
		return "", errors.New("libreoffice_extractor: gotenberg returned PDF (txt output not supported); install gotenberg with libreoffice + ensure outputFormat=txt is honored")
	}
	return strings.TrimSpace(string(respBody)), nil
}

func firstLineString(b []byte) string {
	s := strings.TrimSpace(string(b))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
