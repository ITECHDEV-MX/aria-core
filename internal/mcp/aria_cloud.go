// MCP profile aria cloud-bound. Reemplaza el legacy mcp__aria__* (SQLite local).
// Tools que cualquier agente Claude (Code, Desktop con plan Max) puede usar
// para guardar/buscar memoria persistente compartida en aria-core cloud.
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AriaCloudConfig configura el cliente HTTP para hablar con el cloud.
type AriaCloudConfig struct {
	ServerURL string
	Token     string // JWT user-bound (de session.json)
	Timeout   time.Duration
}

// RegisterAriaCloudTools registra las tools de memoria ARIA en el MCP server.
// Equivalentes 1:1 al legacy mcp__aria__* pero contra cloud REST.
func RegisterAriaCloudTools(srv *server.MCPServer, cfg AriaCloudConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	cli := &ariaClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("aria_save",
		mcp.WithDescription("Guarda una memoria/observación en ARIA Core. Si pasás topic_key, hace UPSERT por (project, topic_key) — la próxima vez con el mismo topic_key actualiza la misma memoria. reasoning_trace es JSON estructurado con conclusion/insight/rejected_alternatives."),
		mcp.WithString("title", mcp.Required(), mcp.Description("Título descriptivo")),
		mcp.WithString("content", mcp.Description("Contenido / narrativa")),
		mcp.WithString("project", mcp.Description("Proyecto al que pertenece")),
		mcp.WithString("scope", mcp.Description("personal | project | team | global | client_knowledge (default: personal)")),
		mcp.WithString("type", mcp.Description("Tipo: general | decision | meeting | commitment | tech_debt | risk | adr | architecture | bugfix | feature")),
		mcp.WithString("topic_key", mcp.Description("Clave estable para upsert (ej: 'auth-strategy', 'aria-core-deploy')")),
		mcp.WithString("session_id", mcp.Description("ID de la sesión activa (opcional)")),
		mcp.WithString("subtitle", mcp.Description("Subtítulo")),
		mcp.WithString("facts", mcp.Description("Hechos clave separados por |")),
		mcp.WithString("concepts", mcp.Description("Conceptos clave separados por |")),
		mcp.WithString("files_touched", mcp.Description("Archivos relevantes separados por |")),
		mcp.WithString("reasoning_trace", mcp.Description(`JSON: {"conclusion": "...", "insight": "...", "rejected_alternatives": [...]}`)),
		mcp.WithString("client_id", mcp.Description("UUID del cliente (para scope=client_knowledge)")),
	), cli.save)

	srv.AddTool(mcp.NewTool("aria_search",
		mcp.WithDescription("Busca memorias por FTS español accent-insensitive. Filtros opcionales por project/scope/type. Si pasás token_budget, ARIA selecciona el subset que cabe en N tokens (rerank canon-first|recent-first|effectiveness)."),
		mcp.WithString("query", mcp.Description("Texto a buscar (vacío = listar recientes)")),
		mcp.WithString("project", mcp.Description("Filtrar por proyecto")),
		mcp.WithString("scope", mcp.Description("Filtrar por scope")),
		mcp.WithString("type", mcp.Description("Filtrar por observation_type")),
		mcp.WithString("limit", mcp.Description("Máximo resultados (default 20)")),
		mcp.WithString("token_budget", mcp.Description("Budget en tokens para el subset retornado (default sin truncar)")),
		mcp.WithString("strategy", mcp.Description("Estrategia de rerank: canon-first|recent-first|effectiveness (default canon-first)")),
	), cli.search)

	srv.AddTool(mcp.NewTool("aria_get",
		mcp.WithDescription("Detalle de una observation por ID."),
		mcp.WithString("id", mcp.Required(), mcp.Description("ID de la observation (obs_...)")),
	), cli.get)

	srv.AddTool(mcp.NewTool("aria_timeline",
		mcp.WithDescription("Cronología de observations en ventana temporal por proyecto."),
		mcp.WithString("project", mcp.Description("Proyecto (opcional)")),
		mcp.WithString("since", mcp.Description("Desde fecha RFC3339")),
		mcp.WithString("until", mcp.Description("Hasta fecha RFC3339")),
		mcp.WithString("limit", mcp.Description("Máximo (default 50)")),
	), cli.timeline)

	srv.AddTool(mcp.NewTool("aria_promote_canon",
		mcp.WithDescription("Marca una observation como canónica (verdad curada). Solo memorias canónicas representan estado durable del proyecto/cliente."),
		mcp.WithString("id", mcp.Required(), mcp.Description("ID de la observation")),
	), cli.promoteCanon)

	srv.AddTool(mcp.NewTool("aria_record_quality",
		mcp.WithDescription("Registra señal de calidad sobre una observation (feedback loop)."),
		mcp.WithString("id", mcp.Required(), mcp.Description("ID de la observation")),
		mcp.WithString("signal", mcp.Required(), mcp.Description("Tipo de señal (e.g. helpful, outdated, drift)")),
		mcp.WithString("score", mcp.Description("Score 0.0-1.0")),
		mcp.WithString("notes", mcp.Description("Notas opcionales")),
	), cli.recordQuality)

	srv.AddTool(mcp.NewTool("aria_session_start",
		mcp.WithDescription("Inicia una sesión trackeable. Devuelve session_id + auto_context (markdown con canon+skills+recipes+open sessions). Usar el auto_context como primer mensaje del agente."),
		mcp.WithString("project", mcp.Description("Proyecto")),
		mcp.WithString("directory", mcp.Description("Directorio de trabajo")),
		mcp.WithString("goal", mcp.Description("Objetivo de la sesión")),
		mcp.WithString("client_id", mcp.Description("UUID cliente opcional")),
		mcp.WithString("machine_id", mcp.Description("ID de máquina (default: local)")),
		mcp.WithString("stack", mcp.Description("Stack separado por comas (typescript,react,go) — usado para sugerir skills")),
		mcp.WithString("token_budget", mcp.Description("Budget para auto_context (default 8000 tokens)")),
	), cli.sessionStart)

	srv.AddTool(mcp.NewTool("aria_session_summary",
		mcp.WithDescription("Cierra sesión + guarda resumen estructurado. Llamar al final de cada sesión para preservar contexto."),
		mcp.WithString("session_id", mcp.Required(), mcp.Description("ID de la sesión a cerrar")),
		mcp.WithString("request", mcp.Description("Qué pidió el developer")),
		mcp.WithString("investigated", mcp.Description("Qué se exploró")),
		mcp.WithString("learned", mcp.Description("Qué se aprendió")),
		mcp.WithString("completed", mcp.Description("Qué se completó")),
		mcp.WithString("next_steps", mcp.Description("Qué queda pendiente")),
		mcp.WithString("files_read", mcp.Description("Archivos leídos | separados")),
		mcp.WithString("files_edited", mcp.Description("Archivos editados | separados")),
		mcp.WithString("notes", mcp.Description("Notas adicionales")),
		mcp.WithString("quality_grade", mcp.Description("Grade A-F autoeval")),
	), cli.sessionSummary)

	srv.AddTool(mcp.NewTool("aria_get_context_status",
		mcp.WithDescription("Auto-orientación al inicio de sesión. Retorna estado del contexto: sesión activa, count observations, skills cargados."),
		mcp.WithString("project", mcp.Description("Proyecto actual")),
	), cli.contextStatus)

	srv.AddTool(mcp.NewTool("aria_get_skills",
		mcp.WithDescription("Skills/standards del equipo para el stack detectado. Si pasás task_description, ARIA rankea por effectiveness (Wilson lower bound) + FTS sobre el goal."),
		mcp.WithString("stack", mcp.Description("Stack separado por comas (ej: typescript,react,go)")),
		mcp.WithString("task_description", mcp.Description("Descripción del goal actual — habilita rerank por effectiveness")),
		mcp.WithString("token_budget", mcp.Description("Budget en tokens para el subset retornado")),
		mcp.WithString("limit", mcp.Description("Máximo skills (default 10)")),
		mcp.WithString("strategy", mcp.Description("canon-first|recent-first|effectiveness")),
		mcp.WithString("session_id", mcp.Description("Session activa — para tracking de retrieval")),
		mcp.WithString("project", mcp.Description("Proyecto actual")),
	), cli.getSkills)

	srv.AddTool(mcp.NewTool("aria_record_skill_feedback",
		mcp.WithDescription("Registra feedback sobre un skill recientemente retornado. Permite que ARIA aprenda qué skills funcionan para qué tareas."),
		mcp.WithString("skill_id", mcp.Required(), mcp.Description("ID del skill")),
		mcp.WithString("signal", mcp.Required(), mcp.Description("commit_referenced | manual_thumbs_up | manual_thumbs_down | used_in_recipe | unused")),
		mcp.WithString("helped", mcp.Description("'true' o 'false' — sobreescribe la inferencia del signal")),
		mcp.WithString("notes", mcp.Description("Notas opcionales")),
	), cli.recordSkillFeedback)

	srv.AddTool(mcp.NewTool("aria_get_recipes",
		mcp.WithDescription("Workflow patterns capturados de sesiones exitosas. Filtra por descripción de tarea + stack."),
		mcp.WithString("task_description", mcp.Required(), mcp.Description("Descripción de la tarea actual")),
		mcp.WithString("stack", mcp.Description("Stack separado por comas")),
		mcp.WithString("limit", mcp.Description("Máximo (default 5)")),
	), cli.getRecipes)
}

