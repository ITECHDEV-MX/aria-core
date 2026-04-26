// Cotizador cloud MCP profile: tools que hablan con el cloud REST API
// vía JWT user-bound. Diseñado para correr embebido en Claude Desktop con
// plan Max (el LLM razona del lado Claude, aria-core es servidor de tools).
package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// CotizadorCloudConfig configura el cliente HTTP para hablar con el cloud.
type CotizadorCloudConfig struct {
	ServerURL string
	Token     string // JWT user-bound (de session.json)
	Timeout   time.Duration
}

// NewBareServer retorna un MCP server vacío con name+version dados.
// Útil para profiles cloud-bound que no necesitan el storage local.
func NewBareServer(name, version string) *server.MCPServer {
	return server.NewMCPServer(name, version)
}

// RegisterCotizadorCloudTools registra las tools del módulo Cotizador en
// el MCP server, conectadas al cloud REST.
func RegisterCotizadorCloudTools(srv *server.MCPServer, cfg CotizadorCloudConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	cli := &cotizadorClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("cotizador_list_leads",
		mcp.WithDescription("Lista leads del módulo Cotizador. Filtra opcionalmente por status (new|contacted|qualified|quoting|won|lost)."),
		mcp.WithString("status", mcp.Description("Filtrar por status; vacío = todos.")),
	), cli.listLeads)

	srv.AddTool(mcp.NewTool("cotizador_get_lead",
		mcp.WithDescription("Detalle de un lead por ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID del lead")),
	), cli.getLead)

	srv.AddTool(mcp.NewTool("cotizador_create_lead",
		mcp.WithDescription("Crea un nuevo lead. El lead queda con status='new' y created_by_role del usuario autenticado."),
		mcp.WithString("name", mcp.Required(), mcp.Description("Nombre del contacto")),
		mcp.WithString("company", mcp.Description("Empresa")),
		mcp.WithString("email", mcp.Description("Email del contacto")),
		mcp.WithString("phone", mcp.Description("Teléfono")),
		mcp.WithString("source", mcp.Description("Origen del lead: referido, LinkedIn, evento, etc.")),
		mcp.WithString("notes", mcp.Description("Notas iniciales")),
	), cli.createLead)

	srv.AddTool(mcp.NewTool("cotizador_update_lead_status",
		mcp.WithDescription("Cambia el estado del lead. Estados válidos: new, contacted, qualified, quoting, won, lost. Genera audit log."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID del lead")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Nuevo status")),
		mcp.WithString("notes", mcp.Description("Notas opcionales del cambio")),
	), cli.updateStatus)

	srv.AddTool(mcp.NewTool("cotizador_lead_history",
		mcp.WithDescription("Historial de cambios de un lead (audit log)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID del lead")),
	), cli.leadHistory)
}

type cotizadorClient struct {
	cfg  CotizadorCloudConfig
	http *http.Client
}

func (c *cotizadorClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.cfg.ServerURL, "/")+path, rdr)
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
	out, _ := io.ReadAll(resp.Body)
	return out, resp.StatusCode, nil
}

func (c *cotizadorClient) listLeads(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	status, _ := req.RequireString("status")
	path := "/v1/cotizador/leads"
	if strings.TrimSpace(status) != "" {
		path += "?status=" + status
	}
	body, code, err := c.do(ctx, http.MethodGet, path, nil)
	return mcpResultFromHTTP("list leads", body, code, err)
}

func (c *cotizadorClient) getLead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/leads/"+id, nil)
	return mcpResultFromHTTP("get lead", body, code, err2)
}

func (c *cotizadorClient) createLead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	name, err := req.RequireString("name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]string{
		"name":    name,
		"company": optString(req, "company"),
		"email":   optString(req, "email"),
		"phone":   optString(req, "phone"),
		"source":  optString(req, "source"),
		"notes":   optString(req, "notes"),
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/leads", payload)
	return mcpResultFromHTTP("create lead", body, code, err2)
}

func (c *cotizadorClient) updateStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status, err := req.RequireString("status")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]string{
		"status": status,
		"notes":  optString(req, "notes"),
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/leads/"+id+"/status", payload)
	return mcpResultFromHTTP("update status", body, code, err2)
}

func (c *cotizadorClient) leadHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/leads/"+id+"/history", nil)
	return mcpResultFromHTTP("lead history", body, code, err2)
}

func optString(req mcp.CallToolRequest, key string) string {
	v, _ := req.RequireString(key)
	return strings.TrimSpace(v)
}

func mcpResultFromHTTP(action string, body []byte, code int, err error) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("%s: %v", action, err)), nil
	}
	if code >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("%s failed (HTTP %d): %s", action, code, strings.TrimSpace(string(body)))), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
