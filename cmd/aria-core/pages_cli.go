// CLI for `aria-core pages <subcommand>` and `aria-core comments <subcommand>`.
//
// Pages subcommands:
//   list                       Lista páginas (filtrable por --project).
//   create                     Crea una página (--title, --parent-id, --template, etc.).
//   export PAGE_ID             Exporta a md|html.
//   seed-templates             Carga los 5 templates builtin (idempotente).
//   import-notion              (TODO) importa un export Notion .zip.
//   attach FILE PAGE_ID        Upload archivo a una página (MIME validated).
//   share PAGE_ID              Crea un public share link (--expires, --password).
//   shares list                Lista share links activos (--page=ID).
//   shares revoke SHARE_ID     Revoca un share link.
//   db create PAGE_ID          Inicializa inline database (--schema=schema.json).
//   db rows list PAGE_ID       Lista rows (--filter, --sort).
//   db rows add  PAGE_ID       Agrega row (--props='{...}').
//   db rows del  ROW_ID        Borra row.
//
// Comments subcommands (separate dispatcher):
//   list PAGE_ID [--unresolved]
//   resolve COMMENT_ID --uid=UUID
//   delete COMMENT_ID
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
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/yuin/goldmark"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/attachments"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/comments"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/databases"
)

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
	case "attach":
		runPagesCmd(args, pagesAttach)
	case "share":
		runPagesCmd(args, pagesShare)
	case "shares":
		if len(args) == 0 {
			printPagesUsage()
			exitFunc(2)
			return
		}
		switch args[0] {
		case "list":
			runPagesCmd(args[1:], pagesSharesList)
		case "revoke":
			runPagesCmd(args[1:], pagesSharesRevoke)
		default:
			fmt.Fprintf(os.Stderr, "unknown shares subcommand: %s\n", args[0])
			printPagesUsage()
			exitFunc(2)
		}
	case "db":
		cmdPagesDB(args)
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

Page subcommands:
  list [--project=X] [--scope=S] [--json]
  create --title="..." [--parent-id=UUID] [--template=KEY] [--project=P]
         [--scope=team] [--icon="📄"] [--by-uid=UUID]
  export PAGE_ID [--format=md|html]
  seed-templates --by-uid=UUID
  import-notion ZIP_FILE                              (TODO)

Attachment + share subcommands:
  attach FILE PAGE_ID [--description="..."] [--uid=UUID]
  share PAGE_ID [--expires=24h] [--password=...] [--copy-to-clipboard] [--uid=UUID]
  shares list --page=ID [--include-revoked]
  shares revoke SHARE_ID [--uid=UUID]

Inline database subcommands:
  db create PAGE_ID --schema=schema.json [--default-view=table|kanban|gallery|list] [--uid=UUID]
  db rows list PAGE_ID [--filter=key:op:value] [--sort=key:asc|desc] [--limit=50]
  db rows add  PAGE_ID --props='{"k":"v"}' [--uid=UUID]
  db rows del  ROW_ID

Templates builtin: prd-v1 | incident-v1 | one-on-one-v1 | adr-v1 | client-onboarding-v1