type ariaClient struct {
	cfg  AriaCloudConfig
	http *http.Client
}

func (c *ariaClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
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

func (c *ariaClient) save(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	title, err := req.RequireString("title")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]any{
		"title":             title,
		"content":           optString(req, "content"),
		"project":           optString(req, "project"),
		"scope":             optString(req, "scope"),
		"type":              optString(req, "type"),
		"topic_key":         optString(req, "topic_key"),
		"session_id":        optString(req, "session_id"),
		"subtitle":          optString(req, "subtitle"),
		"facts":             optString(req, "facts"),
		"concepts":          optString(req, "concepts"),
		"files_touched":     optString(req, "files_touched"),
		"client_id":         optString(req, "client_id"),
	}
	if rt := optString(req, "reasoning_trace"); rt != "" {
		payload["reasoning_trace"] = json.RawMessage(rt)
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/memory/save", payload)
	return mcpResultFromHTTP("aria_save", body, code, err2)
}

func (c *ariaClient) search(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := optString(req, "query")
	project := optString(req, "project")
	scope := optString(req, "scope")
	t := optString(req, "type")
	limit := optString(req, "limit")
	tokenBudget := optString(req, "token_budget")
	strategy := optString(req, "strategy")
	path := "/v1/memory/search?"
	if q != "" {
		path += "q=" + url.QueryEscape(q) + "&"
	}
	if project != "" {
		path += "project=" + url.QueryEscape(project) + "&"
	}
	if scope != "" {
		path += "scope=" + url.QueryEscape(scope) + "&"
	}
	if t != "" {
		path += "type=" + url.QueryEscape(t) + "&"
	}
	if limit != "" {
		path += "limit=" + url.QueryEscape(limit) + "&"
	}
	if tokenBudget != "" {
		path += "token_budget=" + url.QueryEscape(tokenBudget) + "&"
	}
	if strategy != "" {
		path += "strategy=" + url.QueryEscape(strategy) + "&"
	}
	body, code, err := c.do(ctx, http.MethodGet, strings.TrimRight(path, "&?"), nil)
	return mcpResultFromHTTP("aria_search", body, code, err)
}

func (c *ariaClient) get(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodGet, "/v1/memory/observations/"+id, nil)
	return mcpResultFromHTTP("aria_get", body, code, err2)
}

