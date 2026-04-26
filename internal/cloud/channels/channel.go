// Package channels implements the LLM channel routing system used by the
// quote-chat workflow. A "channel" is one concrete LLM endpoint (Claude Code
// CLI on the VPS, local Ollama, etc) — the Router selects the right channel
// for a given Request based on data sensitivity and channel availability.
//
// Wave 6 wiring: claude-max-vps (subprocess) + gemma-local (HTTP) + router.
// Confidential data NEVER touches claude-max-vps; client/internal/public can
// route to claude-max-vps with gemma-local fallback on rate limit / failure.
package channels

import (
	"context"
	"errors"
	"time"
)

// Sensitivity tier copied from redactor to avoid an import cycle.
const (
	SensitivityPublic       = "public"
	SensitivityInternal     = "internal"
	SensitivityClient       = "client"
	SensitivityConfidential = "confidential"
)

// ChannelClaudeMax is the canonical name used for the Claude Code subprocess.
const ChannelClaudeMax = "claude-max-vps"

// ChannelGemmaLocal is the canonical name used for the local Ollama endpoint.
const ChannelGemmaLocal = "gemma-local"

// ErrNoChannelAvailable is returned by the router when all candidate channels
// are exhausted (e.g. claude rate-limited and gemma unreachable).
var ErrNoChannelAvailable = errors.New("channels: no channel available for sensitivity")

// ErrChannelUnavailable is returned by Channel.Query when the channel cannot
// service the request right now (rate-limit, transient HTTP 5xx, missing
// binary). Routers consult IsRateLimit/IsTransient via errors.Is.
var ErrChannelUnavailable = errors.New("channels: channel unavailable")

// ErrChannelRateLimited signals that the upstream returned an explicit rate
// limit error. Router uses this to escalate to fallback gemma-local.
var ErrChannelRateLimited = errors.New("channels: rate limited")

// Message represents one turn in the chat history sent to the LLM.
type Message struct {
	Role    string // "user" | "assistant" | "system"
	Content string
}

// Request is the input shape consumed by Channel.Query.
type Request struct {
	SystemPrompt string
	Messages     []Message
	Sensitivity  string // public|internal|client|confidential
	TimeoutSec   int    // default 60 if 0
	Model        string // optional override (sonnet|opus|gemma4:26b ...)
}

// Response is the output from a Channel.Query call.
type Response struct {
	Content    string
	Model      string
	Channel    string
	DurationMs int
	TokensIn   int    // best-effort estimate
	TokensOut  int    // best-effort estimate
	Error      string // populated on routed-to-fallback / soft errors
}

// Channel is the contract one LLM endpoint must implement.
type Channel interface {
	// Name returns the canonical channel identifier (e.g. "claude-max-vps").
	Name() string
	// Query runs one prompt + history through the upstream and returns the
	// rendered response. timeout is applied via context.WithTimeout if the
	// caller did not already set a deadline.
	Query(ctx context.Context, req Request) (*Response, error)
}

// Router selects + executes a channel for the request.
type Router interface {
	// Select returns the preferred channel for the given sensitivity tier.
	// Returns nil if no channel is allowed at this tier.
	Select(sensitivity string) Channel
	// Query routes the request, handling fallback (claude-max → gemma) on
	// rate limits or transient channel failures. The returned Response has
	// Channel set to the actual channel that produced the content.
	Query(ctx context.Context, req Request) (*Response, error)
}

// defaultTimeout is applied when Request.TimeoutSec is zero.
const defaultTimeout = 60 * time.Second

// applyTimeout returns a context+cancel honouring req.TimeoutSec or the default.
func applyTimeout(parent context.Context, req Request) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		return parent, func() {}
	}
	dur := time.Duration(req.TimeoutSec) * time.Second
	if dur <= 0 {
		dur = defaultTimeout
	}
	return context.WithTimeout(parent, dur)
}

// estimateTokens returns a rough token count using the heuristic 1 token ≈ 4
// characters. Good enough for telemetry — real tokenization happens upstream.
func estimateTokens(text string) int {
	if text == "" {
		return 0
	}
	t := len(text) / 4
	if t == 0 {
		return 1
	}
	return t
}
