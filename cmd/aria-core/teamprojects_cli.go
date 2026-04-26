// CLI commands for team-projects (wave 7).
//
//	aria-core team-projects list [--status=active]
//	aria-core team-projects create --name "Foo" [--description "..."] [--no-github]
//	aria-core team-projects add-member --project SLUG --uid UUID --role member
//	aria-core tasks list [--project=SLUG] [--assignee=UID|me] [--status=todo]
//	aria-core tasks create --project SLUG --title "..." [--description "..."] [--priority high]
//	aria-core tasks assign TASK_ID UID
//	aria-core tasks status TASK_ID in_progress|review|done
//	aria-core tasks close TASK_ID
//
// Estos comandos hablan con el cloud REST API (mismo JWT que MCP — session.json).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

func cmdTeamProjects(cfg store.Config) {
	sub := "list"
	if len(os.Args) > 2 {
		sub = os.Args[2]
	}
	switch sub {
	case "list":
		cmdTeamProjectsList(cfg, os.Args[3:])
	case "create":
		cmdTeamProjectsCreate(cfg, os.Args[3:])
	case "add-member":
		cmdTeamProjectsAddMember(cfg, os.Args[3:])
	case "":
		fmt.Fprintln(os.Stderr, "usage: aria-core team-projects <list|create|add-member> [flags]")
		exitFunc(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown team-projects subcommand: %s\n", sub)
		exitFunc(1)
	}
}

func cmdTasks(cfg store.Config) {
	sub := "list"
	if len(os.Args) > 2 {
		sub = os.Args[2]
	}
	switch sub {
	case "list":
		cmdTasksList(cfg, os.Args[3:])
	case "create":
		cmdTasksCreate(cfg, os.Args[3:])
	case "assign":
		cmdTasksAssign(cfg, os.Args[3:])
	case "status":
		cmdTasksStatus(cfg, os.Args[3:])
	case "close":
		cmdTasksClose(cfg, os.Args[3:])
	case "":
		fmt.Fprintln(os.Stderr, "usage: aria-core tasks <list|create|assign|status|close> [flags|args]")
		exitFunc(1)
	default:
		fmt.Fprintf(os.Stderr, "unknown tasks subcommand: %s\n", sub)
		exitFunc(1)
	}
}

// ─── helpers ────────────────────────────────────────────────────────────

type cliClient struct {
	server string
	token  string
	http   *http.Client
}

func mustCloudClient(cfg store.Config) *cliClient {
	sess, err := loadSession(cfg)
	if err != nil {
		fatal(fmt.Errorf("load session: %w", err))
	}
	if sess == nil {
		fmt.Fprintln(os.Stderr, "no active session — corre 'aria-core login' primero")
		exitFunc(1)
		return nil
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		fmt.Fprintln(os.Stderr, "session expired — corre 'aria-core login' nuevamente")
		exitFunc(1)
		return nil
	}
	return &cliClient{server: sess.Server, token: sess.Token, http: &http.Client{Timeout: 30 * time.Second}}
}

func (c *cliClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.server, "/")+path, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
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

func parseFlagsArgs(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "--") {
			continue
		}
		key := strings.TrimPrefix(a, "--")
		val := "true"
		if eq := strings.Index(key, "="); eq >= 0 {
			val = key[eq+1:]
			key = key[:eq]
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "--") {
			val = args[i+1]
			i++
		}
		out[key] = val
	}
	return out
}

func prettyJSON(b []byte) string {
	var any interface{}
	if err := json.Unmarshal(b, &any); err != nil {
		return string(b)
	}
	out, err := json.MarshalIndent(any, "", "  ")
	if err != nil {
		return string(b)
	}
	return string(out)
}

func mustOK(body []byte, status int, err error) []byte {
	if err != nil {
		fatal(err)
	}
	if status >= 400 {
		fmt.Fprintln(os.Stderr, "request failed:", status, string(body))
		exitFunc(1)
	}
	return body
}

// ─── team-projects subcommands ──────────────────────────────────────────

