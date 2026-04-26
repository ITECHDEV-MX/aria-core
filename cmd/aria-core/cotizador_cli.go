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

// cmdCotizadorCLI dispatches `aria-core cotizador <subcommand>`.
//
// Subcommands implemented (wave 6):
//
//	chat list [--lead UUID]           — list chat sessions, optionally filtered
//	chat show SESSION_ID              — show messages + sections of a session
//	chat send SESSION_ID --message="..." [--sensitivity=client]
//	                                  — append a user message and dispatch to the LLM
//	chat finalize SESSION_ID          — finalize the session and create the quote
func cmdCotizadorCLI(cfg store.Config) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: aria-core cotizador <subcommand>")
		fmt.Fprintln(os.Stderr, "supported subcommands: chat")
		exitFunc(1)
		return
	}
	switch os.Args[2] {
	case "chat":
		cmdCotizadorChatDispatch(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown cotizador subcommand: %s\n", os.Args[2])
		exitFunc(1)
	}
}

func cmdCotizadorChatDispatch(cfg store.Config) {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: aria-core cotizador chat <list|show|send|finalize> [args]")
		exitFunc(1)
		return
	}
	switch os.Args[3] {
	case "list":
		cmdCotizadorChatList(cfg)
	case "show":
		cmdCotizadorChatShow(cfg)
	case "send":
		cmdCotizadorChatSend(cfg)
	case "finalize":
		cmdCotizadorChatFinalize(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown chat command: %s\n", os.Args[3])
		exitFunc(1)
	}
}

func cmdCotizadorChatList(cfg store.Config) {
	leadID := ""
	for i := 4; i < len(os.Args); i++ {
		switch {
		case strings.HasPrefix(os.Args[i], "--lead="):
			leadID = strings.TrimPrefix(os.Args[i], "--lead=")
		case os.Args[i] == "--lead" && i+1 < len(os.Args):
			leadID = os.Args[i+1]
			i++
		}
	}
	cli, err := newCotizadorCLIClient(cfg)
	if err != nil {
		fatal(err)
		return
	}
	// /v1 has no list-sessions endpoint exposed; fall back to /dashboard route
	// is not auth-compatible. So we expose the data via the chat/get endpoint
	// which also returns sessions tied to the lead. For the basic list we just
	// document this as best-effort and tell user to use the dashboard for the
	// canonical list.
	if leadID == "" {
		fmt.Println("Listado de sesiones quote-chat:")
		fmt.Println("  (CLI no soporta listado masivo todavía — usá /dashboard/cotizador/quote-chat)")
		return
	}
	// Echo the lead ID for debugging — eventually we'd add /v1/cotizador/leads/{id}/chats
	fmt.Printf("Sesiones para lead %s:\n", leadID)
	body, code, err := cli.do(context.Background(), http.MethodGet, "/v1/cotizador/leads/"+url.PathEscape(leadID), nil)
	if err != nil {
		fatal(err)
		return
	}
	if code >= 400 {
		fmt.Fprintln(os.Stderr, string(body))
		exitFunc(1)
		return
	}
	fmt.Println(string(body))
}

func cmdCotizadorChatShow(cfg store.Config) {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: aria-core cotizador chat show SESSION_ID")
		exitFunc(1)
		return
	}
	id := strings.TrimSpace(os.Args[4])
	cli, err := newCotizadorCLIClient(cfg)
	if err != nil {
		fatal(err)
		return
	}
	body, code, err := cli.do(context.Background(), http.MethodGet, "/v1/cotizador/chat/"+url.PathEscape(id), nil)
	if err != nil {
		fatal(err)
		return
	}
	if code >= 400 {
		fmt.Fprintln(os.Stderr, string(body))
		exitFunc(1)
		return
	}
	prettyPrintJSON(body)
}

func cmdCotizadorChatSend(cfg store.Config) {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: aria-core cotizador chat send SESSION_ID --message=\"...\" [--sensitivity=client]")
		exitFunc(1)
		return
	}
	id := strings.TrimSpace(os.Args[4])
	message := ""
	sensitivity := ""
	for i := 5; i < len(os.Args); i++ {
		switch {
		case strings.HasPrefix(os.Args[i], "--message="):
			message = strings.TrimPrefix(os.Args[i], "--message=")
		case os.Args[i] == "--message" && i+1 < len(os.Args):
			message = os.Args[i+1]
			i++
		case strings.HasPrefix(os.Args[i], "--sensitivity="):
			sensitivity = strings.TrimPrefix(os.Args[i], "--sensitivity=")
		case os.Args[i] == "--sensitivity" && i+1 < len(os.Args):
			sensitivity = os.Args[i+1]
			i++
		}
	}
	if strings.TrimSpace(message) == "" {
		fmt.Fprintln(os.Stderr, "error: --message is required")
		exitFunc(1)
		return
	}
	cli, err := newCotizadorCLIClient(cfg)
	if err != nil {
		fatal(err)
		return
	}
	payload := map[string]any{
		"message":     message,
		"sensitivity": sensitivity,
		"timeout_sec": 120,
	}
	body, code, err := cli.do(context.Background(), http.MethodPost, "/v1/cotizador/chat/"+url.PathEscape(id)+"/send", payload)
	if err != nil {
		fatal(err)
		return
	}
	if code >= 400 {
		fmt.Fprintln(os.Stderr, string(body))
		exitFunc(1)
		return
	}
	prettyPrintJSON(body)
}

func cmdCotizadorChatFinalize(cfg store.Config) {
	if len(os.Args) < 5 {
		fmt.Fprintln(os.Stderr, "usage: aria-core cotizador chat finalize SESSION_ID")
		exitFunc(1)
		return
	}
	id := strings.TrimSpace(os.Args[4])
	cli, err := newCotizadorCLIClient(cfg)
	if err != nil {
		fatal(err)
		return
	}
	body, code, err := cli.do(context.Background(), http.MethodPost, "/v1/cotizador/chat/"+url.PathEscape(id)+"/finalize", map[string]string{})
	if err != nil {
		fatal(err)
		return
	}
	if code >= 400 {
		fmt.Fprintln(os.Stderr, string(body))
		exitFunc(1)
		return
	}
	prettyPrintJSON(body)
}

// ─── Tiny REST client ────────────────────────────────────────────────────────

type cotizadorCLIClient struct {
	server string
	token  string
	http   *http.Client
}

func newCotizadorCLIClient(cfg store.Config) (*cotizadorCLIClient, error) {
	sess, err := loadSession(cfg)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}
	if sess == nil {
		return nil, fmt.Errorf("no active session — corre 'aria-core login' primero")
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return nil, fmt.Errorf("session expired — corre 'aria-core login' nuevamente")
	}
	return &cotizadorCLIClient{
		server: strings.TrimRight(sess.Server, "/"),
		token:  sess.Token,
		http:   &http.Client{Timeout: 180 * time.Second},
	}, nil
}

func (c *cotizadorCLIClient) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.server+path, rdr)
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

func prettyPrintJSON(in []byte) {
	var v any
	if err := json.Unmarshal(in, &v); err == nil {
		out, _ := json.MarshalIndent(v, "", "  ")
		fmt.Println(string(out))
		return
	}
	fmt.Println(string(in))
}
