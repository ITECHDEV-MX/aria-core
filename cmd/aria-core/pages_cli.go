package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/comments"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/databases"
)

// cmdPages — CLI dispatch for `aria-core pages <subcommand>`.
//
//	db create PAGE_ID --schema=schema.json
//	db rows list PAGE_ID [--filter=key:op:value] [--sort=key:dir]
//	db rows add  PAGE_ID --props='{"k":"v"}'
//	db rows del  ROW_ID
func cmdPages() {
	if len(os.Args) < 3 {
		printPagesUsage()
		exitFunc(2)
		return
	}
	switch os.Args[2] {
	case "db":
		cmdPagesDB()
	case "help", "--help", "-h":
		printPagesUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown pages subcommand: %s\n\n", os.Args[2])
		printPagesUsage()
		exitFunc(2)
	}
}

func printPagesUsage() {
	fmt.Println(`aria-core pages — inline databases (Notion-style)

Subcommands:
  db create PAGE_ID --schema=schema.json
  db rows list PAGE_ID [--filter=key:op:value] [--sort=key:asc|desc]
  db rows add  PAGE_ID --props='{"k":"v"}'
  db rows del  ROW_ID

Schema JSON file is an array of property definitions:
  [
    {"key":"title","name":"Title","type":"text","required":true},
    {"key":"status","name":"Status","type":"select","options":["todo","done"]}
  ]

Requires ARIA_CORE_DATABASE_URL=postgres://...`)
}

func cmdPagesDB() {
	if len(os.Args) < 4 {
		printPagesUsage()
		exitFunc(2)
		return
	}
	sub := os.Args[3]
	args := os.Args[4:]
	switch sub {
	case "create":
		runPagesDBCmd(args, dbCreate)
	case "rows":
		if len(args) < 1 {
			printPagesUsage()
			exitFunc(2)
			return
		}
		switch args[0] {
		case "list":
			runPagesDBCmd(args[1:], dbRowsList)
		case "add":
			runPagesDBCmd(args[1:], dbRowsAdd)
		case "del":
			runPagesDBCmd(args[1:], dbRowsDel)
		default:
			fmt.Fprintf(os.Stderr, "unknown rows subcommand: %s\n", args[0])
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

// cmdComments — CLI for `aria-core comments`.
//
//	list PAGE_ID [--unresolved]
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

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func truncateString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
