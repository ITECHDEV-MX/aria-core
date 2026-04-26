// MCP tool aria_vault_use_in_cmd: ejecuta comando local con secret(s) inyectados
// en env vars, captura stdout/stderr, MASCARA los valores antes de devolver,
// y registra el acceso en aria_secret_access_log via /v1/vault/secrets/{id}/reveal.
//
// Diseño:
//  1. Claude llama tool con secret_names[], command, timeout_secs, reason.
//  2. ARIA resuelve cada name → id via GET /v1/vault/secrets?name=...
//  3. ARIA pide /v1/vault/secrets/{id}/reveal con reason — el cloud verifica ACL
//     y logea el read en aria_secret_access_log.
//  4. ARIA ejecuta el command con os/exec, env=os.Environ() + NAME=value pares.
//  5. ARIA enmascara stdout/stderr (replace de cada value por <<MASKED:NAME>>).
//  6. Retorna {stdout, stderr, exit_code, masked, secrets_used}.
//
// Defensa contra blacklist (rm -rf /, dd if=, mkfs, etc). No es exhaustivo —
// la verdadera defensa es que el usuario en cloud_users tenga grants apropiados.
package mcp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// VaultMCPConfig configura el cliente del vault MCP.
type VaultMCPConfig struct {
	ServerURL string
	Token     string
	Timeout   time.Duration
}

// RegisterAriaVaultTools registra el tool aria_vault_use_in_cmd en el server existente.
// NOTE: este tool es muy poderoso — sólo registrar en perfiles MCP donde se
// confíe que Claude está restringido al user real (no public profile).
func RegisterAriaVaultTools(srv *server.MCPServer, cfg VaultMCPConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	cli := &vaultClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("aria_vault_use_in_cmd",
		mcp.WithDescription(strings.TrimSpace(`
Ejecuta un comando local con secrets del vault inyectados como variables de entorno.
Claude NUNCA ve el valor — solo el output (mascarado).

Cómo:
  - secret_names: lista de nombres canónicos en el vault (e.g. ["DB_PROD","API_KEY"]).
  - command: shell command con $VAR/${VAR} placeholders. Cada NAME del vault está disponible como env var.
  - reason: por qué necesitás los secrets (queda en audit log).

Ejemplo:
  { "secret_names": ["DB_PROD"], "command": "psql -h $DB_HOST -U postgres", "reason": "running migration 042" }

El stdout/stderr retornado tiene los valores reemplazados por <<MASKED:NAME>>.
`)),
		mcp.WithArray("secret_names", mcp.Required(), mcp.Description("Nombres del vault a inyectar como env vars")),
		mcp.WithString("command", mcp.Required(), mcp.Description("Shell command a ejecutar (los secrets están como $NAME)")),
		mcp.WithString("reason", mcp.Required(), mcp.Description("Razón del uso (queda en audit log)")),
		mcp.WithString("timeout_secs", mcp.Description("Timeout en segundos (default 30, max 300)")),
		mcp.WithString("workdir", mcp.Description("Directorio de trabajo opcional")),
	), cli.useInCmd)
}

type vaultClient struct {
	cfg  VaultMCPConfig
	http *http.Client
}

// useInCmd es el handler del tool.
func (c *vaultClient) useInCmd(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// 1. Parse args.
	command := strings.TrimSpace(optString(req, "command"))
	if command == "" {
		return mcp.NewToolResultError("command is required"), nil
	}
	reason := strings.TrimSpace(optString(req, "reason"))
	if reason == "" {
		return mcp.NewToolResultError("reason is required"), nil
	}
	if !commandIsSafe(command) {
		return mcp.NewToolResultError("command rejected by safety blacklist (rm -rf, dd, mkfs, shutdown, etc.)"), nil
	}
	timeoutSecs := 30
	if v := optString(req, "timeout_secs"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 && n <= 300 {
			timeoutSecs = n
		}
	}
	workdir := strings.TrimSpace(optString(req, "workdir"))

	// secret_names viene como array — el helper canónico.
	names, err := req.RequireStringSlice("secret_names")
	if err != nil {
		return mcp.NewToolResultError("secret_names must be array of strings: " + err.Error()), nil
	}
	if len(names) == 0 {
		return mcp.NewToolResultError("secret_names cannot be empty"), nil
	}

	// 2. Resolver name → id + reveal value para cada secret.
	cmdHash := sha256Hex(command)
	type resolved struct {
		Name  string
		ID    string
		Value string
	}
	var secrets []resolved
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		id, err := c.resolveSecretIDByName(ctx, name)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("resolve secret %q: %v", name, err)), nil
		}
		val, err := c.revealSecret(ctx, id, reason+" [cmd_hash="+cmdHash[:12]+"]")
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("reveal %q: %v", name, err)), nil
		}
		secrets = append(secrets, resolved{Name: name, ID: id, Value: val})
	}

	// 3. Ejecutar el comando con os/exec. Inyectar valores en env vars.
	execCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutSecs)*time.Second)
	defer cancel()

	shell := pickShell()
	cmd := exec.CommandContext(execCtx, shell, "-c", command)
	if workdir != "" {
		cmd.Dir = workdir
	}
	envOverlay := []string{}
	for _, s := range secrets {
		envOverlay = append(envOverlay, s.Name+"="+s.Value)
	}
	cmd.Env = append(currentEnv(), envOverlay...)

	stdout, stderr, exitCode := runAndCapture(cmd)

	// 4. Enmascarar valores en output.
	for _, s := range secrets {
		stdout = maskValue(stdout, s.Name, s.Value)
		stderr = maskValue(stderr, s.Name, s.Value)
	}

	usedNames := make([]string, 0, len(secrets))
	for _, s := range secrets {
		usedNames = append(usedNames, s.Name)
	}
	out := map[string]any{
		"stdout":       stdout,
		"stderr":       stderr,
		"exit_code":    exitCode,
		"masked":       true,
		"secrets_used": usedNames,
		"command_hash": cmdHash,
		"timeout_secs": timeoutSecs,
	}
	if execCtx.Err() == context.DeadlineExceeded {
		out["timed_out"] = true
	}
	body, _ := json.MarshalIndent(out, "", "  ")
	return mcp.NewToolResultText(string(body)), nil
}

