// MCP tools del módulo Pages (mini-Notion).
//
// Expone aria_page_create, aria_page_get, aria_page_search, aria_pages_tree
// como un thin shim sobre /v1/pages/* del cloudserver. Usa el mismo JWT
// user-bound que los demás profile tools (cargado desde session.json).
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// PagesMCPConfig configura el cliente HTTP para los tools de pages.
type PagesMCPConfig struct {
	ServerURL string
	Token     string
	Timeout   time.Duration
}

// RegisterAriaPagesTools registra los 4 tools de pages en el MCP server.
//
//	aria_page_create: nueva página (con template opcional + parent_id).
//	aria_page_get:    fetch por id (UUID).
//	aria_page_search: búsqueda cross-everything (Cmd+K).
//	aria_pages_tree:  jerarquía completa filtrable por project/scope.
func RegisterAriaPagesTools(srv *server.MCPServer, cfg PagesMCPConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	cli := &pagesClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("aria_page_create",
		mcp.WithDescription(strings.TrimSpace(`
Crea una nueva página en el wiki ARIA (mini-Notion). Si pasás template_key
(prd-v1, incident-v1, one-on-one-v1, adr-v1, client-onboarding-v1), el body
se hidrata desde el template builtin si content_md viene vacío.

parent_id permite anidar páginas para construir jerarquía.
`)),
		mcp.WithString("title", mcp.Required(), mcp.Description("Título de la página")),
		mcp.WithString("content", mcp.Description("Contenido markdown")),
		mcp.WithString("parent_id", mcp.Description("UUID de la página padre (opcional)")),
		mcp.WithString("template", mcp.Description("Template key: prd-v1 | incident-v1 | one-on-one-v1 | adr-v1 | client-onboarding-v1")),
		mcp.WithString("icon", mcp.Description("Emoji opcional")),
		mcp.WithString("project", mcp.Description("Proyecto al que pertenece")),
		mcp.WithString("scope", mcp.Description("personal | project | team | client_knowledge (default: team)")),
		mcp.WithString("sensitivity", mcp.Description("public | internal | client | confidential")),
	), cli.createPage)

	srv.AddTool(mcp.NewTool("aria_page_get",
		mcp.WithDescription("Obtiene el contenido completo de una página por UUID, incluyendo breadcrumbs (path)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID de la página")),
	), cli.getPage)

	srv.AddTool(mcp.NewTool("aria_page_search",
		mcp.WithDescription(strings.TrimSpace(`
Búsqueda cross-everything del wiki + memorias + skills + recipes + leads + cotizaciones
(equivalente al Cmd+K del dashboard). FTS spanish con tolerancia a acentos.
Retorna top-N por fuente, ordenados por score.
`)),
		mcp.WithString("query", mcp.Required(), mcp.Description("Texto a buscar (FTS spanish accent-insensitive)")),
		mcp.WithString("limit", mcp.Description("Máximo por fuente (default 5, max 50)")),
	), cli.searchPages)

	srv.AddTool(mcp.NewTool("aria_pages_tree",
		mcp.WithDescription(strings.TrimSpace(`
Devuelve la estructura jerárquica de páginas (no archivadas) filtrable por project/scope.
Cada nodo incluye children_count para que Claude pueda reconstruir el árbol.
`)),
		mcp.WithString("project", mcp.Description("Filtrar por proyecto")),
		mcp.WithString("scope", mcp.Description("personal | project | team | client_knowledge")),
	), cli.treePages)
}

type pagesClient struct {
	cfg  PagesMCPConfig
	http *http.Client
}

func (c *pagesClient) createPage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	body := map[string]string{
		"title":       optString(req, "title"),
		"content":     optString(req, "content"),
		"parent_id":   optString(req, "parent_id"),
		"template":    optString(req, "template"),
		"icon":        optString(req, "icon"),
		"project":     optString(req, "project"),
		"scope":       optString(req, "scope"),
		"sensitivity": optString(req, "sensitivity"),
	}
	if strings.TrimSpace(body["title"]) == "" {
		return mcp.NewToolResultError("title is required"), nil
	}
	resp, status, err := c.do(ctx, http.MethodPost, "/v1/pages", body)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("create page: %d %s", status, string(resp))), nil
	}
	return mcp.NewToolResultText(string(resp)), nil
}

func (c *pagesClient) getPage(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id := strings.TrimSpace(optString(req, "id"))
	if id == "" {
		return mcp.NewToolResultError("id is required"), nil
	}
	resp, status, err := c.do(ctx, http.MethodGet, "/v1/pages/"+url.PathEscape(id), nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("get page: %d %s", status, string(resp))), nil
	}
	return mcp.NewToolResultText(string(resp)), nil
}

func (c *pagesClient) searchPages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query := strings.TrimSpace(optString(req, "query"))
	if query == "" {
		return mcp.NewToolResultError("query is required"), nil
	}
	limit := 5
	if v := optString(req, "limit"); v != "" {
		if n, _ := strconv.Atoi(v); n > 0 && n <= 50 {
			limit = n
		}
	}
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	resp, status, err := c.do(ctx, http.MethodGet, "/v1/pages/search?"+q.Encode(), nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("search pages: %d %s", status, string(resp))), nil
	}
	return mcp.NewToolResultText(string(resp)), nil
}

func (c *pagesClient) treePages(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := url.Values{}
	if v := strings.TrimSpace(optString(req, "project")); v != "" {
		q.Set("project", v)
	}
	if v := strings.TrimSpace(optString(req, "scope")); v != "" {
		q.Set("scope", v)
	}
	path := "/v1/pages/tree"
	if encoded := q.Encode(); encoded != "" {
		path = path + "?" + encoded
	}
	resp, status, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("pages tree: %d %s", status, string(resp))), nil
	}
	return mcp.NewToolResultText(string(resp)), nil
}

func (c *pagesClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
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
	url := strings.TrimRight(c.cfg.ServerURL, "/") + path
	if rdr != nil {
		req, err = http.NewRequestWithContext(ctx, method, url, rdr)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, url, nil)
	}
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}
