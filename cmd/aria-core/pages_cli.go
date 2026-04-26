// CLI for `aria-core pages <subcommand>`.
//
// Subcommands:
//   attach FILE PAGE_ID [--description=...]
//   share PAGE_ID [--expires=24h] [--password=...] [--copy-to-clipboard]
//   shares list [--page=ID]
//   shares revoke SHARE_ID
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/pages/attachments"
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
	case "help", "--help", "-h":
		printPagesUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown pages subcommand: %s\n\n", sub)
		printPagesUsage()
		exitFunc(2)
	}
}

func printPagesUsage() {
	fmt.Println(`aria-core pages — file attachments + public share links

Subcommands:
  attach FILE PAGE_ID [--description="..."]
                                  Upload FILE to PAGE_ID. MIME validated.
  share PAGE_ID [--expires=24h] [--password=...] [--copy-to-clipboard]
                                  Create a public share link.
                                  --expires accepts 1h | 24h | 7d | 30d | never
  shares list [--page=ID]         List active share links (filter by page).
  shares revoke SHARE_ID          Revoke a share link.

Requires ARIA_CORE_DATABASE_URL=postgres://...
File storage rooted at ARIA_CORE_ATTACHMENTS_DIR (default: /var/lib/aria-core/attachments).`)
}

type pagesCmdContext struct {
	db          *sql.DB
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

	ctx := context.Background()
	c := &pagesCmdContext{db: db, attStore: attStore, shareStore: shareStore, storageRoot: root}
	if err := fn(ctx, c, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

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

// tryCopyToClipboard pipes url to pbcopy/xclip/wl-copy depending on OS. Best
// effort — returns the underlying error so the user can switch tools.
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
