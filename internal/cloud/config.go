package cloud

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DSN             string
	JWTSecret       string
	CORSOrigins     []string
	MaxPool         int
	Port            int
	BindHost        string
	AdminToken      string
	AllowedProjects []string

	// Microsoft 365 / Microsoft Graph (email module).
	// Empty values → email module is initialized in degraded mode (logs but
	// does not actually send mail).
	M365TenantID     string
	M365ClientID     string
	M365ClientSecret string
	M365FromEmail    string

	// PublicURL is the base URL used to construct magic-link URLs and other
	// absolute references in transactional emails. Default is set in
	// DefaultConfig.
	PublicURL string
}

const DefaultJWTSecret = "aria-core-dev-jwt-secret-for-local-smoke-1234"

func DefaultConfig() Config {
	return Config{
		DSN:         "postgres://aria-core:aria-core_dev@localhost:5433/aria-core_cloud?sslmode=disable",
		JWTSecret:   DefaultJWTSecret,
		CORSOrigins: []string{"*"},
		MaxPool:     10,
		Port:        8080,
		BindHost:    "127.0.0.1",
		PublicURL:   "https://ariacore.itechdev.com.mx",
	}
}

func IsDefaultJWTSecret(secret string) bool {
	return strings.TrimSpace(secret) == DefaultJWTSecret
}

func ConfigFromEnv() Config {
	cfg := DefaultConfig()
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_DATABASE_URL")); v != "" {
		cfg.DSN = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_JWT_SECRET")); v != "" {
		cfg.JWTSecret = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_CLOUD_ADMIN")); v != "" {
		cfg.AdminToken = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Port = n
		}
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_CLOUD_HOST")); v != "" {
		cfg.BindHost = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_M365_TENANT_ID")); v != "" {
		cfg.M365TenantID = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_M365_CLIENT_ID")); v != "" {
		cfg.M365ClientID = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_M365_CLIENT_SECRET")); v != "" {
		cfg.M365ClientSecret = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_M365_FROM_EMAIL")); v != "" {
		cfg.M365FromEmail = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_PUBLIC_URL")); v != "" {
		cfg.PublicURL = v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_CLOUD_ALLOWED_PROJECTS")); v != "" {
		parts := strings.Split(v, ",")
		projects := make([]string, 0, len(parts))
		seen := make(map[string]struct{})
		for _, part := range parts {
			project := strings.TrimSpace(part)
			if project == "" {
				continue
			}
			if _, ok := seen[project]; ok {
				continue
			}
			seen[project] = struct{}{}
			projects = append(projects, project)
		}
		cfg.AllowedProjects = projects
	}
	return cfg
}
