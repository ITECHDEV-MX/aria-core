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
	"net/url"
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

	// === RFPs ===
	srv.AddTool(mcp.NewTool("cotizador_create_rfp",
		mcp.WithDescription("Crea un RFP asociado a un lead. analysis_json puede venir vacío y actualizarse después con cotizador_save_rfp_analysis."),
		mcp.WithString("lead_id", mcp.Required(), mcp.Description("UUID del lead")),
		mcp.WithString("source_type", mcp.Description("text|pdf|url (default: text)")),
		mcp.WithString("source_content", mcp.Required(), mcp.Description("Contenido del RFP (texto del cliente)")),
		mcp.WithString("analysis_json", mcp.Description("JSON estructurado del análisis del RFP (default: {})")),
	), cli.createRFP)

	srv.AddTool(mcp.NewTool("cotizador_list_rfps",
		mcp.WithDescription("Lista los RFPs de un lead."),
		mcp.WithString("lead_id", mcp.Required(), mcp.Description("UUID del lead")),
	), cli.listRFPs)

	srv.AddTool(mcp.NewTool("cotizador_get_rfp",
		mcp.WithDescription("Detalle de un RFP por ID (incluye source_content y analysis_json)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID del RFP")),
	), cli.getRFP)

	srv.AddTool(mcp.NewTool("cotizador_save_rfp_analysis",
		mcp.WithDescription("Actualiza el analysis_json de un RFP. Pensado para que el agente lea el RFP, lo razone, y guarde el análisis estructurado."),
		mcp.WithString("rfp_id", mcp.Required(), mcp.Description("UUID del RFP")),
		mcp.WithString("analysis_json", mcp.Required(), mcp.Description("JSON object con el análisis estructurado")),
	), cli.saveRFPAnalysis)

	// === Quotes ===
	srv.AddTool(mcp.NewTool("cotizador_create_quote",
		mcp.WithDescription("Crea una cotización (status=draft) con N items. Calcula totales automáticamente."),
		mcp.WithString("lead_id", mcp.Required(), mcp.Description("UUID del lead")),
		mcp.WithString("rfp_id", mcp.Description("UUID del RFP origen (opcional)")),
		mcp.WithString("currency", mcp.Description("Moneda ISO (default MXN)")),
		mcp.WithString("valid_until", mcp.Description("Fecha de vigencia YYYY-MM-DD")),
		mcp.WithString("terms", mcp.Description("Términos comerciales (forma de pago, vigencia, etc)")),
		mcp.WithString("justification", mcp.Description("Por qué este precio, qué incluye, supuestos")),
		mcp.WithString("items_json", mcp.Required(), mcp.Description(`JSON array de items: [{"sku":"","description":"","qty":1,"unit_price":1000}, ...]`)),
	), cli.createQuote)

	srv.AddTool(mcp.NewTool("cotizador_list_quotes",
		mcp.WithDescription("Lista las cotizaciones de un lead (todas las versiones)."),
		mcp.WithString("lead_id", mcp.Required(), mcp.Description("UUID del lead")),
	), cli.listQuotes)

	srv.AddTool(mcp.NewTool("cotizador_get_quote",
		mcp.WithDescription("Detalle de una cotización + items (incluye totales calculados)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID de la quote")),
	), cli.getQuote)

	srv.AddTool(mcp.NewTool("cotizador_update_quote_status",
		mcp.WithDescription("Cambia el estado de una cotización: draft|sent|in_review|approved|rejected|expired. Para cierre con outcome+lesson usá cotizador_close_quote."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID de la quote")),
		mcp.WithString("status", mcp.Required(), mcp.Description("Nuevo status")),
		mcp.WithString("notes", mcp.Description("Notas opcionales")),
	), cli.updateQuoteStatus)

	// === Memoria histórica (commit 5) ===
	srv.AddTool(mcp.NewTool("cotizador_close_quote",
		mcp.WithDescription("Cierra una cotización con outcome registrado y lesson aprendida (recomendado). Status terminal: approved|rejected|expired."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID de la quote")),
		mcp.WithString("status", mcp.Required(), mcp.Description("approved|rejected|expired")),
		mcp.WithString("reason", mcp.Description("Razón del outcome (precio alto, ganada por relación, etc)")),
		mcp.WithString("lesson_text", mcp.Description("Lección aprendida — recomendado guardar para alimentar memoria histórica")),
		mcp.WithString("lesson_tags", mcp.Description("Tags separados por coma (ej: 'pricing,alcance')")),
	), cli.closeQuote)

	srv.AddTool(mcp.NewTool("cotizador_search_similar_items",
		mcp.WithDescription("Busca items históricos cotizados antes que coincidan con la query (FTS sobre descripción). Útil para alimentar nuevas cotizaciones con precios y descripciones probadas."),
		mcp.WithString("query", mcp.Required(), mcp.Description("Texto a buscar (ej: 'desarrollo inventarios', 'integración SAP')")),
		mcp.WithString("limit", mcp.Description("Máximo de resultados (default 10)")),
	), cli.searchSimilarItems)

	srv.AddTool(mcp.NewTool("cotizador_get_outcome_stats",
		mcp.WithDescription("Estadísticas globales: win rate, total cotizado, ganado, perdido, monto promedio won/lost."),
	), cli.getOutcomeStats)

	srv.AddTool(mcp.NewTool("cotizador_get_client_history",
		mcp.WithDescription("Historial de cotizaciones de un cliente. Busca por nombre o empresa (LIKE). Devuelve quote_count, won_count, total_sold, last_quote_at."),
		mcp.WithString("q", mcp.Required(), mcp.Description("Nombre o empresa del cliente")),
	), cli.getClientHistory)

	srv.AddTool(mcp.NewTool("cotizador_get_lessons_learned",
		mcp.WithDescription("Busca lecciones aprendidas históricas (FTS sobre texto + filtro opcional por tag). Útil antes de redactar una nueva propuesta."),
		mcp.WithString("q", mcp.Description("Query FTS (vacío = listar todas las recientes)")),
		mcp.WithString("tag", mcp.Description("Filtrar por tag exacto (ej: 'pricing', 'alcance')")),
		mcp.WithString("limit", mcp.Description("Máximo de resultados (default 20)")),
	), cli.getLessonsLearned)

	srv.AddTool(mcp.NewTool("cotizador_save_lesson",
		mcp.WithDescription("Guarda una lección aprendida. Puede asociarse a una quote_id o lead_id. Tags ayudan a categorizar."),
		mcp.WithString("text", mcp.Required(), mcp.Description("Lección aprendida")),
		mcp.WithString("quote_id", mcp.Description("UUID de quote opcional")),
		mcp.WithString("lead_id", mcp.Description("UUID de lead opcional")),
		mcp.WithString("tags", mcp.Description("Tags separados por coma (ej: 'pricing,implementacion')")),
	), cli.saveLesson)

	// === Clients (commit 6) ===
	srv.AddTool(mcp.NewTool("cotizador_promote_lead_to_client",
		mcp.WithDescription("Convierte un lead won en cliente formal con datos fiscales. Crea entry en cotizador_clients y vincula bidireccionalmente."),
		mcp.WithString("lead_id", mcp.Required(), mcp.Description("UUID del lead a promover")),
		mcp.WithString("legal_name", mcp.Required(), mcp.Description("Razón social formal")),
		mcp.WithString("rfc", mcp.Description("RFC fiscal")),
		mcp.WithString("fiscal_address", mcp.Description("Dirección fiscal")),
		mcp.WithString("billing_email", mcp.Description("Email para facturación")),
		mcp.WithString("contacts_json", mcp.Description(`JSON array de contactos: [{"name":"","role":"","email":"","phone":""}, ...]`)),
		mcp.WithString("notes", mcp.Description("Notas adicionales del cliente")),
	), cli.promoteLead)

	srv.AddTool(mcp.NewTool("cotizador_list_clients",
		mcp.WithDescription("Lista los clientes formales (post-aprobación con datos fiscales)."),
	), cli.listClients)

	srv.AddTool(mcp.NewTool("cotizador_get_client",
		mcp.WithDescription("Detalle de un cliente formal por ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("UUID del cliente")),
	), cli.getClient)

	// === Templates (commit 9) ===
	srv.AddTool(mcp.NewTool("cotizador_list_templates",
		mcp.WithDescription("Lista las plantillas pre-fabricadas iTechDev disponibles para aplicar a una quote."),
	), cli.listTemplates)

	srv.AddTool(mcp.NewTool("cotizador_apply_template",
		mcp.WithDescription("Aplica una plantilla pre-fabricada a una quote. Upserts secciones markdown + completa header (proposal_type/product/tags) si no estaban seteados. Templates: itechdev_implementation_v1, itechdev_commercial_v1, itechdev_service_v1."),
		mcp.WithString("quote_id", mcp.Required(), mcp.Description("UUID de la quote")),
		mcp.WithString("template_key", mcp.Required(), mcp.Description("Key del template")),
	), cli.applyTemplate)
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

// === RFPs ===

func (c *cotizadorClient) createRFP(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	leadID, err := req.RequireString("lead_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	source, err := req.RequireString("source_content")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]any{
		"source_type":    optString(req, "source_type"),
		"source_content": source,
		"analysis_json":  optString(req, "analysis_json"),
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/leads/"+leadID+"/rfps", payload)
	return mcpResultFromHTTP("create rfp", body, code, err2)
}

func (c *cotizadorClient) listRFPs(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	leadID, err := req.RequireString("lead_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/leads/"+leadID+"/rfps", nil)
	return mcpResultFromHTTP("list rfps", body, code, err2)
}

func (c *cotizadorClient) getRFP(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/rfps/"+id, nil)
	return mcpResultFromHTTP("get rfp", body, code, err2)
}

func (c *cotizadorClient) saveRFPAnalysis(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("rfp_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	analysis, err := req.RequireString("analysis_json")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if !json.Valid([]byte(analysis)) {
		return mcp.NewToolResultError("analysis_json must be a valid JSON object"), nil
	}
	payload := map[string]json.RawMessage{
		"analysis_json": json.RawMessage(analysis),
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/rfps/"+id+"/analysis", payload)
	return mcpResultFromHTTP("save rfp analysis", body, code, err2)
}

// === Quotes ===

func (c *cotizadorClient) createQuote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	leadID, err := req.RequireString("lead_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	itemsJSON, err := req.RequireString("items_json")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	var items []map[string]any
	if err := json.Unmarshal([]byte(itemsJSON), &items); err != nil {
		return mcp.NewToolResultError("items_json must be a JSON array of item objects"), nil
	}
	payload := map[string]any{
		"rfp_id":        optString(req, "rfp_id"),
		"currency":      optString(req, "currency"),
		"valid_until":   optString(req, "valid_until"),
		"terms":         optString(req, "terms"),
		"justification": optString(req, "justification"),
		"items":         items,
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/leads/"+leadID+"/quotes", payload)
	return mcpResultFromHTTP("create quote", body, code, err2)
}

func (c *cotizadorClient) listQuotes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	leadID, err := req.RequireString("lead_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/leads/"+leadID+"/quotes", nil)
	return mcpResultFromHTTP("list quotes", body, code, err2)
}

func (c *cotizadorClient) getQuote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/quotes/"+id, nil)
	return mcpResultFromHTTP("get quote", body, code, err2)
}

func (c *cotizadorClient) updateQuoteStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status, err := req.RequireString("status")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]string{"status": status, "notes": optString(req, "notes")}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/quotes/"+id+"/status", payload)
	return mcpResultFromHTTP("update quote status", body, code, err2)
}

// === Memoria histórica ===

func (c *cotizadorClient) closeQuote(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	status, err := req.RequireString("status")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tagsStr := optString(req, "lesson_tags")
	var tags []string
	if tagsStr != "" {
		for _, t := range strings.Split(tagsStr, ",") {
			if v := strings.TrimSpace(t); v != "" {
				tags = append(tags, v)
			}
		}
	}
	payload := map[string]any{
		"status":      status,
		"reason":      optString(req, "reason"),
		"lesson_text": optString(req, "lesson_text"),
		"lesson_tags": tags,
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/quotes/"+id+"/close", payload)
	return mcpResultFromHTTP("close quote", body, code, err2)
}

func (c *cotizadorClient) searchSimilarItems(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := req.RequireString("query")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	path := "/v1/cotizador/memory/similar-items?q=" + url.QueryEscape(query)
	if l := optString(req, "limit"); l != "" {
		path += "&limit=" + url.QueryEscape(l)
	}
	body, code, err2 := c.do(ctx, http.MethodGet, path, nil)
	return mcpResultFromHTTP("search similar items", body, code, err2)
}

func (c *cotizadorClient) getOutcomeStats(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	body, code, err := c.do(ctx, http.MethodGet, "/v1/cotizador/memory/outcome-stats", nil)
	return mcpResultFromHTTP("outcome stats", body, code, err)
}

func (c *cotizadorClient) getClientHistory(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q, err := req.RequireString("q")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/memory/client-history?q="+url.QueryEscape(q), nil)
	return mcpResultFromHTTP("client history", body, code, err2)
}

func (c *cotizadorClient) getLessonsLearned(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := optString(req, "q")
	tag := optString(req, "tag")
	limit := optString(req, "limit")
	path := "/v1/cotizador/memory/lessons?"
	if q != "" {
		path += "q=" + url.QueryEscape(q) + "&"
	}
	if tag != "" {
		path += "tag=" + url.QueryEscape(tag) + "&"
	}
	if limit != "" {
		path += "limit=" + url.QueryEscape(limit) + "&"
	}
	body, code, err := c.do(ctx, http.MethodGet, strings.TrimRight(path, "&?"), nil)
	return mcpResultFromHTTP("lessons learned", body, code, err)
}

func (c *cotizadorClient) saveLesson(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	text, err := req.RequireString("text")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	tagsStr := optString(req, "tags")
	var tags []string
	if tagsStr != "" {
		for _, t := range strings.Split(tagsStr, ",") {
			if v := strings.TrimSpace(t); v != "" {
				tags = append(tags, v)
			}
		}
	}
	payload := map[string]any{
		"text":     text,
		"quote_id": optString(req, "quote_id"),
		"lead_id":  optString(req, "lead_id"),
		"tags":     tags,
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/memory/lessons", payload)
	return mcpResultFromHTTP("save lesson", body, code, err2)
}

// === Clients ===

func (c *cotizadorClient) promoteLead(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	leadID, err := req.RequireString("lead_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	legalName, err := req.RequireString("legal_name")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	contactsStr := optString(req, "contacts_json")
	var contacts json.RawMessage
	if contactsStr != "" {
		if !json.Valid([]byte(contactsStr)) {
			return mcp.NewToolResultError("contacts_json must be a valid JSON array"), nil
		}
		contacts = json.RawMessage(contactsStr)
	}
	payload := map[string]any{
		"legal_name":     legalName,
		"rfc":            optString(req, "rfc"),
		"fiscal_address": optString(req, "fiscal_address"),
		"billing_email":  optString(req, "billing_email"),
		"notes":          optString(req, "notes"),
	}
	if contacts != nil {
		payload["contacts"] = contacts
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/leads/"+leadID+"/promote", payload)
	return mcpResultFromHTTP("promote lead", body, code, err2)
}

func (c *cotizadorClient) listClients(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	body, code, err := c.do(ctx, http.MethodGet, "/v1/cotizador/clients", nil)
	return mcpResultFromHTTP("list clients", body, code, err)
}

func (c *cotizadorClient) getClient(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/cotizador/clients/"+id, nil)
	return mcpResultFromHTTP("get client", body, code, err2)
}

// === Templates ===

func (c *cotizadorClient) listTemplates(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	body, code, err := c.do(ctx, http.MethodGet, "/v1/cotizador/templates", nil)
	return mcpResultFromHTTP("list templates", body, code, err)
}

func (c *cotizadorClient) applyTemplate(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	quoteID, err := req.RequireString("quote_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	templateKey, err := req.RequireString("template_key")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]string{"template_key": templateKey}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/cotizador/quotes/"+quoteID+"/apply-template", payload)
	return mcpResultFromHTTP("apply template", body, code, err2)
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
