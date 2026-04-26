// Package email provides a Microsoft Graph (Microsoft 365) email client and
// templated notifications for ARIA Core.
//
// The package uses OAuth2 client credentials against Entra ID and the
// /v1.0/users/{from}/sendMail endpoint to dispatch transactional emails.
// Token caching with auto-refresh, exponential retry, and HTML/text body
// rendering are included. No heavyweight dependencies — only net/http and
// encoding/json.
package email

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"text/template"
	"time"
)

// Config carries the credentials and defaults required to talk to Microsoft Graph.
type Config struct {
	TenantID     string
	ClientID     string
	ClientSecret string
	FromAddress  string
	// HTTPClient is optional — if nil, a default 30s-timeout client is used.
	HTTPClient *http.Client
	// PublicURL is the base URL used to render absolute links in templates
	// (e.g. magic-link invite URLs). Optional — empty falls back to localhost.
	PublicURL string
}

// Client is a Microsoft Graph mail client with cached app-only tokens.
type Client struct {
	cfg        Config
	httpClient *http.Client

	mu          sync.Mutex
	cachedToken string
	tokenExpiry time.Time

	// renderer provides parsed template lookups (lazy-loaded).
	rendererOnce sync.Once
	renderer     *templateRenderer
	rendererErr  error
}

// ErrNotConfigured is returned by SendMail when essential config is missing.
// Callers can use errors.Is to silently skip sending instead of failing.
var ErrNotConfigured = errors.New("email: M365 client is not configured")

const (
	tokenURLFormat = "https://login.microsoftonline.com/%s/oauth2/v2.0/token"
	graphScope     = "https://graph.microsoft.com/.default"
	sendMailURLFmt = "https://graph.microsoft.com/v1.0/users/%s/sendMail"

	// refreshLeeway forces a token refresh when its remaining lifetime is below
	// this threshold (5 minutes per spec).
	refreshLeeway = 5 * time.Minute

	defaultHTTPTimeout = 30 * time.Second
	maxRetries         = 3
)

// NewClient returns a new email client. If TenantID/ClientID/ClientSecret/FromAddress
// are all empty, the client is returned in a "disabled" state — every SendMail
// call will return ErrNotConfigured so callers can no-op silently.
func NewClient(cfg Config) (*Client, error) {
	cfg.TenantID = strings.TrimSpace(cfg.TenantID)
	cfg.ClientID = strings.TrimSpace(cfg.ClientID)
	cfg.ClientSecret = strings.TrimSpace(cfg.ClientSecret)
	cfg.FromAddress = strings.TrimSpace(cfg.FromAddress)
	cfg.PublicURL = strings.TrimSpace(cfg.PublicURL)

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	c := &Client{cfg: cfg, httpClient: httpClient}
	return c, nil
}

// IsConfigured reports whether enough config is present to actually send mail.
func (c *Client) IsConfigured() bool {
	if c == nil {
		return false
	}
	return c.cfg.TenantID != "" && c.cfg.ClientID != "" && c.cfg.ClientSecret != "" && c.cfg.FromAddress != ""
}

// FromAddress exposes the configured sender (used by services to log/build hints).
func (c *Client) FromAddress() string {
	if c == nil {
		return ""
	}
	return c.cfg.FromAddress
}

// PublicURL exposes the configured public URL used for magic links.
func (c *Client) PublicURL() string {
	if c == nil {
		return ""
	}
	return c.cfg.PublicURL
}

// SendMail dispatches an email via Microsoft Graph /sendMail.
// recipient is the primary "to" address. textBody is optional (HTML alone is
// acceptable; the html body is the source of truth for Graph).
func (c *Client) SendMail(ctx context.Context, to, subject, htmlBody, textBody string) error {
	return c.SendMailBCC(ctx, to, nil, subject, htmlBody, textBody)
}

// SendMailBCC is the same as SendMail but with optional BCC recipients.
func (c *Client) SendMailBCC(ctx context.Context, to string, bcc []string, subject, htmlBody, textBody string) error {
	if c == nil || !c.IsConfigured() {
		return ErrNotConfigured
	}
	to = strings.TrimSpace(to)
	if to == "" {
		return fmt.Errorf("email: recipient is required")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return fmt.Errorf("email: subject is required")
	}
	body := strings.TrimSpace(htmlBody)
	contentType := "HTML"
	if body == "" {
		body = strings.TrimSpace(textBody)
		contentType = "Text"
	}
	if body == "" {
		return fmt.Errorf("email: body is required")
	}

	msg := graphMessage{
		Subject: subject,
		Body: graphBody{
			ContentType: contentType,
			Content:     body,
		},
		ToRecipients: []graphRecipient{
			{EmailAddress: graphAddress{Address: to}},
		},
	}
	for _, addr := range bcc {
		addr = strings.TrimSpace(addr)
		if addr == "" || strings.EqualFold(addr, to) {
			continue
		}
		msg.BCCRecipients = append(msg.BCCRecipients, graphRecipient{EmailAddress: graphAddress{Address: addr}})
	}

	payload := graphSendMailRequest{
		SaveToSentItems: false,
		Message:         msg,
	}

	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("email: encode sendMail payload: %w", err)
	}

	endpoint := fmt.Sprintf(sendMailURLFmt, url.PathEscape(c.cfg.FromAddress))
	return c.sendWithRetry(ctx, endpoint, encoded)
}

