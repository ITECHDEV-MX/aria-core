// MCP tools del módulo Team Projects (wave 7).
//
// Expone los siguientes tools como thin shim sobre /v1/team-projects + /v1/tasks
// del cloudserver. Usa el JWT user-bound de session.json (mismo patrón que
// aria_pages_mcp / aria_recipe_mcp).
package mcp

import (
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

// TeamProjectsMCPConfig configura el cliente HTTP para los tools.
type TeamProjectsMCPConfig struct {
	ServerURL string
	Token     string
	Timeout   time.Duration
}

// RegisterAriaTeamProjectsTools registra los tools wave 7.
//
//	aria_project_list / aria_project_create / aria_project_add_member
//	aria_task_list / aria_task_create / aria_task_assign
//	aria_task_update_status / aria_task_link_observation / aria_task_close
func RegisterAriaTeamProjectsTools(srv *server.MCPServer, cfg TeamProjectsMCPConfig) {
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	cli := &teamProjectsClient{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}

	srv.AddTool(mcp.NewTool("aria_project_list",
		mcp.WithDescription("Lista proyectos del equipo iTechDev visibles al user. Filter opcional por status."),
		mcp.WithString("status", mcp.Description("active | paused | archived | all (default: active)")),
	), cli.listProjects)

	srv.AddTool(mcp.NewTool("aria_project_create",
		mcp.WithDescription(strings.TrimSpace(`
Crea un proyecto interno del equipo. Si GitHub está configurado, auto-crea repo
ITECHDEV-MX/<slug> privado con README, .gitignore y MIT license. Si NO está
configurado (GITHUB_API_TOKEN ausente del vault), el proyecto se crea sin repo
y retorna warning.
`)),
		mcp.WithString("name", mcp.Required(), mcp.Description("Nombre del proyecto")),
		mcp.WithString("slug", mcp.Description("Slug opcional (auto-derivado del name)")),
		mcp.WithString("description", mcp.Description("Descripción markdown")),
		mcp.WithString("client_id", mcp.Description("UUID del cliente, opcional")),
		mcp.WithBoolean("no_github", mcp.Description("Si true, NO intenta crear el repo")),
	), cli.createProject)

	srv.AddTool(mcp.NewTool("aria_project_add_member",
		mcp.WithDescription("Agrega un usuario al proyecto con role (owner|lead|member|viewer)."),
		mcp.WithString("project_id", mcp.Required()),
		mcp.WithString("user_uid", mcp.Required()),
		mcp.WithString("role", mcp.Description("owner|lead|member|viewer (default: member)")),
	), cli.addMember)

	srv.AddTool(mcp.NewTool("aria_task_list",
		mcp.WithDescription("Lista tasks. Si project_id presente, scope al proyecto. Si assignee=me, lista las del user actual. Filter por status."),
		mcp.WithString("project_id", mcp.Description("UUID del proyecto, opcional")),
		mcp.WithBoolean("assigned_to_me", mcp.Description("Si true, lista solo tasks asignadas al caller")),
		mcp.WithString("status", mcp.Description("todo|in_progress|review|done|cancelled|all")),
	), cli.listTasks)

	srv.AddTool(mcp.NewTool("aria_task_create",
		mcp.WithDescription("Crea una task en el Kanban del proyecto. Optional: priority, labels, asignados (UIDs)."),
		mcp.WithString("project_id", mcp.Required()),
		mcp.WithString("title", mcp.Required()),
		mcp.WithString("description", mcp.Description("Descripción markdown")),
		mcp.WithString("priority", mcp.Description("low|medium|high|urgent")),
	), cli.createTask)

	srv.AddTool(mcp.NewTool("aria_task_assign",
		mcp.WithDescription("Asigna un user a la task."),
		mcp.WithString("task_id", mcp.Required()),
		mcp.WithString("user_uid", mcp.Required()),
	), cli.assignTask)

	srv.AddTool(mcp.NewTool("aria_task_update_status",
		mcp.WithDescription("Cambia el status de la task. Estados: todo|in_progress|review|done|cancelled."),
		mcp.WithString("task_id", mcp.Required()),
		mcp.WithString("status", mcp.Required()),
	), cli.updateStatus)

	srv.AddTool(mcp.NewTool("aria_task_link_observation",
		mcp.WithDescription("Linkea una observación de memoria como knowledge de la task."),
		mcp.WithString("task_id", mcp.Required()),
		mcp.WithString("observation_id", mcp.Required()),
		mcp.WithString("link_type", mcp.Description("work|prompt|outcome|reference (default: work)")),
	), cli.linkObservation)

	srv.AddTool(mcp.NewTool("aria_task_close",
		mcp.WithDescription("Cierra la task y dispara captura automática de knowledge: observaciones+sesiones+commits del rango temporal."),
		mcp.WithString("task_id", mcp.Required()),
	), cli.closeTask)
}

type teamProjectsClient struct {
	cfg  TeamProjectsMCPConfig
	http *http.Client
}

func (c *teamProjectsClient) listProjects(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	q := url.Values{}
	if v := optString(req, "status"); v != "" {
		q.Set("status", v)
	}
	path := "/v1/team-projects"
	if e := q.Encode(); e != "" {
		path += "?" + e
	}
	body, status, err := c.do(ctx, http.MethodGet, path, nil)
	return tpResult(body, status, err, "list projects")
}

func (c *teamProjectsClient) createProject(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pl := map[string]any{
		"name":        optString(req, "name"),
		"slug":        optString(req, "slug"),
		"description": optString(req, "description"),
		"client_id":   optString(req, "client_id"),
		"no_github":   boolFromReq(req, "no_github"),
	}
	if strings.TrimSpace(pl["name"].(string)) == "" {
		return mcp.NewToolResultError("name required"), nil
	}
	body, status, err := c.do(ctx, http.MethodPost, "/v1/team-projects", pl)
	return tpResult(body, status, err, "create project")
}

func (c *teamProjectsClient) addMember(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pid := strings.TrimSpace(optString(req, "project_id"))
	if pid == "" {
		return mcp.NewToolResultError("project_id required"), nil
	}
	pl := map[string]any{
		"user_uid": optString(req, "user_uid"),
		"role":     optString(req, "role"),
	}
	body, status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/team-projects/%s/members", url.PathEscape(pid)), pl)
	return tpResult(body, status, err, "add member")
}

func (c *teamProjectsClient) listTasks(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if boolFromReq(req, "assigned_to_me") {
		body, status, err := c.do(ctx, http.MethodGet, "/v1/tasks/assigned-to-me?status="+url.QueryEscape(optString(req, "status")), nil)
		return tpResult(body, status, err, "list assigned tasks")
	}
	pid := strings.TrimSpace(optString(req, "project_id"))
	if pid == "" {
		return mcp.NewToolResultError("project_id required (or assigned_to_me=true)"), nil
	}
	q := url.Values{}
	if v := optString(req, "status"); v != "" {
		q.Set("status", v)
	}
	body, status, err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/team-projects/%s/tasks?%s", url.PathEscape(pid), q.Encode()), nil)
	return tpResult(body, status, err, "list tasks")
}

func (c *teamProjectsClient) createTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	pid := strings.TrimSpace(optString(req, "project_id"))
	if pid == "" {
		return mcp.NewToolResultError("project_id required"), nil
	}
	pl := map[string]any{
		"title":       optString(req, "title"),
		"description": optString(req, "description"),
		"priority":    optString(req, "priority"),
	}
	body, status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/team-projects/%s/tasks", url.PathEscape(pid)), pl)
	return tpResult(body, status, err, "create task")
}

