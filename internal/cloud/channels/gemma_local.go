package channels

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GemmaLocalConfig configures the Ollama HTTP channel.
type GemmaLocalConfig struct {
	// BaseURL is the Ollama endpoint root. Defaults to "http://127.0.0.1:11434".
	BaseURL string
	// DefaultModel is the model name passed to Ollama when Request.Model is empty.
	// Defaults to "gemma4:latest".
	DefaultModel string
	// HTTPClient is the http.Client used. nil → default with 90s timeout.
	HTTPClient *http.Client
}

// GemmaLocalChannel POSTs /api/chat against Ollama and reads the response.
type GemmaLocalChannel struct {
	cfg GemmaLocalConfig
}

// NewGemmaLocalChannel builds a channel wired to the local Ollama daemon.
func NewGemmaLocalChannel(cfg GemmaLocalConfig) *GemmaLocalChannel {
	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = "http://127.0.0.1:11434"
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		cfg.DefaultModel = "gemma4:latest"
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 90 * time.Second}
	}
	return &GemmaLocalChannel{cfg: cfg}
}

// Name returns "gemma-local".
func (c *GemmaLocalChannel) Name() string { return ChannelGemmaLocal }

// ollamaMessage is the wire shape Ollama's /api/chat expects.
type ollamaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ollamaChatRequest struct {
	Model    string          `json:"model"`
	Messages []ollamaMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

// ollamaChatResponse is the non-streaming response shape.
type ollamaChatResponse struct {
	Model     string `json:"model"`
	CreatedAt string `json:"created_at"`
	Message   struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"message"`
	Done            bool `json:"done"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
}

// Query sends the request to Ollama and returns the response.
func (c *GemmaLocalChannel) Query(ctx context.Context, req Request) (*Response, error) {
	if c == nil {
		return nil, ErrChannelUnavailable
	}
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.cfg.DefaultModel
	}

	msgs := make([]ollamaMessage, 0, len(req.Messages)+1)
	if sys := strings.TrimSpace(req.SystemPrompt); sys != "" {
		msgs = append(msgs, ollamaMessage{Role: "system", Content: sys})
	}
	for _, m := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		content := strings.TrimSpace(m.Content)
		if content == "" {
			continue
		}
		switch role {
		case "user", "assistant", "system":
			msgs = append(msgs, ollamaMessage{Role: role, Content: content})
		}
	}
	if len(msgs) == 0 {
		return nil, errors.New("gemma_local: empty messages")
	}

	payload := ollamaChatRequest{
		Model:    model,
		Messages: msgs,
		Stream:   false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("gemma_local: marshal: %w", err)
	}

	subCtx, cancel := applyTimeout(ctx, req)
	defer cancel()

	endpoint := strings.TrimRight(c.cfg.BaseURL, "/") + "/api/chat"
	httpReq, err := http.NewRequestWithContext(subCtx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("gemma_local: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.cfg.HTTPClient.Do(httpReq)
	elapsed := time.Since(start)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("%w: gemma timeout: %w", ErrChannelUnavailable, err)
		}
		return nil, fmt.Errorf("%w: gemma transport: %w", ErrChannelUnavailable, err)
	}
	defer resp.Body.Close()

	rawBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests {
		return nil, fmt.Errorf("%w: gemma 429", ErrChannelRateLimited)
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%w: gemma http %d: %s", ErrChannelUnavailable, resp.StatusCode, firstLine(string(rawBody)))
	}

	var parsed ollamaChatResponse
	if err := json.Unmarshal(rawBody, &parsed); err != nil {
		return nil, fmt.Errorf("gemma_local: decode response: %w", err)
	}
	out := strings.TrimSpace(parsed.Message.Content)
	tokensIn := parsed.PromptEvalCount
	if tokensIn == 0 {
		tokensIn = estimateTokens(joinMessages(msgs))
	}
	tokensOut := parsed.EvalCount
	if tokensOut == 0 {
		tokensOut = estimateTokens(out)
	}

	return &Response{
		Content:    out,
		Model:      parsed.Model,
		Channel:    ChannelGemmaLocal,
		DurationMs: int(elapsed / time.Millisecond),
		TokensIn:   tokensIn,
		TokensOut:  tokensOut,
	}, nil
}

func joinMessages(ms []ollamaMessage) string {
	var b strings.Builder
	for _, m := range ms {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}
