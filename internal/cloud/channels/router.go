package channels

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// RouterConfig wires the router with its known channels.
type RouterConfig struct {
	ClaudeMax Channel // optional; nil disables claude-max-vps
	GemmaLocal Channel // required for confidential traffic and as fallback
}

// defaultRouter is the production routing implementation.
type defaultRouter struct {
	claude Channel
	gemma  Channel
}

// NewRouter returns a Router. At least gemma must be wired or all queries
// against confidential data will fail.
func NewRouter(cfg RouterConfig) Router {
	return &defaultRouter{claude: cfg.ClaudeMax, gemma: cfg.GemmaLocal}
}

// Select implements Router. It chooses the preferred channel for the
// sensitivity tier without considering current availability — that is the
// caller's job (or use Query, which does fallback).
func (r *defaultRouter) Select(sensitivity string) Channel {
	switch normalizeSensitivity(sensitivity) {
	case SensitivityConfidential:
		return r.gemma
	case SensitivityPublic, SensitivityInternal, SensitivityClient:
		if r.claude != nil {
			return r.claude
		}
		return r.gemma
	default:
		// Unknown → treat as confidential (fail closed: only gemma).
		return r.gemma
	}
}

// Query routes the request, retrying on the gemma fallback if claude-max is
// rate-limited or transiently unavailable. For confidential traffic, NO
// fallback to claude is ever attempted regardless of channel state.
func (r *defaultRouter) Query(ctx context.Context, req Request) (*Response, error) {
	sens := normalizeSensitivity(req.Sensitivity)

	if sens == SensitivityConfidential {
		if r.gemma == nil {
			return nil, fmt.Errorf("%w: confidential sensitivity requires gemma-local", ErrNoChannelAvailable)
		}
		return r.gemma.Query(ctx, req)
	}

	// Public/internal/client → prefer claude-max-vps with gemma fallback.
	primary := r.claude
	if primary == nil {
		primary = r.gemma
	}
	if primary == nil {
		return nil, ErrNoChannelAvailable
	}

	resp, err := primary.Query(ctx, req)
	if err == nil {
		return resp, nil
	}

	// Only fall back to gemma when primary was claude-max and the failure was
	// rate-limit or transient unavailability. If primary already was gemma, no
	// further fallback exists.
	if primary == r.claude && r.gemma != nil && (errors.Is(err, ErrChannelRateLimited) || errors.Is(err, ErrChannelUnavailable)) {
		fallback, fbErr := r.gemma.Query(ctx, req)
		if fbErr == nil {
			fallback.Error = fmt.Sprintf("primary %s failed: %v; fallback %s used", primary.Name(), err, r.gemma.Name())
			return fallback, nil
		}
		return nil, fmt.Errorf("primary %s: %w; fallback %s: %w", primary.Name(), err, r.gemma.Name(), fbErr)
	}

	return nil, err
}

func normalizeSensitivity(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case SensitivityPublic, SensitivityInternal, SensitivityClient, SensitivityConfidential:
		return v
	default:
		return SensitivityConfidential
	}
}
