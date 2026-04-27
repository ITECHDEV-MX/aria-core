// Package github implementa el cliente HTTP minimalista contra la GitHub API
// que ARIA Core necesita para wave 7 (gestión proyectos del equipo).
//
// Capabilities:
//   - CreateRepo: POST /orgs/{org}/repos (privado por default).
//   - InviteCollaborator: PUT /repos/{owner}/{repo}/collaborators/{user}.
//   - CreateIssue: POST /repos/{owner}/{repo}/issues.
//   - GetIssue: GET /repos/{owner}/{repo}/issues/{number}.
//   - ListCommits: GET /repos/{owner}/{repo}/commits?since=&until=&author=
//
// Auth: Bearer <PAT> + Accept: application/vnd.github+json. El PAT se lee
// del vault (secret name GITHUB_API_TOKEN) en NewFromVault.
//
// Importante: este paquete NO importa nada de teamprojects/cloudserver para
// evitar ciclos. La integración de alto nivel vive en internal/cloud/teamprojects
// + cmd/aria-core/teamprojects_adapter.go.
package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// encodeBase64 retorna content como base64 standard (sin saltos de línea — la
// API de GitHub acepta ambas formas pero envía con line breaks; nosotros
// mandamos compacto).
func encodeBase64(content []byte) string {
	return base64.StdEncoding.EncodeToString(content)
}

// decodeBase64Lines acepta base64 con o sin line breaks (la API de GitHub
// retorna content separado en líneas de 60 chars).
func decodeBase64Lines(s string) ([]byte, error) {
	cleaned := strings.NewReplacer("\n", "", "\r", "", " ", "").Replace(s)
	return base64.StdEncoding.DecodeString(cleaned)
}

const defaultBaseURL = "https://api.github.com"

// Errores públicos.
var (
	ErrUnauthorized = errors.New("github: unauthorized (token inválido o sin scopes)")
	ErrNotFound     = errors.New("github: not found")
	ErrAPI          = errors.New("github: api error")
)

// VaultLike es la interfaz mínima que usamos del vault para resolver el PAT.
// Implementación: cmd/aria-core/teamprojects_adapter usa vault.PgStore.
// Devuelve plaintext del secret cuando existe + permitido.
type VaultLike interface {
	RevealByName(ctx context.Context, name string) (string, error)
}

// Client es el cliente HTTP + token + org default.
type Client struct {
	token   string
	org     string
	baseURL string
	http    *http.Client
}

// Config configura NewClient.
type Config struct {
	Token   string
	Org     string
	BaseURL string // override para tests; default https://api.github.com
	HTTP    *http.Client
}

// NewClient construye un cliente con un PAT directo (NO lee del vault).
// Útil para tests con mock HTTP server.
func NewClient(cfg Config) (*Client, error) {
	tok := strings.TrimSpace(cfg.Token)
	if tok == "" {
		return nil, fmt.Errorf("github: token required (set GITHUB_API_TOKEN in vault)")
	}
	org := strings.TrimSpace(cfg.Org)
	if org == "" {
		org = "ITECHDEV-MX"
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = defaultBaseURL
	}
	hc := cfg.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{token: tok, org: org, baseURL: base, http: hc}, nil
}

// NewFromVault lee GITHUB_API_TOKEN del vault y construye el cliente.
// Si el secret no existe o el vault está degraded, retorna error claro.
func NewFromVault(ctx context.Context, vault VaultLike, org string) (*Client, error) {
	if vault == nil {
		return nil, fmt.Errorf("github: vault is required")
	}
	tok, err := vault.RevealByName(ctx, "GITHUB_API_TOKEN")
	if err != nil {
		return nil, fmt.Errorf("github: load token from vault: %w", err)
	}
	return NewClient(Config{Token: tok, Org: org})
}

// Org retorna el org default configurado.
func (c *Client) Org() string {
	if c == nil {
		return ""
	}
	return c.org
}

// ─── Repos ──────────────────────────────────────────────────────────────