// sendWithRetry posts the encoded sendMail body, refreshing tokens on 401 and
// applying exponential backoff for transient errors.
func (c *Client) sendWithRetry(ctx context.Context, endpoint string, body []byte) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		token, err := c.getToken(ctx, attempt > 0 /* forceRefresh */)
		if err != nil {
			return fmt.Errorf("email: acquire token: %w", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("email: build sendMail request: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("email: send attempt %d: %w", attempt+1, err)
			if backoffSleep(ctx, attempt) {
				continue
			}
			return lastErr
		}
		// Drain & close body even on success.
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusAccepted, resp.StatusCode == http.StatusOK, resp.StatusCode == http.StatusNoContent:
			return nil
		case resp.StatusCode == http.StatusUnauthorized:
			// Force token refresh on next attempt.
			c.invalidateToken()
			lastErr = fmt.Errorf("email: graph 401: %s", trimResponseBody(respBody))
		case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
			lastErr = fmt.Errorf("email: graph %d: %s", resp.StatusCode, trimResponseBody(respBody))
		default:
			// Non-retryable client error.
			return fmt.Errorf("email: graph %d: %s", resp.StatusCode, trimResponseBody(respBody))
		}
		if !backoffSleep(ctx, attempt) {
			return lastErr
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("email: send failed after %d attempts", maxRetries)
	}
	return lastErr
}

// getToken returns a cached access token or fetches a new one when expired.
func (c *Client) getToken(ctx context.Context, forceRefresh bool) (string, error) {
	c.mu.Lock()
	if !forceRefresh && c.cachedToken != "" && time.Until(c.tokenExpiry) > refreshLeeway {
		t := c.cachedToken
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	token, expiresIn, err := c.fetchToken(ctx)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cachedToken = token
	c.tokenExpiry = time.Now().Add(time.Duration(expiresIn) * time.Second)
	c.mu.Unlock()
	return token, nil
}

func (c *Client) invalidateToken() {
	c.mu.Lock()
	c.cachedToken = ""
	c.tokenExpiry = time.Time{}
	c.mu.Unlock()
}

func (c *Client) fetchToken(ctx context.Context) (string, int, error) {
	endpoint := fmt.Sprintf(tokenURLFormat, url.PathEscape(c.cfg.TenantID))
	form := url.Values{}
	form.Set("client_id", c.cfg.ClientID)
	form.Set("client_secret", c.cfg.ClientSecret)
	form.Set("scope", graphScope)
	form.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("token http: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("token endpoint %d: %s", resp.StatusCode, trimResponseBody(body))
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", 0, fmt.Errorf("decode token response: %w", err)
	}
	if strings.TrimSpace(tr.AccessToken) == "" {
		return "", 0, fmt.Errorf("token response missing access_token")
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 3600
	}
	return tr.AccessToken, tr.ExpiresIn, nil
}

// backoffSleep waits 200ms*2^attempt with context-respecting cancellation.
// Returns false if the context expires.
func backoffSleep(ctx context.Context, attempt int) bool {
	delay := time.Duration(200*(1<<attempt)) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func trimResponseBody(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 512 {
		s = s[:512] + "...(truncated)"
	}
	return s
}

// ─── Microsoft Graph types ─────────────────────────────────────────────────

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	TokenType   string `json:"token_type"`
	Scope       string `json:"scope"`
}

type graphSendMailRequest struct {
	Message         graphMessage `json:"message"`
	SaveToSentItems bool         `json:"saveToSentItems"`
}

type graphMessage struct {
	Subject      string           `json:"subject"`
	Body         graphBody        `json:"body"`
	ToRecipients []graphRecipient `json:"toRecipients"`
	CCRecipients []graphRecipient `json:"ccRecipients,omitempty"`
	BCCRecipients []graphRecipient `json:"bccRecipients,omitempty"`
}

type graphBody struct {
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

type graphRecipient struct {
	EmailAddress graphAddress `json:"emailAddress"`
}

type graphAddress struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
}

// ─── Templates (//go:embed) ────────────────────────────────────────────────

//go:embed templates/*.html
var templateFS embed.FS

type templateRenderer struct {
	tmpls map[string]*template.Template
}

func (c *Client) loadRenderer() (*templateRenderer, error) {
	c.rendererOnce.Do(func() {
		entries, err := templateFS.ReadDir("templates")
		if err != nil {
			c.rendererErr = fmt.Errorf("read templates dir: %w", err)
			return
		}
		r := &templateRenderer{tmpls: make(map[string]*template.Template, len(entries))}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
				continue
			}
			data, err := templateFS.ReadFile("templates/" + e.Name())
			if err != nil {
				c.rendererErr = fmt.Errorf("read template %s: %w", e.Name(), err)
				return
			}
			t, err := template.New(e.Name()).Parse(string(data))
			if err != nil {
				c.rendererErr = fmt.Errorf("parse template %s: %w", e.Name(), err)
				return
			}
			key := strings.TrimSuffix(e.Name(), ".html")
			r.tmpls[key] = t
		}
		c.renderer = r
	})
	return c.renderer, c.rendererErr
}

// Render executes a named template (without the .html suffix) with the given data.
func (c *Client) Render(name string, data any) (string, error) {
	r, err := c.loadRenderer()
	if err != nil {
		return "", err
	}
	t, ok := r.tmpls[name]
	if !ok {
		return "", fmt.Errorf("email: template %q not found", name)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("email: render template %q: %w", name, err)
	}
	return buf.String(), nil
}
