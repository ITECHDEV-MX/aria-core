// MCP tools del módulo knowledge-base sync (wave 8).
//
// Expone:
//
//	aria_kb_status            -- counts globales del tracking aria_kb_synced_entities.
//	aria_kb_sync_project      -- re-sincea TODO lo asociado a un project_id.
//	aria_kb_sync_quote        -- fuerza re-sync de una cotización al repo central.
//	aria_quote_export_docx    -- bytes DOCX de una cotización (base64) para que
//	                              Claude pueda ofrecer al usuario el archivo.
//
// Todas las tools son thin shims sobre /v1/knowledge-base/* y
// /v1/cotizador/quotes/{id}/export/docx del cloudserver. Usan el JWT
// user-bound (session.json), igual que las demás aria_* tools.
package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// KnowledgeBaseMCPConfig configura el cliente HTTP para los tools de KB.
type KnowledgeBaseMCPConfig struct {
	ServerURL string
	Token     string
	Timeout   time.Duration
}

// RegisterAriaKnowledgeBaseTools registra las 4 tools de KB en el MCP server.
func RegisterAriaKnowledgeBaseTools(srv *server.MCPServer, cfg KnowledgeBaseMCPConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	cli := &kbClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("aria_kb_status",
		mcp.WithDescription(strings.TrimSpace(`
Retorna counts globales del tracking aria_kb_synced_entities (total + por
sync_status). Útil para verificar la salud del sync wave 8 antes de operar.
`)),
	), cli.status)

	srv.AddTool(mcp.NewTool("aria_kb_sync_project",
		mcp.WithDescription(strings.TrimSpace(`
Re-sincea TODO lo asociado a un project_id (PRDs + historias + cotizaciones +
project README + root index). Útil después de cambios masivos o cuando un
batch falló parcialmente. Requiere role admin.
`)),
		mcp.WithString("project_id", mcp.Required(), mcp.Description("UUID del proyecto a re-sincear")),
	), cli.syncProject)

	srv.AddTool(mcp.NewTool("aria_kb_sync_quote",
		mcp.WithDescription(strings.TrimSpace(`
Fuerza el re-sync de una cotización al repo central (markdown + metadata +
DOCX iTechDev). Útil cuando se editó el quote sin pasar por el chat
orchestrator que normalmente trigerea el sync automáticamente al finalizar.
`)),
		mcp.WithString("quote_id", mcp.Required(), mcp.Description("UUID de la cotización")),
	), cli.syncQuote)

	srv.AddTool(mcp.NewTool("aria_quote_export_docx",
		mcp.WithDescription(strings.TrimSpace(`
Genera el DOCX iTechDev de una cotización SIN commitearla al repo central y
retorna los bytes codificados en base64. Útil cuando Claude quiere ofrecer
el archivo al usuario directamente para download (vía mensaje con adjunto)
sin pasar por el flujo de sync.
`)),
		mcp.WithString("quote_id", mcp.Required(), mcp.Description("UUID de la cotización")),
	), cli.exportDOCX)
}

type kbClient struct {
	cfg  KnowledgeBaseMCPConfig
	http *http.Client
}

func (c *kbClient) status(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	resp, status, err := c.do(ctx, http.MethodGet, "/v1/knowledge-base/status", nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("kb status: %d %s", status, string(resp))), nil
	}
	return mcp.NewToolResultText(string(resp)), nil
}

func (c *kbClient) syncProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pid := strings.TrimSpace(optString(req, "project_id"))
	if pid == "" {
		return mcp.NewToolResultError("project_id is required"), nil
	}
	resp, status, err := c.do(ctx, http.MethodPost, "/v1/knowledge-base/sync/"+pid, nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("kb sync project: %d %s", status, string(resp))), nil
	}
	return mcp.NewToolResultText(string(resp)), nil
}

func (c *kbClient) syncQuote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	qid := strings.TrimSpace(optString(req, "quote_id"))
	if qid == "" {
		return mcp.NewToolResultError("quote_id is required"), nil
	}
	resp, status, err := c.do(ctx, http.MethodPost, "/v1/cotizador/quotes/"+qid+"/sync-to-kb", nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("kb sync quote: %d %s", status, string(resp))), nil
	}
	return mcp.NewToolResultText(string(resp)), nil
}

func (c *kbClient) exportDOCX(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	qid := strings.TrimSpace(optString(req, "quote_id"))
	if qid == "" {
		return mcp.NewToolResultError("quote_id is required"), nil
	}
	body, status, err := c.doRaw(ctx, http.MethodGet, "/v1/cotizador/quotes/"+qid+"/export/docx", nil)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("export docx: %d %s", status, string(body))), nil
	}
	encoded := base64.StdEncoding.EncodeToString(body)
	out := map[string]any{
		"quote_id":          qid,
		"size_bytes":        len(body),
		"content_type":      "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"docx_base64":       encoded,
		"filename_suggested": fmt.Sprintf("propuesta-%s.docx", qid),
	}
	enc, _ := json.Marshal(out)
	return mcp.NewToolResultText(string(enc)), nil
}

func (c *kbClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	return c.doRaw(ctx, method, path, body)
}

func (c *kbClient) doRaw(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr *strings.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = strings.NewReader(string(buf))
	}
	url := strings.TrimRight(c.cfg.ServerURL, "/") + path
	var req *http.Request
	var err error
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