Requiere ARIA_CORE_DATABASE_URL=postgres://...
Storage de attachments: ARIA_CORE_ATTACHMENTS_DIR (default: /var/lib/aria-core/attachments).`)
}

// pagesCmdContext es el contexto compartido por handlers de pages (CRUD + att/share).
type pagesCmdContext struct {
	db          *sql.DB
	pgStore     *pages.PgStore
	attStore    *attachments.AttachmentStore
	shareStore  *attachments.PgShareStore
	storageRoot string
}

func runPagesCmd(args []string, fn func(ctx context.Context, c *pagesCmdContext, args []string) error) {
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

	pgStore := pages.NewPgStore(db)

	root := strings.TrimSpace(os.Getenv("ARIA_CORE_ATTACHMENTS_DIR"))
	if root == "" {
		root = defaultAttachmentsRoot()
	}
	storage, err := attachments.NewFilesystemStorage(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "init storage: %v\n", err)
		exitFunc(1)
		return
	}
	attStore := attachments.NewAttachmentStore(db, storage, attachments.Config{})
	shareStore := attachments.NewPgShareStore(db)

	c := &pagesCmdContext{
		db:          db,
		pgStore:     pgStore,
		attStore:    attStore,
		shareStore:  shareStore,
		storageRoot: root,
	}
	if err := fn(context.Background(), c, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

// ─── PAGES handlers (CRUD) ──────────────────────────────────────────────────

func pagesList(ctx context.Context, c *pagesCmdContext, args []string) error {
	fs := flag.NewFlagSet("pages list", flag.ExitOnError)
	project := fs.String("project", "", "Filtrar por proyecto")
	scope := fs.String("scope", "", "Filtrar por scope")
	asJSON := fs.Bool("json", false, "Output JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rs, err := c.pgStore.Tree(ctx, *project, *scope)
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

func pagesCreate(ctx context.Context, c *pagesCmdContext, args []string) error {
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

	pg, err := c.pgStore.Create(ctx, pages.CreateParams{
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

func pagesExport(ctx context.Context, c *pagesCmdContext, args []string) error {
	fs := flag.NewFlagSet("pages export", flag.ExitOnError)
	format := fs.String("format", "md", "Formato: md | html")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("PAGE_ID requerido")
	}
	id := fs.Arg(0)
	pg, err := c.pgStore.Get(ctx, id)
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

func pagesSeedTemplates(ctx context.Context, c *pagesCmdContext, args []string) error {
	fs := flag.NewFlagSet("pages seed-templates", flag.ExitOnError)
	byUID := fs.String("by-uid", "", "UUID del usuario que actúa como creador (requerido)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*byUID) == "" {
		return fmt.Errorf("--by-uid es requerido (UUID del usuario)")
	}
	if err := c.pgStore.SeedTemplates(ctx, *byUID); err != nil {
		return err
	}
	fmt.Printf("✓ %d templates seedeados (idempotente)\n", len(pages.BuiltinTemplates()))
	for _, t := range pages.BuiltinTemplates() {
		fmt.Printf("  %s %s — %s\n", t.Icon, t.Key, t.Name)
	}
	return nil
}

// ─── ATTACH/SHARE handlers ─────────────────────────────────────────────────

func pagesAttach(ctx context.Context, c *pagesCmdContext, args []string) error {
	fs := flag.NewFlagSet("attach", flag.ContinueOnError)
	desc := fs.String("description", "", "optional description")
	uid := fs.String("uid", "", "uploader UID (defaults to ARIA_CORE_DEFAULT_UID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 2 {
		return fmt.Errorf("usage: pages attach FILE PAGE_ID [--description=...]")
	}
	file := rest[0]
	pageID := rest[1]
	uploaderUID := *uid
	if uploaderUID == "" {
		uploaderUID = strings.TrimSpace(os.Getenv("ARIA_CORE_DEFAULT_UID"))
	}
	if uploaderUID == "" {
		return fmt.Errorf("either --uid or env ARIA_CORE_DEFAULT_UID is required")
	}
	f, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("open %q: %w", file, err)
	}
	defer f.Close()
	att, err := c.attStore.Upload(ctx, attachments.UploadParams{
		PageID:           pageID,
		OriginalFilename: file,
		Body:             f,
		UploadedByUID:    uploaderUID,
		Description:      *desc,
	})
	if err != nil {
		return err
	}
	fmt.Printf("uploaded %s\n  id=%s\n  size=%s\n  mime=%s\n  storage=%s\n",
		att.OriginalFilename, att.ID, attachments.HumanSize(att.SizeBytes), att.MIMEType, att.StoragePath)
	return nil
}

func pagesShare(ctx context.Context, c *pagesCmdContext, args []string) error {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	expires := fs.String("expires", "24h", "1h|24h|7d|30d|never")
	password := fs.String("password", "", "optional password (bcrypt-hashed)")
	copyClip := fs.Bool("copy-to-clipboard", false, "copy URL to clipboard")
	uid := fs.String("uid", "", "creator UID (defaults to ARIA_CORE_DEFAULT_UID)")
	publicBase := fs.String("public-url", "", "public base URL for printed link (default: ARIA_CORE_PUBLIC_URL or localhost)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: pages share PAGE_ID [--expires=24h] [--password=...]")
	}
	pageID := rest[0]
	creatorUID := *uid
	if creatorUID == "" {
		creatorUID = strings.TrimSpace(os.Getenv("ARIA_CORE_DEFAULT_UID"))
	}
	if creatorUID == "" {
		return fmt.Errorf("either --uid or env ARIA_CORE_DEFAULT_UID is required")
	}
	var expiresAt *time.Time
	if d, ok := parsePagesExpiry(*expires); ok {
		t := time.Now().UTC().Add(d)
		expiresAt = &t
	}
	link, err := c.shareStore.Create(ctx, attachments.CreateShareParams{
		PageID:       pageID,
		Password:     *password,
		ExpiresAt:    expiresAt,
		CreatedByUID: creatorUID,
	})
	if err != nil {
		return err
	}
	base := strings.TrimRight(*publicBase, "/")
	if base == "" {
		base = strings.TrimRight(strings.TrimSpace(os.Getenv("ARIA_CORE_PUBLIC_URL")), "/")
	}
	if base == "" {
		base = "http://localhost:8080"
	}
	url := base + "/p/" + link.Token
	fmt.Printf("share link created\n  id=%s\n  url=%s\n  has_password=%v\n",
		link.ID, url, link.HasPassword)
	if link.ExpiresAt != nil {
		fmt.Printf("  expires=%s\n", link.ExpiresAt.Format(time.RFC3339))
	}
	if *copyClip {
		if err := tryCopyToClipboard(url); err != nil {
			fmt.Fprintf(os.Stderr, "  (clipboard copy failed: %v)\n", err)
		} else {
			fmt.Println("  (URL copied to clipboard)")
		}
	}
	return nil
}

func pagesSharesList(ctx context.Context, c *pagesCmdContext, args []string) error {
	fs := flag.NewFlagSet("shares-list", flag.ContinueOnError)
	page := fs.String("page", "", "filter by page id")
	includeRevoked := fs.Bool("include-revoked", false, "show revoked links too")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *page == "" {
		return fmt.Errorf("--page=ID is required (the share table is per-page)")
	}
	links, err := c.shareStore.ListByPage(ctx, *page, *includeRevoked)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "ID\tTOKEN\tPW?\tVIEWS\tEXPIRES\tCREATED\tREVOKED")
	for _, link := range links {
		expires := "never"
		if link.ExpiresAt != nil {
			expires = link.ExpiresAt.Format(time.RFC3339)
		}
		pw := "no"
		if link.HasPassword {
			pw = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\t%v\n",
			link.ID, truncToken(link.Token), pw, link.ViewCount, expires,
			link.CreatedAt.Format(time.RFC3339), link.IsRevoked)
	}
	if len(links) == 0 {
		fmt.Fprintln(tw, "(no shares)")
	}
	return nil
}

func pagesSharesRevoke(ctx context.Context, c *pagesCmdContext, args []string) error {
	fs := flag.NewFlagSet("shares-revoke", flag.ContinueOnError)
	uid := fs.String("uid", "", "revoker UID (defaults to ARIA_CORE_DEFAULT_UID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: pages shares revoke SHARE_ID")
	}
	id := rest[0]
	by := *uid
	if by == "" {
		by = strings.TrimSpace(os.Getenv("ARIA_CORE_DEFAULT_UID"))
	}
	if err := c.shareStore.Revoke(ctx, id, by); err != nil {
		return err
	}
	fmt.Printf("revoked share %s\n", id)
	return nil
}

// ─── DB inline databases handlers ──────────────────────────────────────────

func cmdPagesDB(args []string) {
	if len(args) < 1 {
		printPagesUsage()
		exitFunc(2)
		return
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "create":
		runPagesDBCmd(rest, dbCreate)
	case "rows":
		if len(rest) < 1 {
			printPagesUsage()
			exitFunc(2)
			return
		}
		switch rest[0] {
		case "list":
			runPagesDBCmd(rest[1:], dbRowsList)
		case "add":
			runPagesDBCmd(rest[1:], dbRowsAdd)
		case "del":
			runPagesDBCmd(rest[1:], dbRowsDel)
		default:
			fmt.Fprintf(os.Stderr, "unknown rows subcommand: %s\n", rest[0])
			exitFunc(2)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown db subcommand: %s\n", sub)
		exitFunc(2)
	}
}

func runPagesDBCmd(args []string, fn func(ctx context.Context, s *databases.Store, args []string) error) {
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_DATABASE_URL"))
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "ARIA_CORE_DATABASE_URL is required")
		exitFunc(1)
		return
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "ping db:", err)
		exitFunc(1)
		return
	}
	store := databases.New(db)
	if err := fn(ctx, store, args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
	}
}

func dbCreate(ctx context.Context, s *databases.Store, args []string) error {
	fs := flag.NewFlagSet("db create", flag.ContinueOnError)
	schemaFile := fs.String("schema", "", "path to JSON file with array of PropDef")
	defaultView := fs.String("default-view", "table", "default view: table|kanban|gallery|list")
	creator := fs.String("uid", "", "creator UID (UUID)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("PAGE_ID required")
	}
	pageID := fs.Arg(0)
	if *schemaFile == "" {
		return fmt.Errorf("--schema is required")
	}
	raw, err := os.ReadFile(*schemaFile)
	if err != nil {
		return err
	}
	var schema []databases.PropDef
	if err := json.Unmarshal(raw, &schema); err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	uid := strings.TrimSpace(*creator)
	if uid == "" {
		uid = "00000000-0000-0000-0000-000000000000"
	}
	out, err := s.Create(ctx, databases.CreateParams{
		PageID: pageID, Schema: schema, DefaultView: *defaultView, CreatedByUID: uid,
	})
	if err != nil {
		return err
	}
	fmt.Printf("database created: id=%s page_id=%s\n", out.ID, out.PageID)
	return nil
}

func dbRowsList(ctx context.Context, s *databases.Store, args []string) error {
	fs := flag.NewFlagSet("rows list", flag.ContinueOnError)
	filter := fs.String("filter", "", "filter spec key:op:value (CSV)")
	sortSpec := fs.String("sort", "", "sort spec key:asc (CSV)")
	limit := fs.Int("limit", 50, "max rows")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("PAGE_ID required")
	}
	db, err := s.GetByPage(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	opts := databases.ListRowsOpts{Limit: *limit}
	if *filter != "" {
		opts.Filters = parseCLIFilters(*filter)
	}
	if *sortSpec != "" {
		opts.Sorts = parseCLISorts(*sortSpec)
	}
	rows, err := s.ListRows(ctx, db.ID, opts)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	headers := []string{"ROW_ID"}
	for _, def := range db.Schema {
		headers = append(headers, def.Name)
	}
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, r := range rows {
		cells := []string{shortID(r.ID)}
		for _, def := range db.Schema {
			v := ""
			if x, ok := r.Props[def.Key]; ok && x != nil {
				if s, ok := x.(string); ok {
					v = s
				} else {
					b, _ := json.Marshal(x)
					v = string(b)
				}
			}
			cells = append(cells, v)
		}
		fmt.Fprintln(tw, strings.Join(cells, "\t"))
	}
	return nil
}

func dbRowsAdd(ctx context.Context, s *databases.Store, args []string) error {
	fs := flag.NewFlagSet("rows add", flag.ContinueOnError)
	propsFlag := fs.String("props", "{}", "props JSON")
	creator := fs.String("uid", "", "creator UID")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("PAGE_ID required")
	}
	db, err := s.GetByPage(ctx, fs.Arg(0))
	if err != nil {
		return err
	}
	var props map[string]any
	if err := json.Unmarshal([]byte(*propsFlag), &props); err != nil {
		return fmt.Errorf("parse props: %w", err)
	}
	uid := strings.TrimSpace(*creator)
	if uid == "" {
		uid = "00000000-0000-0000-0000-000000000000"
	}
	row, err := s.CreateRow(ctx, databases.CreateRowParams{
		DatabaseID: db.ID, Props: props, CreatedByUID: uid,
	})
	if err != nil {
		return err
	}
	fmt.Printf("row added: %s\n", row.ID)
	return nil
}

func dbRowsDel(ctx context.Context, s *databases.Store, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("ROW_ID required")
	}
	if err := s.DeleteRow(ctx, args[0]); err != nil {
		return err
	}
	fmt.Println("row deleted")
	return nil
}

func parseCLIFilters(raw string) []databases.Filter {
	parts := strings.Split(raw, ",")
	out := []databases.Filter{}
	for _, p := range parts {
		segs := strings.SplitN(p, ":", 3)
		if len(segs) < 2 {
			continue
		}
		f := databases.Filter{Key: segs[0], Op: databases.FilterOp(segs[1])}
		if len(segs) == 3 {
			f.Value = segs[2]
		}
		out = append(out, f)
	}
	return out
}

func parseCLISorts(raw string) []databases.Sort {
	parts := strings.Split(raw, ",")
	out := []databases.Sort{}
	for _, p := range parts {
		segs := strings.SplitN(p, ":", 2)
		s := databases.Sort{Key: segs[0], Direction: "asc"}
		if len(segs) == 2 {
			s.Direction = segs[1]
		}
		out = append(out, s)
	}
	return out
}

// ─── COMMENTS dispatcher (`aria-core comments <subcommand>`) ────────────────

func cmdComments() {
	if len(os.Args) < 3 {
		fmt.Println(`aria-core comments — page comments

