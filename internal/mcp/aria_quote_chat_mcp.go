package mcp

import (
	"context"
	"net/http"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// RegisterQuoteChatTools registers the quote-chat MCP tools on the existing
// cotizadorClient. These wrap the /v1/cotizador/chat/* REST endpoints.
//
// New tools:
//   - cotizador_chat_create
//   - cotizador_chat_get
//   - cotizador_chat_send
//   - cotizador_chat_finalize
//   - cotizador_chat_send_email
func RegisterQuoteChatTools(srv *server.MCPServer, cfg CotizadorCloudConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 60 * 1e9 // 60s
	}
	cli := &cotizadorClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("cotizador_chat_create",
		mcp.WithDescription("Crea una nueva sesión de quote-chat. Devuelve session_id."),
		mcp.WithString("lead_id", mcp.Required(), mcp.Description("UUID del lead")),
		mcp.WithString("template_key", mcp.Description("Key del template (default: itechdev_implementation_v1)")),
		mcp.WithString("title", mcp.Description("Título descriptivo de la cotización")),
		mcp.WithString("rfp_id", mcp.Description("UUID del RFP origen (opcional)")),
	), cli.chatCreate)

	srv.AddTool(mcp.NewTool("cotizador_chat_get",
		mcp.WithDescription("Detalle de una sesión de quote-chat: mensajes + secciones de la quote."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("UUID de la sesión")),
	), cli.chatGet)

	srv.AddTool(mcp.NewTool("cotizador_chat_send",
		mcp.WithDescription("Envía un mensaje a la sesión. ARIA scrub PII antes de mandar a Claude, expand al volver. Devuelve user message + assistant response."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("UUID de la sesión")),
		mcp.WithString("message", mcp.Required(), mcp.Description("Mensaje del usuario en markdown")),
		mcp.WithString("sensitivity", mcp.Description("public|internal|client|confidential (default: client)")),
	), cli.chatSend)

	srv.AddTool(mcp.NewTool("cotizador_chat_finalize",
		mcp.WithDescription("Finaliza la sesión: crea la quote borrador y persiste todas las secciones generadas. Devuelve quote_id."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("UUID de la sesión")),
	), cli.chatFinalize)

	srv.AddTool(mcp.NewTool("cotizador_chat_send_email",
		mcp.WithDescription("Envía manualmente el email al cliente con la cotización. CRÍTICO: este envío NO tiene preview en MCP — se asume que el dev revisó el body antes de invocar. Para preview usar el dashboard."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("UUID de la sesión")),
		mcp.WithString("quote_id", mcp.Required(), mcp.Description("UUID de la quote asociada")),
		mcp.WithString("to", mcp.Required(), mcp.Description("Email destinatario")),
		mcp.WithString("cc", mcp.Description("CC (cma-separated)")),
		mcp.WithString("subject", mcp.Required(), mcp.Description("Asunto del email")),
		mcp.WithString("body_html", mcp.Required(), mcp.Description("Body HTML completo del email")),
	), cli.chatSendEmail)
}

func (c *cotizadorClient) chatCreate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	leadID, err := req.RequireString("lead_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]string{
		"lead_id":      leadID,
		"template_key": optString(req, "template_key"),
		"title":        optString(req, "title"),
		"rfp_id":       optString(req, "rfp_id"),
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/chat", payload)
	return mcpResultFromHTTP("chat create", body, code, err2)
}

func (c *cotizadorClient) chatGet(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/chat/"+id, nil)
	return mcpResultFromHTTP("chat get", body, code, err2)
}

func (c *cotizadorClient) chatSend(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	msg, err := req.RequireString("message")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]any{
		"message":     msg,
		"sensitivity": strings.TrimSpace(optString(req, "sensitivity")),
		"timeout_sec": 120,
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/chat/"+id+"/send", payload)
	return mcpResultFromHTTP("chat send", body, code, err2)
}

func (c *cotizadorClient) chatFinalize(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/chat/"+id+"/finalize", map[string]string{})
	return mcpResultFromHTTP("chat finalize", body, code, err2)
}

func (c *cotizadorClient) chatSendEmail(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	quoteID, err := req.RequireString("quote_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	to, err := req.RequireString("to")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	subject, err := req.RequireString("subject")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, err := req.RequireString("body_html")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]string{
		"quote_id":  quoteID,
		"to":        to,
		"cc":        optString(req, "cc"),
		"subject":   subject,
		"body_html": body,
	}
	out, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/chat/"+id+"/email-send", payload)
	return mcpResultFromHTTP("chat send email", out, code, err2)
}
