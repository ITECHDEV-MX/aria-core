package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// cmdKB dispatch: aria-core kb <subcommand>.
//
// Subcommands:
//
//	status [--project=PID]
//	sync [--project=PID] [--type=prd|historia|cotizacion]
//	pull
//
// Hablan con /v1/knowledge-base/* del cloudserver. Usan el JWT cacheado en
// ~/.aria-core/session.json (corre `aria-core login` primero).
func cmdKB(cfg store.Config) {
	if len(os.Args) < 3 {
		printKBUsage()
		exitFunc(2)
		return
	}
	switch os.Args[2] {
	case "status":
		runKBStatus(cfg, os.Args[3:])
	case "sync":
		runKBSync(cfg, os.Args[3:])
	case "pull":
		runKBPull(cfg, os.Args[3:])
	case "help", "--help", "-h":
		printKBUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown kb subcommand: %s\n\n", os.Args[2])
		printKBUsage()
		exitFunc(2)
	}
}

func printKBUsage() {
	fmt.Println(`aria-core kb — knowledge-base sync (wave 8)

Subcommands:
  status [--project=PID]
      Counts globales del tracking aria_kb_synced_entities. Filtrá por
      project_id para ver solo lo de un proyecto.

  sync [--project=PID] [--type=prd|historia|cotizacion]
      Re-sincea entidades al repo central. Sin flags, intenta re-sync
      de todo lo failed. Con --project, re-sincea TODO lo del proyecto.

  pull
      Pull del repo central — log de últimos commits. NO sobre-escribe el
      estado local. Útil para inspeccionar qué se ha sincronizado.

Requiere: aria-core login.`)
}

func runKBStatus(cfg store.Config, args []string) {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	project := fs.String("project", "", "filtrar por project_id")
	if err := fs.Parse(args); err != nil {
		exitFunc(1)
		return
	}
	body, status, err := kbDo(cfg, http.MethodGet, "/v1/knowledge-base/status", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "error: %d %s\n", status, string(body))
		exitFunc(1)
		return
	}
	fmt.Println(string(body))
	if *project != "" {
		fmt.Println("(nota: --project requiere endpoint de detail; status global mostrado arriba)")
	}
}

func runKBSync(cfg store.Config, args []string) {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	project := fs.String("project", "", "project_id a re-sincear (UUID)")
	kind := fs.String("type", "", "tipo: prd|historia|cotizacion (informativo)")
	if err := fs.Parse(args); err != nil {
		exitFunc(1)
		return
	}
	_ = kind // exposed for forward-compat; backend re-syncs all kinds for project
	var path string
	if *project != "" {
		path = "/v1/knowledge-base/sync/" + *project
	} else {
		path = "/v1/knowledge-base/sync"
	}
	body, status, err := kbDo(cfg, http.MethodPost, path, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "error: %d %s\n", status, string(body))
		exitFunc(1)
		return
	}
	fmt.Println(string(body))
}

func runKBPull(cfg store.Config, args []string) {
	// Pull es read-only: pega al status endpoint para el listado y muestra
	// qué hay en el tracking. NO consulta GitHub directamente — para eso
	// el dashboard /knowledge-base es más útil.
	body, status, err := kbDo(cfg, http.MethodGet, "/v1/knowledge-base/status", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "error: %d %s\n", status, string(body))
		exitFunc(1)
		return
	}
	fmt.Println("Tracking actual del knowledge-base sync:")
	fmt.Println(string(body))
	fmt.Println()
	fmt.Println("Para ver detalle por entidad: abrí /dashboard/knowledge-base")
}

// cmdQuote dispatch: aria-core quote <subcommand>.
//
// Subcommands:
//
//	export-docx QUOTE_ID --output FILE
//	export-md   QUOTE_ID [--output FILE]
//	sync-to-kb  QUOTE_ID
func cmdQuote(cfg store.Config) {
	if len(os.Args) < 3 {
		printQuoteUsage()
		exitFunc(2)
		return
	}
	switch os.Args[2] {
	case "export-docx":
		runQuoteExportDOCX(cfg, os.Args[3:])
	case "export-md", "export-markdown":
		runQuoteExportMarkdown(cfg, os.Args[3:])
	case "sync-to-kb", "sync":
		runQuoteSyncToKB(cfg, os.Args[3:])
	case "help", "--help", "-h":
		printQuoteUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown quote subcommand: %s\n\n", os.Args[2])
		printQuoteUsage()
		exitFunc(2)
	}
}