func (c *ariaClient) timeline(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project := optString(req, "project")
	since := optString(req, "since")
	until := optString(req, "until")
	limit := optString(req, "limit")
	path := "/v1/memory/timeline?"
	if project != "" {
		path += "project=" + url.QueryEscape(project) + "&"
	}
	if since != "" {
		path += "since=" + url.QueryEscape(since) + "&"
	}
	if until != "" {
		path += "until=" + url.QueryEscape(until) + "&"
	}
	if limit != "" {
		path += "limit=" + url.QueryEscape(limit) + "&"
	}
	body, code, err := c.do(ctx, http.MethodGet, strings.TrimRight(path, "&?"), nil)
	return mcpResultFromHTTP("aria_timeline", body, code, err)
}

func (c *ariaClient) promoteCanon(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/memory/observations/"+id+"/promote-canon", map[string]any{})
	return mcpResultFromHTTP("aria_promote_canon", body, code, err2)
}

func (c *ariaClient) recordQuality(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	signal, err := req.RequireString("signal")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	score := 0.0
	if v := optString(req, "score"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			score = f
		}
	}
	payload := map[string]any{
		"signal": signal, "score": score, "notes": optString(req, "notes"),
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/memory/observations/"+id+"/quality", payload)
	return mcpResultFromHTTP("aria_record_quality", body, code, err2)
}