// Repo es la representación pública de un repo.
type Repo struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	HTMLURL       string `json:"html_url"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
}

// CreateRepoParams agrupa el body del create-repo.
type CreateRepoParams struct {
	Name        string
	Description string
	Private     bool
	AutoInit    bool
	GitIgnore   string // ej. "Go" — opcional template del .gitignore
	License     string // ej. "mit"
}

// CreateRepo crea un repo en la org configurada.
//
// POST /orgs/{org}/repos con body { name, description, private, auto_init,
// gitignore_template, license_template }.
func (c *Client) CreateRepo(ctx context.Context, p CreateRepoParams) (*Repo, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return nil, fmt.Errorf("github: repo name required")
	}
	body := map[string]any{
		"name":        name,
		"description": strings.TrimSpace(p.Description),
		"private":     p.Private,
		"auto_init":   p.AutoInit,
	}
	if g := strings.TrimSpace(p.GitIgnore); g != "" {
		body["gitignore_template"] = g
	}
	if l := strings.TrimSpace(p.License); l != "" {
		body["license_template"] = l
	}
	var repo Repo
	if err := c.do(ctx, http.MethodPost, "/orgs/"+c.org+"/repos", body, &repo); err != nil {
		return nil, err
	}
	return &repo, nil
}

// InviteCollaborator agrega/invita a un colaborador a un repo.
//
// PUT /repos/{owner}/{repo}/collaborators/{username}
// permission: pull|triage|push|maintain|admin (default: push).
func (c *Client) InviteCollaborator(ctx context.Context, repoOwner, repo, ghUsername, permission string) error {
	if c == nil {
		return fmt.Errorf("github: nil client")
	}
	if strings.TrimSpace(repoOwner) == "" || strings.TrimSpace(repo) == "" || strings.TrimSpace(ghUsername) == "" {
		return fmt.Errorf("github: owner/repo/username required")
	}
	if permission == "" {
		permission = "push"
	}
	body := map[string]any{"permission": permission}
	path := fmt.Sprintf("/repos/%s/%s/collaborators/%s", repoOwner, repo, ghUsername)
	return c.do(ctx, http.MethodPut, path, body, nil)
}

// ─── Issues ─────────────────────────────────────────────────────────────

// Issue es una issue de GitHub.
type Issue struct {
	ID      int64    `json:"id"`
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	HTMLURL string   `json:"html_url"`
	State   string   `json:"state"`
}

// CreateIssue abre una issue.
func (c *Client) CreateIssue(ctx context.Context, owner, repo, title, body string, assignees []string) (*Issue, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	pl := map[string]any{
		"title": title,
		"body":  body,
	}
	if len(assignees) > 0 {
		pl["assignees"] = assignees
	}
	var iss Issue
	if err := c.do(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/%s/issues", owner, repo), pl, &iss); err != nil {
		return nil, err
	}
	return &iss, nil
}

// GetIssue lee una issue por número.
func (c *Client) GetIssue(ctx context.Context, owner, repo string, number int) (*Issue, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	var iss Issue
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number), nil, &iss); err != nil {
		return nil, err
	}
	return &iss, nil
}

// ─── Commits ────────────────────────────────────────────────────────────

// Commit representa el shape mínimo del response GET /commits.
type Commit struct {
	SHA       string `json:"sha"`
	HTMLURL   string `json:"html_url"`
	CommitObj struct {
		Message string `json:"message"`
		Author  struct {
			Name  string    `json:"name"`
			Email string    `json:"email"`
			Date  time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
}

// ListCommits lista commits de un repo, opcional filtrado por author + rango temporal.
func (c *Client) ListCommits(ctx context.Context, owner, repo string, since, until time.Time, author string) ([]Commit, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	q := url.Values{}
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339))
	}
	if !until.IsZero() {
		q.Set("until", until.UTC().Format(time.RFC3339))
	}
	if strings.TrimSpace(author) != "" {
		q.Set("author", author)
	}
	q.Set("per_page", "100")
	path := fmt.Sprintf("/repos/%s/%s/commits?%s", owner, repo, q.Encode())
	var out []Commit
	if err := c.do(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// ─── Repos: Get ─────────────────────────────────────────────────────────

// GetRepo lee un repo. Retorna (nil, nil) si no existe (404 → no error).
func (c *Client) GetRepo(ctx context.Context, owner, repo string) (*Repo, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	var r Repo
	if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s", owner, repo), nil, &r); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// ─── Contents: Files ────────────────────────────────────────────────────

// FileContent es la respuesta de GET /repos/{owner}/{repo}/contents/{path}
// cuando path apunta a un archivo.
type FileContent struct {
	Type        string `json:"type"`
	Path        string `json:"path"`
	Name        string `json:"name"`
	SHA         string `json:"sha"`
	Size        int64  `json:"size"`
	Content     string `json:"content"`  // base64-encoded
	Encoding    string `json:"encoding"` // "base64"
	HTMLURL     string `json:"html_url"`
	DownloadURL string `json:"download_url"`
}

// FileEntry es una entrada del listado de directorio.
type FileEntry struct {
	Type    string `json:"type"` // "file" | "dir"
	Path    string `json:"path"`
	Name    string `json:"name"`
	SHA     string `json:"sha"`
	Size    int64  `json:"size"`
	HTMLURL string `json:"html_url"`
}

// PutFileResult espeja la respuesta de PUT /contents/{path}.
type PutFileResult struct {
	Content struct {
		Path    string `json:"path"`
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
	} `json:"content"`
	Commit struct {
		SHA     string `json:"sha"`
		HTMLURL string `json:"html_url"`
	} `json:"commit"`
}

// GetFile lee un archivo del repo. Retorna (nil, nil) si no existe.
// El Content viene base64-decoded.
func (c *Client) GetFile(ctx context.Context, owner, repo, branch, path string) (*FileContent, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	q := ""
	if strings.TrimSpace(branch) != "" {
		q = "?ref=" + url.QueryEscape(branch)
	}
	var f FileContent
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/contents/%s%s", owner, repo, path, q), nil, &f)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if f.Encoding == "base64" && f.Content != "" {
		decoded, derr := decodeBase64Lines(f.Content)
		if derr != nil {
			return nil, fmt.Errorf("github: decode file content: %w", derr)
		}
		f.Content = string(decoded)
		f.Encoding = "utf-8"
	}
	return &f, nil
}

// PutFile crea o actualiza un archivo. Si previousSHA está vacío, asume
// creación; GitHub rechaza creates sobre archivos existentes con 422,
// así que el caller debe haber hecho GetFile antes para obtener el SHA.
func (c *Client) PutFile(ctx context.Context, owner, repo, branch, path string, content []byte, message, previousSHA string) (*PutFileResult, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	body := map[string]any{
		"message": message,
		"content": encodeBase64(content),
	}
	if strings.TrimSpace(branch) != "" {
		body["branch"] = branch
	}
	if strings.TrimSpace(previousSHA) != "" {
		body["sha"] = previousSHA
	}
	var res PutFileResult
	if err := c.do(ctx, http.MethodPut, fmt.Sprintf("/repos/%s/%s/contents/%s", owner, repo, path), body, &res); err != nil {
		return nil, err
	}
	return &res, nil
}

// ListFiles lista los entries del directorio. Si path es "", lista la raíz.
func (c *Client) ListFiles(ctx context.Context, owner, repo, branch, path string) ([]FileEntry, error) {
	if c == nil {
		return nil, fmt.Errorf("github: nil client")
	}
	q := ""
	if strings.TrimSpace(branch) != "" {
		q = "?ref=" + url.QueryEscape(branch)
	}
	var entries []FileEntry
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/%s/contents/%s%s", owner, repo, path, q), nil, &entries)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return entries, nil
}

// ─── HTTP plumbing ──────────────────────────────────────────────────────

func (c *Client) do(ctx context.Context, method, path string, body any, decodeInto any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: marshal body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	rawBody, _ := io.ReadAll(resp.Body)

	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		if decodeInto == nil || len(rawBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(rawBody, decodeInto); err != nil {
			return fmt.Errorf("github: decode response: %w", err)
		}
		return nil
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	default:
		// Try to extract a friendly error message.
		var apiErr struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(rawBody, &apiErr)
		msg := strings.TrimSpace(apiErr.Message)
		if msg == "" {
			msg = strings.TrimSpace(string(rawBody))
		}
		return fmt.Errorf("%w: status %d (%s): %s", ErrAPI, resp.StatusCode, http.StatusText(resp.StatusCode), msg)
	}
}

// ─── Helpers ────────────────────────────────────────────────────────────

// IssueNumberFromURL extrae el número de issue de una URL como
// https://github.com/foo/bar/issues/42. Retorna 0 si no matchea.
func IssueNumberFromURL(u string) int {
	idx := strings.LastIndex(u, "/issues/")
	if idx < 0 {
		return 0
	}
	tail := u[idx+len("/issues/"):]
	if i := strings.Index(tail, "/"); i >= 0 {
		tail = tail[:i]
	}
	if i := strings.Index(tail, "#"); i >= 0 {
		tail = tail[:i]
	}
	n, err := strconv.Atoi(tail)
	if err != nil {
		return 0
	}
	return n
}
