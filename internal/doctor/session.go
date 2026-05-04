package doctor

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

// readSessionExpiry reads session.json and returns the JWT exp claim
// as a time.Time. Without dragging in jwt-go, we just split on dots
// and base64-decode the payload.
func readSessionExpiry(path string) (time.Time, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return time.Time{}, fmt.Errorf("read session file: %w", err)
	}

	var sess struct {
		Token string `json:"token"`
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(data, &sess); err != nil {
		return time.Time{}, fmt.Errorf("parse session json: %w", err)
	}

	// Prefer ExpiresAt field if present
	if sess.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, sess.ExpiresAt)
		if err == nil {
			return t.UTC(), nil
		}
	}

	// Fallback: decode JWT exp
	if sess.Token == "" {
		return time.Time{}, fmt.Errorf("no token in session.json")
	}
	parts := strings.Split(sess.Token, ".")
	if len(parts) < 2 {
		return time.Time{}, fmt.Errorf("malformed JWT (only %d parts)", len(parts))
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		// Try standard encoding too
		payload, err = base64.StdEncoding.DecodeString(parts[1])
		if err != nil {
			return time.Time{}, fmt.Errorf("decode JWT payload: %w", err)
		}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return time.Time{}, fmt.Errorf("parse JWT claims: %w", err)
	}
	if claims.Exp == 0 {
		return time.Time{}, fmt.Errorf("no exp claim in JWT")
	}
	return time.Unix(claims.Exp, 0).UTC(), nil
}