func (c *ariaClient) sessionStart(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	payload := map[string]any{
		"project":    optString(req, "project"),
		"directory":  optString(req, "directory"),
		"goal":       optString(req, "goal"),
		"client_id":  optString(req, "client_id"),
		"machine_id": optString(req, "machine_id"),
	}
	if stack := optString(req, "stack"); stack != "" {
		parts := []string{}
		for _, t := range strings.Split(stack, ",") {
			if tt := strings.TrimSpace(t); tt != "" {
				parts = append(parts, tt)
			}
		}
		payload["stack"] = parts
	}
	if v := optString(req, "token_budget"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			payload["token_budget"] = n
		}
	}
	body, code, err := c.do(ctx, http.MethodPost, "/v1/memory/sessions/start", payload)
	return mcpResultFromHTTP("aria_session_start", body, code, err)
}

func (c *ariaClient) sessionSummary(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	id, err := req.RequireString("session_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]any{
		"request":       optString(req, "request"),
		"investigated":  optString(req, "investigated"),
		"learned":       optString(req, "learned"),
		"completed":     optString(req, "completed"),
		"next_steps":    optString(req, "next_steps"),
		"files_read":    optString(req, "files_read"),
		"files_edited": optString(req, "files_edited"),
		"notes":         optString(req, "notes"),
		"quality_grade": optString(req, "quality_grade"),
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/memory/sessions/"+id+"/summary", payload)
	return mcpResultFromHTTP("aria_session_summary", body, code, err2)
}

func (c *ariaClient) contextStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	project := optString(req, "project")
	path := "/v1/memory/context-status"
	if project != "" {
		path += "?project=" + url.QueryEscape(project)
	}
	body, code, err := c.do(ctx, http.MethodGet, path, nil)
	return mcpResultFromHTTP("aria_get_context_status", body, code, err)
}

func (c *ariaClient) getSkills(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	stack := optString(req, "stack")
	taskDesc := optString(req, "task_description")
	tokenBudget := optString(req, "token_budget")
	limit := optString(req, "limit")
	strategy := optString(req, "strategy")
	sessionID := optString(req, "session_id")
	project := optString(req, "project")
	path := "/v1/memory/skills?"
	if stack != "" {
		path += "stack=" + url.QueryEscape(stack) + "&"
	}
	if taskDesc != "" {
		path += "task_description=" + url.QueryEscape(taskDesc) + "&"
	}
	if tokenBudget != "" {
		path += "token_budget=" + url.QueryEscape(tokenBudget) + "&"
	}
	if limit != "" {
		path += "limit=" + url.QueryEscape(limit) + "&"
	}
	if strategy != "" {
		path += "strategy=" + url.QueryEscape(strategy) + "&"
	}
	if sessionID != "" {
		path += "session_id=" + url.QueryEscape(sessionID) + "&"
	}
	if project != "" {
		path += "project=" + url.QueryEscape(project) + "&"
	}
	body, code, err := c.do(ctx, http.MethodGet, strings.TrimRight(path, "&?"), nil)
	return mcpResultFromHTTP("aria_get_skills", body, code, err)
}

func (c *ariaClient) recordSkillFeedback(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	skillID, err := req.RequireString("skill_id")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	signal, err := req.RequireString("signal")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	payload := map[string]any{
		"skill_id": skillID,
		"signal":   signal,
		"notes":    optString(req, "notes"),
	}
	if helped := optString(req, "helped"); helped != "" {
		switch strings.ToLower(strings.TrimSpace(helped)) {
		case "true", "1", "yes":
			b := true
			payload["helped"] = b
		case "false", "0", "no":
			b := false
			payload["helped"] = b
		}
	}
	body, code, err2 := c.do(ctx, http.MethodPost, "/v1/memory/skills/feedback", payload)
	return mcpResultFromHTTP("aria_record_skill_feedback", body, code, err2)
}

func (c *ariaClient) getRecipes(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	task, err := req.RequireString("task_description")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	stack := optString(req, "stack")
	limit := optString(req, "limit")
	path := "/v1/memory/recipes?task=" + url.QueryEscape(task)
	if stack != "" {
		path += "&stack=" + url.QueryEscape(stack)
	}
	if limit != "" {
		path += "&limit=" + url.QueryEscape(limit)
	}
	body, code, err2 := c.do(ctx, http.MethodGet, path, nil)
	return mcpResultFromHTTP("aria_get_recipes", body, code, err2)
}

func init() {
	// Suprimir unused imports warning si hay
	_ = fmt.Sprintf
}