func cmdTeamProjectsList(cfg store.Config, args []string) {
	c := mustCloudClient(cfg)
	flags := parseFlagsArgs(args)
	q := url.Values{}
	if v, ok := flags["status"]; ok {
		q.Set("status", v)
	}
	body, status, err := c.do(context.Background(), http.MethodGet, "/v1/team-projects?"+q.Encode(), nil)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

func cmdTeamProjectsCreate(cfg store.Config, args []string) {
	c := mustCloudClient(cfg)
	flags := parseFlagsArgs(args)
	if flags["name"] == "" {
		fmt.Fprintln(os.Stderr, "usage: aria-core team-projects create --name STRING [--slug SLUG] [--description STRING] [--client_id UUID] [--no-github]")
		exitFunc(1)
	}
	pl := map[string]any{
		"name":        flags["name"],
		"slug":        flags["slug"],
		"description": flags["description"],
		"client_id":   flags["client_id"],
		"no_github":   flags["no-github"] == "true" || flags["no_github"] == "true",
	}
	body, status, err := c.do(context.Background(), http.MethodPost, "/v1/team-projects", pl)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

func cmdTeamProjectsAddMember(cfg store.Config, args []string) {
	c := mustCloudClient(cfg)
	flags := parseFlagsArgs(args)
	pid := firstNonEmpty(flags["project"], flags["project_id"])
	if pid == "" || flags["uid"] == "" {
		fmt.Fprintln(os.Stderr, "usage: aria-core team-projects add-member --project UUID --uid UUID [--role member]")
		exitFunc(1)
	}
	pl := map[string]any{"user_uid": flags["uid"], "role": flags["role"]}
	body, status, err := c.do(context.Background(), http.MethodPost, "/v1/team-projects/"+pid+"/members", pl)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

// ─── tasks subcommands ──────────────────────────────────────────────────

func cmdTasksList(cfg store.Config, args []string) {
	c := mustCloudClient(cfg)
	flags := parseFlagsArgs(args)
	if a := flags["assignee"]; a == "me" {
		body, status, err := c.do(context.Background(), http.MethodGet, "/v1/tasks/assigned-to-me?status="+url.QueryEscape(flags["status"]), nil)
		body = mustOK(body, status, err)
		fmt.Println(prettyJSON(body))
		return
	}
	pid := firstNonEmpty(flags["project"], flags["project_id"])
	if pid == "" {
		fmt.Fprintln(os.Stderr, "usage: aria-core tasks list --project UUID [--status STR] | --assignee me")
		exitFunc(1)
	}
	q := url.Values{}
	if v := flags["status"]; v != "" {
		q.Set("status", v)
	}
	body, status, err := c.do(context.Background(), http.MethodGet, "/v1/team-projects/"+pid+"/tasks?"+q.Encode(), nil)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

func cmdTasksCreate(cfg store.Config, args []string) {
	c := mustCloudClient(cfg)
	flags := parseFlagsArgs(args)
	pid := firstNonEmpty(flags["project"], flags["project_id"])
	if pid == "" || flags["title"] == "" {
		fmt.Fprintln(os.Stderr, "usage: aria-core tasks create --project UUID --title STR [--description STR] [--priority STR]")
		exitFunc(1)
	}
	pl := map[string]any{
		"title":       flags["title"],
		"description": flags["description"],
		"priority":    flags["priority"],
	}
	if v := flags["assignee"]; v != "" {
		pl["assignees"] = []string{v}
	}
	body, status, err := c.do(context.Background(), http.MethodPost, "/v1/team-projects/"+pid+"/tasks", pl)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

func cmdTasksAssign(cfg store.Config, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aria-core tasks assign TASK_ID UID")
		exitFunc(1)
	}
	c := mustCloudClient(cfg)
	pl := map[string]any{"user_uid": args[1]}
	body, status, err := c.do(context.Background(), http.MethodPost, "/v1/tasks/"+args[0]+"/assign", pl)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

func cmdTasksStatus(cfg store.Config, args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: aria-core tasks status TASK_ID STATUS")
		exitFunc(1)
	}
	c := mustCloudClient(cfg)
	pl := map[string]any{"status": args[1]}
	body, status, err := c.do(context.Background(), http.MethodPost, "/v1/tasks/"+args[0]+"/status", pl)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

func cmdTasksClose(cfg store.Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: aria-core tasks close TASK_ID")
		exitFunc(1)
	}
	c := mustCloudClient(cfg)
	body, status, err := c.do(context.Background(), http.MethodPost, "/v1/tasks/"+args[0]+"/close", nil)
	body = mustOK(body, status, err)
	fmt.Println(prettyJSON(body))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
