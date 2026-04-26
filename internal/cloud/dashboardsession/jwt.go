// Package dashboardsession emite y verifica JWT HS256 para sesiones del dashboard.
package dashboardsession

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	algHS256 = "HS256"
	typJWT   = "JWT"
)

// Claims representa los claims de la sesión del dashboard.
type Claims struct {
	UID   string `json:"sub"`
	Email string `json:"email,omitempty"`
	Role  string `json:"role,omitempty"`
	IAT   int64  `json:"iat"`
	EXP   int64  `json:"exp"`
}

type Codec struct {
	secret []byte
	ttl    time.Duration
}

func NewCodec(secret string, ttl time.Duration) (*Codec, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil, errors.New("dashboardsession: secret is required")
	}
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &Codec{secret: []byte(secret), ttl: ttl}, nil
}

// Mint emite un JWT HS256 con los claims dados (uid, email, role).
func (c *Codec) Mint(uid, email, role string) (string, error) {
	uid = strings.TrimSpace(uid)
	if uid == "" {
		return "", errors.New("dashboardsession: uid is required")
	}
	now := time.Now().UTC()
	claims := Claims{
		UID:   uid,
		Email: strings.TrimSpace(email),
		Role:  strings.TrimSpace(role),
		IAT:   now.Unix(),
		EXP:   now.Add(c.ttl).Unix(),
	}
	hdr := map[string]string{"alg": algHS256, "typ": typJWT}
	hb, err := json.Marshal(hdr)
	if err != nil {
		return "", fmt.Errorf("marshal header: %w", err)
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal claims: %w", err)
	}
	hSeg := base64URLEncode(hb)
	cSeg := base64URLEncode(cb)
	signing := hSeg + "." + cSeg
	sig := signHMAC(c.secret, signing)
	return signing + "." + sig, nil
}

// Parse verifica firma + exp y retorna los claims.
func (c *Codec) Parse(token string) (*Claims, error) {
	token = strings.TrimSpace(token)
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("dashboardsession: malformed token")
	}
	signing := parts[0] + "." + parts[1]
	expected := signHMAC(c.secret, signing)
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return nil, errors.New("dashboardsession: invalid signature")
	}
	hb, err := base64URLDecode(parts[0])
	if err != nil {
		return nil, fmt.Errorf("decode header: %w", err)
	}
	var hdr map[string]string
	if err := json.Unmarshal(hb, &hdr); err != nil {
		return nil, fmt.Errorf("parse header: %w", err)
	}
	if hdr["alg"] != algHS256 {
		return nil, fmt.Errorf("dashboardsession: unsupported alg %q", hdr["alg"])
	}
	cb, err := base64URLDecode(parts[1])
	if err != nil {
		return nil, fmt.Errorf("decode claims: %w", err)
	}
	var claims Claims
	if err := json.Unmarshal(cb, &claims); err != nil {
		return nil, fmt.Errorf("parse claims: %w", err)
	}
	if claims.EXP > 0 && time.Now().UTC().Unix() > claims.EXP {
		return nil, errors.New("dashboardsession: token expired")
	}
	if strings.TrimSpace(claims.UID) == "" {
		return nil, errors.New("dashboardsession: uid claim missing")
	}
	return &claims, nil
}

func base64URLEncode(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func base64URLDecode(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(s)
}

func signHMAC(secret []byte, msg string) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(msg))
	return base64URLEncode(mac.Sum(nil))
}
