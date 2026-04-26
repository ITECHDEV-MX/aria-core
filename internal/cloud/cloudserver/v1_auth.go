package cloudserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/dashboardsession"
)

// ctxKeyClaims se usa para inyectar los claims JWT en el request context.
type ctxKeyClaims struct{}

func claimsFromContext(ctx context.Context) (*dashboardsession.Claims, bool) {
	c, ok := ctx.Value(ctxKeyClaims{}).(*dashboardsession.Claims)
	return c, ok
}

// withJWTAuth extrae bearer token del header Authorization y verifica JWT.
// Inyecta los claims en el context si OK, o responde 401.
func (s *CloudServer) withJWTAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.sessionCodec == nil {
			http.Error(w, "session codec not configured", http.StatusServiceUnavailable)
			return
		}
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, `{"error":"missing bearer token"}`, http.StatusUnauthorized)
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		claims, err := s.sessionCodec.Parse(token)
		if err != nil {
			http.Error(w, `{"error":"invalid or expired token"}`, http.StatusUnauthorized)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyClaims{}, claims)
		next(w, r.WithContext(ctx))
	}
}

// withJWTRole envuelve withJWTAuth y agrega chequeo de role.
func (s *CloudServer) withJWTRole(allowed []string, next http.HandlerFunc) http.HandlerFunc {
	return s.withJWTAuth(func(w http.ResponseWriter, r *http.Request) {
		claims, _ := claimsFromContext(r.Context())
		if claims == nil || !claims.HasAnyRole(allowed...) {
			http.Error(w, `{"error":"forbidden: required role not assigned"}`, http.StatusForbidden)
			return
		}
		next(w, r)
	})
}

// loginRequest es el body de POST /v1/auth/login.
type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token     string   `json:"token"`
	ExpiresIn int      `json:"expires_in_seconds"`
	UID       string   `json:"uid"`
	Email     string   `json:"email"`
	Roles     []string `json:"roles"`
}

func (s *CloudServer) handleV1AuthLogin(w http.ResponseWriter, r *http.Request) {
	if s.userStore == nil || s.sessionCodec == nil {
		http.Error(w, `{"error":"login not configured"}`, http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16*1024)
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid json body"}`, http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Email) == "" || req.Password == "" {
		http.Error(w, `{"error":"email and password are required"}`, http.StatusBadRequest)
		return
	}
	u, err := s.userStore.VerifyPassword(r.Context(), req.Email, req.Password)
	if err != nil {
		http.Error(w, `{"error":"invalid credentials"}`, http.StatusUnauthorized)
		return
	}
	token, err := s.sessionCodec.Mint(u.UID, u.Email, u.Roles)
	if err != nil {
		http.Error(w, `{"error":"mint token failed"}`, http.StatusInternalServerError)
		return
	}
	jsonResponse(w, http.StatusOK, loginResponse{
		Token:     token,
		ExpiresIn: int((8 * time.Hour).Seconds()),
		UID:       u.UID,
		Email:     u.Email,
		Roles:     u.Roles,
	})
}

func (s *CloudServer) handleV1AuthMe(w http.ResponseWriter, r *http.Request) {
	claims, _ := claimsFromContext(r.Context())
	if claims == nil {
		http.Error(w, `{"error":"no claims"}`, http.StatusUnauthorized)
		return
	}
	roles := claims.Roles
	if len(roles) == 0 && claims.Role != "" {
		roles = []string{claims.Role}
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"uid":     claims.UID,
		"email":   claims.Email,
		"roles":   roles,
		"expires": claims.EXP,
	})
}

// Compat shim para evitar import dummy si no se usa errors.
var _ = errors.New