func printQuoteUsage() {
	fmt.Println(`aria-core quote — export + sync de cotizaciones (wave 8)

Subcommands:
  export-docx QUOTE_ID --output FILE
      Genera el DOCX iTechDev de una cotización y lo escribe a FILE.
      Requiere pandoc instalado en el server.

  export-md QUOTE_ID [--output FILE]
      Imprime/escribe el .md fuente de la cotización.

  sync-to-kb QUOTE_ID
      Fuerza el sync de la cotización al repo central
      (ITECHDEV-MX/team-knowledge-base).

Requiere: aria-core login.`)
}

func runQuoteExportDOCX(cfg store.Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "QUOTE_ID is required")
		exitFunc(2)
		return
	}
	qid := args[0]
	fs := flag.NewFlagSet("export-docx", flag.ContinueOnError)
	output := fs.String("output", "", "archivo destino (default stdout — binario, no recomendado)")
	if err := fs.Parse(args[1:]); err != nil {
		exitFunc(1)
		return
	}
	body, status, err := kbDo(cfg, http.MethodGet, "/v1/cotizador/quotes/"+qid+"/export/docx", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "error: %d %s\n", status, string(body))
		exitFunc(1)
		return
	}
	if *output == "" {
		fmt.Fprintln(os.Stderr, "WARNING: emitting binary DOCX to stdout. Use --output FILE.")
		_, _ = os.Stdout.Write(body)
		return
	}
	if err := os.WriteFile(*output, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *output, err)
		exitFunc(1)
		return
	}
	fmt.Printf("✓ DOCX escrito (%d bytes) en %s\n", len(body), *output)
}

func runQuoteExportMarkdown(cfg store.Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "QUOTE_ID is required")
		exitFunc(2)
		return
	}
	qid := args[0]
	fs := flag.NewFlagSet("export-md", flag.ContinueOnError)
	output := fs.String("output", "", "archivo destino (default stdout)")
	if err := fs.Parse(args[1:]); err != nil {
		exitFunc(1)
		return
	}
	body, status, err := kbDo(cfg, http.MethodGet, "/v1/cotizador/quotes/"+qid+"/export/markdown", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "error: %d %s\n", status, string(body))
		exitFunc(1)
		return
	}
	if *output == "" {
		_, _ = os.Stdout.Write(body)
		return
	}
	if err := os.WriteFile(*output, body, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", *output, err)
		exitFunc(1)
		return
	}
	fmt.Printf("✓ MD escrito (%d bytes) en %s\n", len(body), *output)
}

func runQuoteSyncToKB(cfg store.Config, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "QUOTE_ID is required")
		exitFunc(2)
		return
	}
	qid := args[0]
	body, status, err := kbDo(cfg, http.MethodPost, "/v1/cotizador/quotes/"+qid+"/sync-to-kb", nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
	if status >= 400 {
		fmt.Fprintf(os.Stderr, "error: %d %s\n", status, string(body))
		exitFunc(1)
		return
	}
	fmt.Println(string(body))
}

// kbDo es un thin HTTP wrapper que carga el session.json y emite un
// request al cloud. Si la response no es JSON parseable lo retorna como
// bytes raw (útil para los exports binarios).
func kbDo(cfg store.Config, method, path string, body any) ([]byte, int, error) {
	sess, err := loadSession(cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("load session: %w", err)
	}
	if sess == nil {
		return nil, 0, fmt.Errorf("no active session — corre 'aria-core login' primero")
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		return nil, 0, fmt.Errorf("session expired — corre 'aria-core login' nuevamente")
	}
	url := strings.TrimRight(sess.Server, "/") + path
	var rdr *bytes.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		rdr = bytes.NewReader(buf)
	}
	var req *http.Request
	if rdr != nil {
		req, err = http.NewRequest(method, url, rdr)
	} else {
		req, err = http.NewRequest(method, url, nil)
	}
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+sess.Token)
	req.Header.Set("Content-Type", "application/json")
	cli := &http.Client{Timeout: 60 * time.Second}
	resp, err := cli.Do(req)
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
