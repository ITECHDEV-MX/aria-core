package recipes

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// execHTTP runs an http step via the configured HTTPExecutor.
func execHTTP(ctx context.Context, h HTTPExecutor, sr *StepResult, step Step, tCtx TemplateContext) {
	urlStr := RenderString(step.URL, tCtx)
	if strings.TrimSpace(urlStr) == "" {
		sr.Status = StatusFailed
		sr.Stderr = "http step: url is required"
		return
	}
	method := strings.ToUpper(strings.TrimSpace(step.Method))
	if method == "" {
		method = http.MethodGet
	}
	timeout := time.Duration(step.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	bodyBytes := []byte(RenderString(string(step.Body), tCtx))
	status, body, err := h.Do(ctx, method, urlStr, bodyBytes, timeout)
	sr.Stdout = body
	sr.ExitCode = status
	if err != nil {
		sr.Status = StatusFailed
		sr.Stderr = err.Error()
		return
	}
	expected := step.ExpectedStatus
	if expected == 0 {
		// Default success: any 2xx.
		if status >= 200 && status < 300 {
			sr.Status = StatusSuccess
			return
		}
		sr.Status = StatusFailed
		sr.Stderr = fmt.Sprintf("http step: expected 2xx, got %d", status)
		return
	}
	if status != expected {
		sr.Status = StatusFailed
		sr.Stderr = fmt.Sprintf("http step: expected status %d, got %d", expected, status)
		return
	}
	sr.Status = StatusSuccess
}

// defaultHTTPExecutor uses net/http with the supplied timeout.
type defaultHTTPExecutor struct{}

func (defaultHTTPExecutor) Do(ctx context.Context, method, url string, body []byte, timeout time.Duration) (int, string, error) {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cli := &http.Client{Timeout: timeout}

	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return 0, "", err
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := cli.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	return resp.StatusCode, string(respBody), nil
}
