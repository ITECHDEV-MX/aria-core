// MCP tool aria_recipe_run: ejecuta un recipe ejecutable contra el cloud
// y retorna la traza completa (status por step, durations, stderr enmascarado
// si el recipe usó vault). Acepta dry_run=true para listar steps sin ejecutar.
//
// El runner real vive en internal/cloud/recipes y se invoca via HTTP
// /v1/recipes/run con JWT user-bound. Este MCP shim sólo es transporte.
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RecipeMCPConfig configura el cliente MCP para hablar con /v1/recipes/*.
type RecipeMCPConfig struct {
	ServerURL string
	Token     string
	Timeout   time.Duration
}

// RegisterAriaRecipeTools agrega el tool aria_recipe_run en el server existente.
func RegisterAriaRecipeTools(srv *server.MCPServer, cfg RecipeMCPConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 5 * time.Minute
	}
	cli := &recipeClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("aria_recipe_run",
		mcp.WithDescription(strings.TrimSpace(`
Ejecuta un recipe ejecutable de ARIA Core (deploy, backup, onboarding, etc.) y retorna
la traza completa: status por step, exit codes, stdout/stderr (masked si vault), duración total.

Cómo funciona:
  - recipe_key: clave estable del recipe (ej. 'deploy-aria-core').
  - vars: map de strings, expuestos a step templates como {{ .Vars.* }}.
  - dry_run=true: NO ejecuta nada, retorna lista de steps + estimated duration.

Use aria_get_recipes para descubrir recipes disponibles antes de invocar.
`)),
		mcp.WithString("recipe_key", mcp.Required(), mcp.Description("Clave estable del recipe (ej. deploy-aria-core)")),
		mcp.WithObject("vars", mcp.Description("Variables expuestas a step templates como {{ .Vars.* }}")),
		mcp.WithString("project", mcp.Description("Project para el registro de telemetría (opcional)")),
		mcp.WithBoolean("dry_run", mcp.Description("Si true, no ejecuta los steps; sólo retorna el plan estimado")),
	), cli.runRecipe)

	srv.AddTool(mcp.NewTool("aria_recipe_executions",
		mcp.WithDescription("Lista las últimas ejecuciones de recipes con status, duración, recipe_key y dev. Útil para auditoría/ROI."),
		mcp.WithString("recipe_key", mcp.Description("Filtrar por recipe key")),
		mcp.WithString("status", mcp.Description("Filtrar por status: success | failed | running | cancelled")),
		mcp.WithString("days", mcp.Description("Solo últimas N días (default: 30)")),
		mcp.WithString("limit", mcp.Description("Máximo (default 50)")),
	), cli.listExecutions)
}

type recipeClient struct {
	cfg  RecipeMCPConfig
	http *http.Client
}

func (c *recipeClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
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

func (c *recipeClient) runRecipe(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	key, err := req.RequireString("recipe_key")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]any{
		"recipe_key": key,
		"project":    optString(req, "project"),
		"dry_run":    boolFromReq(req, "dry_run"),
	}
	if vars, ok := req.GetArguments()["vars"].(map[string]any); ok {
		out := make(map[string]string, len(vars))
		for k, v := range vars {
			if s, ok := v.(string); ok {
				out[k] = s
			} else {
				out[k] = toString(v)
			}
		}
		payload["vars"] = out
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/recipes/run", payload)
	return mcpResultFromHTTP("aria_recipe_run", body, code, err2)
}

func (c *recipeClient) listExecutions(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := "/v1/recipes/executions?"
	if v := optString(req, "recipe_key"); v != "" {
		q += "recipe_key=" + v + "&"
	}
	if v := optString(req, "status"); v != "" {
		q += "status=" + v + "&"
	}
	if v := optString(req, "days"); v != "" {
		q += "days=" + v + "&"
	}
	if v := optString(req, "limit"); v != "" {
		q += "limit=" + v + "&"
	}
	body, code, err := c.do(ctx, http.MethodGet, strings.TrimRight(q, "&?"), nil)
	return mcpResultFromHTTP("aria_recipe_executions", body, code, err)
}

func boolFromReq(req mcp.CallToolRequest, key string) bool {
	v, ok := req.GetArguments()[key].(bool)
	if !ok {
		return false
	}
	return v
}

func toString(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	}
	b, _ := json.Marshal(v)
	return string(b)
}