Subcommands:
  list PAGE_ID [--unresolved] [--limit=N]
  resolve COMMENT_ID --uid=UUID
  delete COMMENT_ID

Requires ARIA_CORE_DATABASE_URL.`)
		return
	}
	dsn := strings.TrimSpace(os.Getenv("ARIA_CORE_DATABASE_URL"))
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "ARIA_CORE_DATABASE_URL is required")
		exitFunc(1)
		return
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
		return
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "ping db:", err)
		exitFunc(1)
		return
	}
	store := comments.New(db)
	switch os.Args[2] {
	case "list":
		commentsList(ctx, store, os.Args[3:])
	case "resolve":
		commentsResolve(ctx, store, os.Args[3:])
	case "delete":
		commentsDelete(ctx, store, os.Args[3:])
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[2])
		exitFunc(2)
	}
}

func commentsList(ctx context.Context, s *comments.Store, args []string) {
	fs := flag.NewFlagSet("comments list", flag.ContinueOnError)
	unresolved := fs.Bool("unresolved", false, "only unresolved")
	limit := fs.Int("limit", 50, "max rows")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
		return
	}
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "PAGE_ID required")
		exitFunc(2)
		return
	}
	cs, err := s.ListPage(ctx, fs.Arg(0), comments.ListPageOpts{
		OnlyUnresolved: *unresolved,
		Limit:          *limit,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
		return
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "ID\tAUTHOR\tRESOLVED\tREPLIES\tCONTENT")
	for _, c := range cs {
		fmt.Fprintf(tw, "%s\t%s\t%v\t%d\t%s\n",
			shortID(c.ID), shortID(c.AuthorUID), c.IsResolved, c.ReplyCount, truncateString(c.ContentMD, 40))
	}
}

func commentsResolve(ctx context.Context, s *comments.Store, args []string) {
	fs := flag.NewFlagSet("comments resolve", flag.ContinueOnError)
	uid := fs.String("uid", "", "resolver UID")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
		return
	}
	if fs.NArg() < 1 || *uid == "" {
		fmt.Fprintln(os.Stderr, "COMMENT_ID and --uid required")
		exitFunc(2)
		return
	}
	if err := s.Resolve(ctx, fs.Arg(0), *uid); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
		return
	}
	fmt.Println("resolved")
}

func commentsDelete(ctx context.Context, s *comments.Store, args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "COMMENT_ID required")
		exitFunc(2)
		return
	}
	if err := s.Delete(ctx, args[0]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		exitFunc(1)
		return
	}
	fmt.Println("deleted")
}

// ─── Helpers ────────────────────────────────────────────────────────────────

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

func parsePagesExpiry(s string) (time.Duration, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "":
		return 0, false
	case "1h":
		return time.Hour, true
	case "24h", "1d":
		return 24 * time.Hour, true
	case "7d":
		return 7 * 24 * time.Hour, true
	case "30d":
		return 30 * 24 * time.Hour, true
	case "never":
		return 0, false
	}
	if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return d, true
	}
	return 0, false
}

func truncToken(t string) string {
	if len(t) <= 12 {
		return t
	}
	return t[:8] + "…" + t[len(t)-4:]
}

// tryCopyToClipboard pipes url to pbcopy/xclip/wl-copy depending on OS.
func tryCopyToClipboard(url string) error {
	candidates := [][]string{
		{"pbcopy"},
		{"wl-copy"},
		{"xclip", "-selection", "clipboard"},
	}
	for _, c := range candidates {
		path, err := exec.LookPath(c[0])
		if err != nil || path == "" {
			continue
		}
		cmd := exec.Command(path, c[1:]...)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			continue
		}
		if err := cmd.Start(); err != nil {
			continue
		}
		_, _ = stdin.Write([]byte(url))
		_ = stdin.Close()
		if err := cmd.Wait(); err == nil {
			return nil
		}
	}
	return fmt.Errorf("no clipboard tool found (install pbcopy/xclip/wl-copy)")
}
