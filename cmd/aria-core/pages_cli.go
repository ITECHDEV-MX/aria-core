package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/yuin/goldmark"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages"
)

// cmdPages — CLI dispatch para `aria-core pages <subcommand>`.
//
//	list                       Lista páginas (filtrable por --project).
//	create                     Crea una página (--title, --parent-id, --template, etc.).
//	export PAGE_ID             Exporta a md|html.
//	seed-templates             Carga los 5 templates builtin (idempotente).
//	import-notion              (TODO) importa un export Notion .zip.
func cmdPages() {
	if len(os.Args) < 3 {
		printPagesUsage()
		exitFunc(2)
		return
	}
	sub := os.Args[2]
	args := os.Args[3:]
	switch sub {
	case "list":
		runPagesCmd(args, pagesList)
	case "create":
		runPagesCmd(args, pagesCreate)
	case "export":
		runPagesCmd(args, pagesExport)
	case "seed-templates":
		runPagesCmd(args, pagesSeedTemplates)
	case "import-notion":
		fmt.Fprintln(os.Stderr, "import-notion: TODO — preserva jerarquía de export Notion .zip y convierte links internos a slugs aria_pages.")
		exitFunc(2)
	case "help", "--help", "-h":
		printPagesUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown pages subcommand: %s\n\n", sub)
		printPagesUsage()
		exitFunc(2)
	}
}

func printPagesUsage() {
	fmt.Println(`aria-core pages — mini-Notion (wiki) CLI

Subcommands:
  list [--project=X] [--scope=S] [--json]
                              Lista páginas no archivadas.
  create --title="..." [--parent-id=UUID] [--template=KEY] [--project=P]
         [--scope=team] [--icon="📄"] [--by-uid=UUID]
                              Crea una página. Si --template, hidrata body.
  export PAGE_ID [--format=md|html]
                              Imprime el contenido renderizado a stdout.
  seed-templates --by-uid=UUID
                              Carga los 5 templates builtin (idempotente).
  import-notion ZIP_FILE      (TODO) Importa export Notion preservando jerarquía.

Templates builtin: prd-v1 | incident-v1 | one-on-one-v1 | adr-v1 | client-onboarding-v1

Requiere ARIA_CORE_DATABASE_URL=postgres://...`)
}

// runPagesCmd abre DB y entrega un PgStore al handler.
func runPagesCmd(args []string, fn func(ctx context.Context, store *pages.PgStore, args []string) error) {
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_DATABASE_URL"))
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "ARIA_CORE_DATABASE_URL is required (postgres://...)")
		exitFunc(1)
		return
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open db: %v\n", err)
		exitFunc(1)
		return
	}
	defer db.Close()
	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "ping db: %v\n", err)
		exitFunc(1)
		return
	}
	st := pages.NewPgStore(db)
	if err := fn(context.Background(), st, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

// ─── Subcommand implementations ─────────────────────────────────────────────

func pagesList(ctx context.Context, store *pages.PgStore, args []string) error {
	fs := flag.NewFlagSet("pages list", flag.ExitOnError)
	project := fs.String("project", "", "Filtrar por proyecto")
	scope := fs.String("scope", "", "Filtrar por scope")
	asJSON := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rs, err := store.Tree(ctx, *project, *scope)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rs)
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tTITLE\tPROJECT\tSCOPE\tTYPE\tCHILDREN")
	for _, p := range rs {
		title := truncatePages(p.Title, 50)
		if p.Icon != "" {
			title = p.Icon + " " + title
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n",
			shortID(p.ID), title, p.Project, p.Scope, p.PageType, p.ChildrenCount)
	}
	return tw.Flush()
}