func (c *teamProjectsClient) assignTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tid := strings.TrimSpace(optString(req, "task_id"))
	if tid == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	pl := map[string]any{"user_uid": optString(req, "user_uid")}
	body, status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/tasks/%s/assign", url.PathEscape(tid)), pl)
	return tpResult(body, status, err, "assign task")
}

func (c *teamProjectsClient) updateStatus(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tid := strings.TrimSpace(optString(req, "task_id"))
	if tid == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	pl := map[string]any{"status": optString(req, "status")}
	body, status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/tasks/%s/status", url.PathEscape(tid)), pl)
	return tpResult(body, status, err, "update task status")
}

func (c *teamProjectsClient) linkObservation(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tid := strings.TrimSpace(optString(req, "task_id"))
	if tid == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	pl := map[string]any{
		"observation_id": optString(req, "observation_id"),
		"link_type":      optString(req, "link_type"),
	}
	body, status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/tasks/%s/link-observation", url.PathEscape(tid)), pl)
	return tpResult(body, status, err, "link observation")
}

func (c *teamProjectsClient) closeTask(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	tid := strings.TrimSpace(optString(req, "task_id"))
	if tid == "" {
		return mcp.NewToolResultError("task_id required"), nil
	}
	body, status, err := c.do(ctx, http.MethodPost, fmt.Sprintf("/v1/tasks/%s/close", url.PathEscape(tid)), nil)
	return tpResult(body, status, err, "close task")
}

// ─── HTTP helpers ───────────────────────────────────────────────────────

func (c *teamProjectsClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
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
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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

func tpResult(body []byte, status int, err error, label string) (*mcp.CallToolResult, error) {
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}
	if status >= 400 {
		return mcp.NewToolResultError(fmt.Sprintf("%s: %d %s", label, status, string(body))), nil
	}
	return mcp.NewToolResultText(string(body)), nil
}