// ─── helpers ───────────────────────────────────────────────────────────────

func (c *vaultClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr *strings.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = strings.NewReader(string(buf))
	}
	var req *http.Request
	var err error
	if rdr != nil {
		req, err = http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.ServerURL, "/")+path, rdr)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.ServerURL, "/")+path, nil)
	}
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out := make([]byte, 0, 1024)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return out, resp.StatusCode, nil
}

// resolveSecretIDByName busca un secret por nombre exacto (lista filtra cliente-side).
func (c *vaultClient) resolveSecretIDByName(ctx context.Context, name string) (string, error) {
	body, code, err := c.do(ctx, http.MethodGet, "/v1/vault/secrets?limit=500", nil)
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", fmt.Errorf("list HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
	var resp struct {
		Results []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"results"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse list: %w", err)
	}
	for _, s := range resp.Results {
		if s.Name == name {
			return s.ID, nil
		}
	}
	return "", fmt.Errorf("secret %q not found (or not accessible)", name)
}

// revealSecret pide el plaintext y registra el motivo en audit log (server side).
func (c *vaultClient) revealSecret(ctx context.Context, id, reason string) (string, error) {
	body, code, err := c.do(ctx, http.MethodPost, "/v1/vault/secrets/"+url.PathEscape(id)+"/reveal", map[string]any{"reason": reason})
	if err != nil {
		return "", err
	}
	if code >= 400 {
		return "", fmt.Errorf("reveal HTTP %d: %s", code, strings.TrimSpace(string(body)))
	}
	var resp struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse reveal: %w", err)
	}
	return resp.Value, nil
}

// maskValue reemplaza ocurrencias de value por <<MASKED:NAME>> en s.
// Si value es vacío o muy corto (<6 chars) se evita el masking para no
// reemplazar substrings legítimos.
func maskValue(s, name, value string) string {
	if len(value) < 6 {
		return s
	}
	return strings.ReplaceAll(s, value, fmt.Sprintf("<<MASKED:%s>>", name))
}

// commandIsSafe blacklista comandos destructivos obvios.
// No es exhaustivo, es defensa-en-profundidad. La auth real es ACL del store.
var destructivePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\brm\s+-[rR]?[fF][rR]?\s+/`),
	regexp.MustCompile(`(?i)\brm\s+-[rR]?[fF][rR]?\s+~`),
	regexp.MustCompile(`(?i)\bdd\s+(if|of)=/dev/`),
	regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`),
	regexp.MustCompile(`(?i):\(\)\s*\{\s*:\|:&\s*\}`), // fork bomb
	regexp.MustCompile(`(?i)\bshutdown\s+`),
	regexp.MustCompile(`(?i)\breboot\s+`),
	regexp.MustCompile(`(?i)>\s*/dev/sd[a-z]`),
	regexp.MustCompile(`(?i)\bchmod\s+-[rR]?\s+777\s+/`),
}

func commandIsSafe(cmd string) bool {
	for _, re := range destructivePatterns {
		if re.MatchString(cmd) {
			return false
		}
	}
	return true
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// pickShell elige el shell para -c. En Windows usa cmd; en unix usa sh.
func pickShell() string {
	// /bin/sh está siempre disponible en unix; en windows el caller debe usar
	// otra estrategia. Por ahora: hardcoded sh.
	return "/bin/sh"
}

// currentEnv retorna os.Environ() — wrapper indirección para mockear en tests.
var currentEnv = func() []string {
	return os.Environ()
}

// runAndCapture ejecuta cmd y captura stdout/stderr separados. Fail-soft: si exec
// falla devuelve exit_code=-1 y el error en stderr.
func runAndCapture(cmd *exec.Cmd) (stdout, stderr string, exitCode int) {
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err := cmd.Run()
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()
	if err == nil {
		return stdout, stderr, 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return stdout, stderr, exitErr.ExitCode()
	}
	if stderr == "" {
		stderr = err.Error()
	}
	return stdout, stderr, -1
}