func pagesCreate(ctx context.Context, store *pages.PgStore, args []string) error {
	fs := flag.NewFlagSet("pages create", flag.ExitOnError)
	title := fs.String("title", "", "Título (requerido)")
	parentID := fs.String("parent-id", "", "UUID del parent (opcional)")
	template := fs.String("template", "", "Template key (prd-v1, incident-v1, ...)")
	project := fs.String("project", "", "Proyecto")
	scope := fs.String("scope", "team", "Scope: personal|project|team|client_knowledge")
	icon := fs.String("icon", "", "Emoji opcional")
	byUID := fs.String("by-uid", "", "UUID del usuario creador (requerido)")
	contentFile := fs.String("content-file", "", "Lee body desde este archivo (override del template)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*title) == "" {
		return fmt.Errorf("--title es requerido")
	}
	if strings.TrimSpace(*byUID) == "" {
		return fmt.Errorf("--by-uid es requerido (UUID del usuario)")
	}

	contentMD := ""
	usedIcon := *icon
	if k := strings.TrimSpace(*template); k != "" {
		tpl := pages.GetBuiltinTemplate(k)
		if tpl == nil {
			return fmt.Errorf("template %q no existe (ver `aria-core pages seed-templates`)", k)
		}
		contentMD = tpl.BodyMD
		if usedIcon == "" {
			usedIcon = tpl.Icon
		}
	}
	if path := strings.TrimSpace(*contentFile); path != "" {
		buf, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read content-file: %w", err)
		}
		contentMD = string(buf)
	}

	pg, err := store.Create(ctx, pages.CreateParams{
		ParentID:     *parentID,
		Title:        *title,
		ContentMD:    contentMD,
		Icon:         usedIcon,
		Project:      *project,
		Scope:        *scope,
		PageType:     "doc",
		TemplateKey:  *template,
		Sensitivity:  "internal",
		CreatedByUID: *byUID,
	})
	if err != nil {
		return err
	}
	fmt.Printf("✓ creada página %s\n  Title: %s\n  Slug:  %s\n", pg.ID, pg.Title, pages.Slug(pg))
	return nil
}

func pagesExport(ctx context.Context, store *pages.PgStore, args []string) error {
	fs := flag.NewFlagSet("pages export", flag.ExitOnError)
	format := fs.String("format", "md", "Formato: md | html")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("PAGE_ID requerido")
	}
	id := fs.Arg(0)
	pg, err := store.Get(ctx, id)
	if err != nil {
		return err
	}
	switch strings.ToLower(*format) {
	case "md", "markdown":
		_, _ = io.WriteString(os.Stdout, pg.ContentMD)
		if !strings.HasSuffix(pg.ContentMD, "\n") {
			fmt.Println()
		}
	case "html":
		var buf bytes.Buffer
		if err := goldmark.Convert([]byte(pg.ContentMD), &buf); err != nil {
			return fmt.Errorf("render html: %w", err)
		}
		fmt.Printf("<!DOCTYPE html><html><head><title>%s</title></head><body>\n", htmlEscape(pg.Title))
		_, _ = io.Copy(os.Stdout, &buf)
		fmt.Println("\n</body></html>")
	default:
		return fmt.Errorf("formato no soportado: %s (usa md o html)", *format)
	}
	return nil
}

func pagesSeedTemplates(ctx context.Context, store *pages.PgStore, args []string) error {
	fs := flag.NewFlagSet("pages seed-templates", flag.ExitOnError)
	byUID := fs.String("by-uid", "", "UUID del usuario que actúa como creador (requerido)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*byUID) == "" {
		return fmt.Errorf("--by-uid es requerido (UUID del usuario)")
	}
	if err := store.SeedTemplates(ctx, *byUID); err != nil {
		return err
	}
	fmt.Printf("✓ %d templates seedeados (idempotente)\n", len(pages.BuiltinTemplates()))
	for _, t := range pages.BuiltinTemplates() {
		fmt.Printf("  %s %s — %s\n", t.Icon, t.Key, t.Name)
	}
	return nil
}

func truncatePages(s string, max int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func shortID(id string) string {
	id = strings.ReplaceAll(id, "-", "")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// htmlEscape minimo para usar en <title>.
func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}
