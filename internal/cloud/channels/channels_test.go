package channels

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeRunner is a stub for the claude binary subprocess.
type fakeRunner struct {
	stdout   string
	stderr   string
	exitCode int
	err      error
	gotArgs  []string
	gotStdin string
}

func (f *fakeRunner) run(_ context.Context, args []string, stdin string) (string, string, int, error) {
	f.gotArgs = append([]string{}, args...)
	f.gotStdin = stdin
	return f.stdout, f.stderr, f.exitCode, f.err
}

func TestClaudeMaxChannelHappyPath(t *testing.T) {
	r := &fakeRunner{stdout: "Hola JC, vamos a armar la cotización.\n"}
	ch := NewClaudeMaxChannel(ClaudeMaxConfig{
		BinaryPath:   "/fake/claude",
		DefaultModel: "sonnet",
		Runner:       r.run,
	})

	resp, err := ch.Query(context.Background(), Request{
		SystemPrompt: "Sos un cotizador iTechDev.",
		Messages: []Message{
			{Role: "user", Content: "Necesito propuesta para PARK Salesforce v2"},
		},
		Sensitivity: SensitivityClient,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.Channel != ChannelClaudeMax {
		t.Errorf("channel = %q, want %q", resp.Channel, ChannelClaudeMax)
	}
	if !strings.Contains(resp.Content, "PARK") && !strings.Contains(resp.Content, "Hola JC") {
		t.Errorf("content unexpected: %q", resp.Content)
	}
	if resp.Model != "sonnet" {
		t.Errorf("model = %q, want sonnet", resp.Model)
	}
	if resp.TokensIn == 0 || resp.TokensOut == 0 {
		t.Errorf("expected estimated tokens > 0; got in=%d out=%d", resp.TokensIn, resp.TokensOut)
	}

	// Validate args contained model + system prompt + print/bare flags.
	joined := strings.Join(r.gotArgs, " ")
	if !strings.Contains(joined, "--print") || !strings.Contains(joined, "--bare") {
		t.Errorf("args missing flags: %v", r.gotArgs)
	}
	if !strings.Contains(joined, "--model sonnet") {
		t.Errorf("args missing model: %v", r.gotArgs)
	}
	if !strings.Contains(joined, "--append-system-prompt") {
		t.Errorf("args missing system prompt flag: %v", r.gotArgs)
	}
	if !strings.Contains(r.gotStdin, "PARK") {
		t.Errorf("stdin should contain user message, got %q", r.gotStdin)
	}
}

func TestClaudeMaxRateLimitDetection(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
	}{
		{"explicit", "Error: Rate limit exceeded for user.\n"},
		{"alt", "{\"error\":\"rate_limit\"}"},
		{"429-status", "HTTP 429 too many requests"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &fakeRunner{stderr: c.stderr, exitCode: 1, err: errors.New("subprocess failed")}
			ch := NewClaudeMaxChannel(ClaudeMaxConfig{
				BinaryPath: "/fake/claude",
				Runner:     r.run,
			})
			_, err := ch.Query(context.Background(), Request{
				Messages:    []Message{{Role: "user", Content: "hi"}},
				Sensitivity: SensitivityClient,
			})
			if !errors.Is(err, ErrChannelRateLimited) {
				t.Fatalf("expected ErrChannelRateLimited, got %v", err)
			}
		})
	}
}

func TestClaudeMaxFailureMaps(t *testing.T) {
	r := &fakeRunner{stderr: "boom", exitCode: 7, err: errors.New("non-zero")}
	ch := NewClaudeMaxChannel(ClaudeMaxConfig{
		BinaryPath: "/fake/claude",
		Runner:     r.run,
	})
	_, err := ch.Query(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "x"}},
		Sensitivity: SensitivityInternal,
	})
	if !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("expected ErrChannelUnavailable, got %v", err)
	}
}

func TestGemmaLocalChannelHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var req ollamaChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if req.Stream {
			t.Errorf("stream should be false")
		}
		if req.Model == "" {
			t.Errorf("missing model")
		}
		// Echo last user content as the assistant reply.
		var content string
		for _, m := range req.Messages {
			if m.Role == "user" {
				content = "Eco: " + m.Content
			}
		}
		resp := ollamaChatResponse{
			Model:           req.Model,
			Done:            true,
			PromptEvalCount: 42,
			EvalCount:       17,
		}
		resp.Message.Role = "assistant"
		resp.Message.Content = content
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	ch := NewGemmaLocalChannel(GemmaLocalConfig{
		BaseURL:      srv.URL,
		DefaultModel: "gemma4:latest",
	})

	resp, err := ch.Query(context.Background(), Request{
		SystemPrompt: "Sos cotizador.",
		Messages:     []Message{{Role: "user", Content: "Hola"}},
		Sensitivity:  SensitivityConfidential,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.Channel != ChannelGemmaLocal {
		t.Errorf("channel = %s", resp.Channel)
	}
	if !strings.Contains(resp.Content, "Eco: Hola") {
		t.Errorf("content = %q", resp.Content)
	}
	if resp.TokensIn != 42 || resp.TokensOut != 17 {
		t.Errorf("tokens = %d/%d", resp.TokensIn, resp.TokensOut)
	}
}

func TestGemmaLocalChannelHTTPFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ch := NewGemmaLocalChannel(GemmaLocalConfig{BaseURL: srv.URL})
	_, err := ch.Query(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "x"}},
		Sensitivity: SensitivityConfidential,
	})
	if !errors.Is(err, ErrChannelUnavailable) {
		t.Fatalf("expected ErrChannelUnavailable, got %v", err)
	}
}

// stubChannel returns canned responses or errors for routing tests.
type stubChannel struct {
	name    string
	resp    *Response
	err     error
	calls   int
}

func (s *stubChannel) Name() string { return s.name }
func (s *stubChannel) Query(_ context.Context, _ Request) (*Response, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	clone := *s.resp
	return &clone, nil
}

func TestRouterSelectBySensitivity(t *testing.T) {
	claude := &stubChannel{name: ChannelClaudeMax}
	gemma := &stubChannel{name: ChannelGemmaLocal}
	r := NewRouter(RouterConfig{ClaudeMax: claude, GemmaLocal: gemma})

	if got := r.Select(SensitivityPublic); got != claude {
		t.Errorf("public should pick claude")
	}
	if got := r.Select(SensitivityInternal); got != claude {
		t.Errorf("internal should pick claude")
	}
	if got := r.Select(SensitivityClient); got != claude {
		t.Errorf("client should pick claude")
	}
	if got := r.Select(SensitivityConfidential); got != gemma {
		t.Errorf("confidential should pick gemma")
	}
	if got := r.Select("unknown"); got != gemma {
		t.Errorf("unknown should fail-closed to gemma")
	}
}

func TestRouterFallbackOnRateLimit(t *testing.T) {
	claude := &stubChannel{name: ChannelClaudeMax, err: ErrChannelRateLimited}
	gemma := &stubChannel{
		name: ChannelGemmaLocal,
		resp: &Response{Content: "fallback ok", Channel: ChannelGemmaLocal, Model: "gemma4:latest"},
	}
	r := NewRouter(RouterConfig{ClaudeMax: claude, GemmaLocal: gemma})

	resp, err := r.Query(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "x"}},
		Sensitivity: SensitivityClient,
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.Channel != ChannelGemmaLocal {
		t.Errorf("expected fallback channel, got %q", resp.Channel)
	}
	if resp.Error == "" {
		t.Errorf("expected fallback marker in Error")
	}
}

func TestRouterConfidentialNeverFallsBackToClaude(t *testing.T) {
	claude := &stubChannel{name: ChannelClaudeMax, resp: &Response{Content: "no!", Channel: ChannelClaudeMax}}
	gemma := &stubChannel{name: ChannelGemmaLocal, err: errors.New("gemma down")}
	r := NewRouter(RouterConfig{ClaudeMax: claude, GemmaLocal: gemma})

	_, err := r.Query(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "secret"}},
		Sensitivity: SensitivityConfidential,
	})
	if err == nil {
		t.Fatal("expected error since gemma failed and no fallback to claude is allowed")
	}
	if claude.calls != 0 {
		t.Errorf("claude should NEVER be called for confidential, got calls=%d", claude.calls)
	}
}

func TestRouterNoChannelsAvailable(t *testing.T) {
	r := NewRouter(RouterConfig{})
	_, err := r.Query(context.Background(), Request{
		Messages:    []Message{{Role: "user", Content: "x"}},
		Sensitivity: SensitivityInternal,
	})
	if !errors.Is(err, ErrNoChannelAvailable) {
		t.Fatalf("expected ErrNoChannelAvailable, got %v", err)
	}
}

func TestEstimateCostUSD(t *testing.T) {
	c := EstimateCostUSD(ChannelClaudeMax, 1000, 200)
	if c <= 0 {
		t.Errorf("claude cost should be > 0, got %f", c)
	}
	if EstimateCostUSD(ChannelGemmaLocal, 1000, 1000) != 0 {
		t.Errorf("gemma should be free")
	}
}

func TestEstimateTokens(t *testing.T) {
	if estimateTokens("") != 0 {
		t.Errorf("empty string should give 0 tokens")
	}
	if estimateTokens("hi") < 1 {
		t.Errorf("small string should give >= 1")
	}
}
