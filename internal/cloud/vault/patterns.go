// Leak detection patterns. Usado por:
//   1. aria_save: bloquear narrative/facts con credenciales accidentales
//   2. CLI: aria-core vault scan FILE
//
// Política: false negative > false positive (preferimos dejar pasar antes que
// bloquear narrative legítimo). Igual la lista cubre los typical:
//   - AWS access keys / secret keys
//   - GitHub/GitLab Personal Access Tokens
//   - Slack/Stripe/Twilio/etc tokens con prefijos canónicos
//   - JWT (header.payload.signature)
//   - PRIVATE KEY blocks (RSA/EC/OpenSSH/PGP)
//   - password=... env-style assignments
//   - Generic hex/base64 secrets >= 32 chars precedidos por keyword
package vault

import (
	"fmt"
	"regexp"
	"strings"
)

// LeakMatch describe una coincidencia de pattern.
type LeakMatch struct {
	Pattern  string // identificador legible: AWS_AK, GH_PAT, JWT, etc.
	Match    string // substring matched (truncado a 80 chars + "..." si más largo)
	Severity string // "high" | "medium" | "low"
	Hint     string // sugerencia para el dev (e.g. "convertir a vault secret")
}

// LeakDetector escanea texto buscando patterns de credenciales.
type LeakDetector interface {
	Scan(text string) []LeakMatch
}

type detector struct {
	patterns []namedPattern
}

type namedPattern struct {
	name     string
	severity string
	hint     string
	re       *regexp.Regexp
}

// NewLeakDetector retorna un detector con la lista canónica de patterns.
func NewLeakDetector() LeakDetector {
	return &detector{patterns: defaultPatterns()}
}

func defaultPatterns() []namedPattern {
	return []namedPattern{
		{
			name:     "AWS_AK",
			severity: "high",
			hint:     "AWS Access Key ID detectada. Convertir a vault secret category=api_token",
			// AKIA / ASIA + 16 chars uppercase/digits.
			re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`),
		},
		{
			name:     "AWS_SK",
			severity: "high",
			hint:     "Posible AWS Secret Access Key (40 chars b64-like asociado a aws_secret).",
			// aws_secret_access_key="..." con 40 chars [A-Za-z0-9/+=].
			re: regexp.MustCompile(`(?i)aws[_\-]?secret[_\-]?access[_\-]?key["'\s:=]+([A-Za-z0-9/+=]{40})\b`),
		},
		{
			name:     "GH_PAT",
			severity: "high",
			hint:     "GitHub Personal Access Token (ghp_/gho_/ghu_/ghs_/ghr_).",
			re:       regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b`),
		},
		{
			name:     "GH_OAUTH",
			severity: "high",
			hint:     "GitHub OAuth token (github_pat_).",
			re:       regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{60,}\b`),
		},
		{
			name:     "GITLAB_PAT",
			severity: "high",
			hint:     "GitLab Personal Access Token (glpat-).",
			re:       regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}\b`),
		},
		{
			name:     "SLACK_TOKEN",
			severity: "high",
			hint:     "Slack token (xox[bpsa]-...).",
			re:       regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`),
		},
		{
			name:     "STRIPE_KEY",
			severity: "high",
			hint:     "Stripe secret/restricted key (sk_live_/rk_live_/sk_test_).",
			re:       regexp.MustCompile(`\b(?:sk|rk)_(?:live|test)_[A-Za-z0-9]{20,}\b`),
		},
		{
			name:     "GOOGLE_API",
			severity: "medium",
			hint:     "Google API Key (AIza...).",
			re:       regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`),
		},
		{
			name:     "JWT",
			severity: "medium",
			hint:     "JWT (header.payload.signature). Si es token vivo → vault.",
			// 3 segmentos base64url separados por '.', con segundo segmento empezando 'ey'.
			re: regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\b`),
		},
		{
			name:     "BEGIN_PRIVATE_KEY",
			severity: "high",
			hint:     "PEM private key block. Convertir a vault secret category=ssh_key|cert.",
			re:       regexp.MustCompile(`-----BEGIN (?:RSA |DSA |EC |OPENSSH |PGP )?PRIVATE KEY-----`),
		},
		{
			name:     "PASSWORD_ASSIGN",
			severity: "medium",
			hint:     "password=... en texto plano. Si es real, convertir a vault.",
			// password=secret, password: 'secret', DB_PASSWORD="secret" — captura valor >=8 chars no-whitespace.
			// Usamos (?:^|[^A-Za-z]) en vez de \b porque _PASSWORD no rompe en \b.
			re: regexp.MustCompile(`(?i)(?:^|[^A-Za-z])(?:password|passwd|pwd)\s*[:=]\s*["']?([^\s"',;]{8,})["']?`),
		},
		{
			name:     "GENERIC_API_KEY",
			severity: "low",
			hint:     "Asignación api_key=... con valor largo. Verificar si es secret real.",
			re:       regexp.MustCompile(`(?i)(?:^|[^A-Za-z])(?:api[_\-]?key|api[_\-]?secret|access[_\-]?token|auth[_\-]?token|secret[_\-]?key)\s*[:=]\s*["']?([A-Za-z0-9_\-/+=]{20,})["']?`),
		},
		{
			name:     "GCP_SA_PRIVATE",
			severity: "high",
			hint:     "GCP service account JSON con private_key field.",
			re:       regexp.MustCompile(`"private_key"\s*:\s*"-----BEGIN`),
		},
	}
}

func (d *detector) Scan(text string) []LeakMatch {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	var out []LeakMatch
	seen := make(map[string]struct{})
	for _, p := range d.patterns {
		matches := p.re.FindAllString(text, -1)
		for _, m := range matches {
			// dedupe por (pattern, match) — mismo pattern muchas veces no spamea.
			key := p.name + "::" + m
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, LeakMatch{
				Pattern:  p.name,
				Match:    truncate(m, 80),
				Severity: p.severity,
				Hint:     p.hint,
			})
		}
	}
	return out
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// FormatLeakError produce un mensaje de error human-friendly para un set de matches.
// Lo usa ariamem.Save() cuando bloquea el insert.
func FormatLeakError(matches []LeakMatch) error {
	if len(matches) == 0 {
		return nil
	}
	patterns := make([]string, 0, len(matches))
	for _, m := range matches {
		patterns = append(patterns, m.Pattern)
	}
	return fmt.Errorf(
		"detected potential secret in observation (patterns: %s). Crear como vault secret en su lugar (aria-core vault create), o pasar force_save=true para sobreescribir el bloqueo",
		strings.Join(uniqueStrings(patterns), ", "),
	)
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
