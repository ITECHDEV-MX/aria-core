package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/skillmaintainers"
)

// runSkillMaintainersAdmin opens a DB connection from
// ARIA_CORE_DATABASE_URL and dispatches the supplied subcommand.
//
// Mirrors runAdmin in admin.go but with a typed Store this package
// owns. We do not extend runAdmin because that one is cloudusers-
// specific.
func runSkillMaintainersAdmin(args []string, fn func(ctx context.Context, store *skillmaintainers.Store, args []string) error) {
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
	store := skillmaintainers.New(db)
	if err := fn(context.Background(), store, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
}

// adminSkillMaintainerAdd implements `aria-core admin skill-maintainer-add`.
//
//	--email           required
//	--github          GitHub username (optional)
//	--domains         comma-separated expertise tags (optional)
//	--added-by-uid    UUID of admin who added (optional)
func adminSkillMaintainerAdd(ctx context.Context, store *skillmaintainers.Store, args []string) error {
	fs := flag.NewFlagSet("skill-maintainer-add", flag.ContinueOnError)
	email := fs.String("email", "", "email del maintainer (requerido)")
	gh := fs.String("github", "", "GitHub username (opcional)")
	domains := fs.String("domains", "", "expertise domains separados por coma (opcional)")
	addedBy := fs.String("added-by-uid", "", "UUID del admin que agrega (opcional)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("--email is required")
	}

	var domainList []string
	for _, d := range strings.Split(*domains, ",") {
		d = strings.TrimSpace(strings.ToLower(d))
		if d != "" {
			domainList = append(domainList, d)
		}
	}

	m, err := store.Add(ctx, skillmaintainers.AddParams{
		Email:            *email,
		GitHubUsername:   strings.TrimSpace(*gh),
		ExpertiseDomains: domainList,
		AddedByUID:       strings.TrimSpace(*addedBy),
	})
	if err != nil {
		return err
	}
	fmt.Printf("✓ maintainer activo\n  id:      %s\n  email:   %s\n  github:  %s\n  domains: %s\n  added:   %s\n",
		m.ID, m.Email, m.GitHubUsername, strings.Join(m.ExpertiseDomains, ","), m.AddedAt.Format("2006-01-02 15:04 UTC"))
	return nil
}

// adminSkillMaintainerList implements `aria-core admin skill-maintainer-list`.
//
//	--all   include revoked maintainers (default: only active)
func adminSkillMaintainerList(ctx context.Context, store *skillmaintainers.Store, args []string) error {
	fs := flag.NewFlagSet("skill-maintainer-list", flag.ContinueOnError)
	all := fs.Bool("all", false, "incluir revoked maintainers")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rows, err := store.List(ctx, !*all)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		fmt.Println("(no maintainers)")
		return nil
	}
	fmt.Printf("%-36s %-30s %-15s %-25s %s\n", "EMAIL", "GITHUB", "STATUS", "DOMAINS", "ADDED")
	for _, m := range rows {
		status := "active"
		if !m.IsActive() {
			status = fmt.Sprintf("revoked (%s)", m.RevokedAt.Format("2006-01-02"))
		}
		fmt.Printf("%-36s %-30s %-15s %-25s %s\n",
			truncateStr(m.Email, 35),
			truncateStr(m.GitHubUsername, 29),
			status,
			truncateStr(strings.Join(m.ExpertiseDomains, ","), 24),
			m.AddedAt.Format("2006-01-02"),
		)
	}
	return nil
}

// adminSkillMaintainerRevoke implements `aria-core admin skill-maintainer-revoke`.
//
//	--email   required
func adminSkillMaintainerRevoke(ctx context.Context, store *skillmaintainers.Store, args []string) error {
	fs := flag.NewFlagSet("skill-maintainer-revoke", flag.ContinueOnError)
	email := fs.String("email", "", "email del maintainer a revocar (requerido)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("--email is required")
	}
	if err := store.Revoke(ctx, *email); err != nil {
		return err
	}
	fmt.Printf("✓ maintainer revocado: %s\n", *email)
	return nil
}

// truncate is a tiny helper for the list table.
func truncateStr(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}
