package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudusers"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/redactor"
)

// cmdAdmin dispatch para `aria-core admin <subcommand>`.
//
// Bootstrap del primer admin desde el VPS:
//
//	aria-core admin create-user --email jc@itechdev.com.mx --name "JC" --role admin --password '****'
//
// Requiere ARIA_CORE_DATABASE_URL apuntando al Postgres del cloud.
func cmdAdmin() {
	if len(os.Args) < 3 {
		printAdminUsage()
		exitFunc(2)
		return
	}
	sub := os.Args[2]
	args := os.Args[3:]

	switch sub {
	case "create-user":
		runAdmin(args, adminCreateUser)
	case "list-users":
		runAdmin(args, adminListUsers)
	case "grant-role":
		runAdmin(args, adminGrantRole)
	case "revoke-role":
		runAdmin(args, adminRevokeRole)
	case "list-roles":
		runAdmin(args, adminListRoles)
	case "set-password":
		runAdmin(args, adminSetPassword)
	case "deactivate":
		runAdmin(args, adminDeactivate)
	case "activate":
		runAdmin(args, adminActivate)
	case "create-invite":
		runAdmin(args, adminCreateInvite)
	case "import-proposals":
		adminImportProposals(args)
	case "migrate-from-legacy":
		adminMigrateFromLegacy(args)
	case "help", "--help", "-h":
		printAdminUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown admin subcommand: %s\n\n", sub)
		printAdminUsage()
		exitFunc(2)
	}
}

func printAdminUsage() {
	fmt.Println(`aria-core admin — gestión de usuarios del dashboard cloud

Subcommands:
  create-user   --email E --name N --role R [--role R2 --role R3] --password P
  list-users
  grant-role    --uid UUID --role admin|dev|cotizador|project_admin
  revoke-role   --uid UUID --role admin|dev|cotizador|project_admin
  list-roles    --uid UUID
  set-password  --uid UUID --password P
  activate      --uid UUID
  deactivate    --uid UUID
  create-invite --email E --role R [--role R2] [--public-url URL]
                      Genera un magic-link UUID en cloud_invites y emite la
                      URL para que el admin la copie/pegue (no envía email
                      desde la CLI; el envío vía Graph se hace desde el
                      dashboard /dashboard/admin/users).
  import-proposals    Importa las 3 propuestas histórico iTechDev (idempotente por folio)
  migrate-from-legacy --sqlite ~/.aria/aria.db [--dry-run]
                      Migra ARIA legacy SQLite → aria_core_cloud Postgres
                      (observations, sessions, summaries, skills)

Roles soportados: admin, dev, cotizador, project_admin
Requiere ARIA_CORE_DATABASE_URL apuntando al Postgres cloud.`)
}

func runAdmin(args []string, fn func(ctx context.Context, store *cloudusers.Store, args []string) error) {
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
	store := cloudusers.New(db)
	if err := fn(context.Background(), store, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
}

// stringSliceFlag implementa flag.Value para colectar --role X --role Y.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string     { return strings.Join(*s, ",") }
func (s *stringSliceFlag) Set(v string) error { *s = append(*s, v); return nil }

func adminCreateUser(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	email := fs.String("email", "", "email del usuario (UNIQUE)")
	name := fs.String("name", "", "nombre")
	var roles stringSliceFlag
	fs.Var(&roles, "role", "rol (puede repetirse): admin | dev | cotizador | project_admin")
	password := fs.String("password", "", "password (≥8 caracteres)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(roles) == 0 {
		roles = stringSliceFlag{cloudusers.RoleDev}
	}
	u, err := store.Create(ctx, *email, *name, roles[0], *password)
	if err != nil {
		return err
	}
	for _, extra := range roles[1:] {
		if err := store.AddRole(ctx, u.UID, extra); err != nil {
			return fmt.Errorf("add role %q: %w", extra, err)
		}
	}
	fmt.Printf("✓ usuario creado\n  uid:   %s\n  email: %s\n  roles: %s\n", u.UID, u.Email, strings.Join(roles, ", "))
	return nil
}

func adminListUsers(ctx context.Context, store *cloudusers.Store, _ []string) error {
	users, err := store.List(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "UID\tEMAIL\tNAME\tROLES\tACTIVE\tCREATED")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\t%s\n", u.UID, u.Email, u.Name, strings.Join(u.Roles, ","), u.IsActive, u.CreatedAt.UTC().Format("2006-01-02 15:04"))
	}
	return nil
}

func adminGrantRole(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("grant-role", flag.ContinueOnError)
	uid := fs.String("uid", "", "uid del usuario")
	role := fs.String("role", "", "rol a agregar")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := store.AddRole(ctx, *uid, *role); err != nil {
		return err
	}
	fmt.Printf("✓ rol %q asignado a uid=%s\n", *role, *uid)
	return nil
}

func adminRevokeRole(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("revoke-role", flag.ContinueOnError)
	uid := fs.String("uid", "", "uid del usuario")
	role := fs.String("role", "", "rol a quitar")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := store.RemoveRole(ctx, *uid, *role); err != nil {
		return err
	}
	fmt.Printf("✓ rol %q revocado de uid=%s\n", *role, *uid)
	return nil
}

func adminListRoles(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("list-roles", flag.ContinueOnError)
	uid := fs.String("uid", "", "uid del usuario")
	if err := fs.Parse(args); err != nil {
		return err
	}
	roles, err := store.ListRoles(ctx, *uid)
	if err != nil {
		return err
	}
	if len(roles) == 0 {
		fmt.Println("(no roles asignados)")
		return nil
	}
	for _, r := range roles {
		fmt.Println(r)
	}
	return nil
}

func adminSetPassword(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("set-password", flag.ContinueOnError)
	uid := fs.String("uid", "", "uid del usuario")
	password := fs.String("password", "", "nueva password (≥8)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := store.ChangePassword(ctx, *uid, *password); err != nil {
		return err
	}
	fmt.Printf("✓ password actualizada para uid=%s\n", *uid)
	return nil
}

func adminDeactivate(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("deactivate", flag.ContinueOnError)
	uid := fs.String("uid", "", "uid del usuario")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := store.SetActive(ctx, *uid, false); err != nil {
		return err
	}
	fmt.Printf("✓ uid=%s desactivado\n", *uid)
	return nil
}

func adminCreateInvite(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("create-invite", flag.ContinueOnError)
	email := fs.String("email", "", "email del invitado (UNIQUE check al activar)")
	publicURL := fs.String("public-url", "", "URL pública (default: ARIA_CORE_PUBLIC_URL o https://ariacore.itechdev.com.mx)")
	var roles stringSliceFlag
	fs.Var(&roles, "role", "rol del invitado (puede repetirse): admin | dev | cotizador | project_admin")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*email) == "" {
		return fmt.Errorf("--email is required")
	}
	if len(roles) == 0 {
		roles = stringSliceFlag{cloudusers.RoleDev}
	}
	inv, err := store.CreateInvite(ctx, *email, []string(roles), "")
	if err != nil {
		return err
	}
	base := strings.TrimSpace(*publicURL)
	if base == "" {
		base = strings.TrimSpace(os.Getenv("ARIA_CORE_PUBLIC_URL"))
	}
	if base == "" {
		base = "https://ariacore.itechdev.com.mx"
	}
	link := strings.TrimRight(base, "/") + "/dashboard/invite/" + inv.Token
	fmt.Printf("✓ invitación creada\n  email:   %s\n  roles:   %s\n  expires: %s\n  link:    %s\n",
		inv.Email, strings.Join(inv.Roles, ", "), inv.ExpiresAt.UTC().Format("2006-01-02 15:04 UTC"), link)
	return nil
}

// ─── Redactor CLI subcommand ─────────────────────────────────────────────────
//
// `aria-core redactor scan FILE`        -- escanea un archivo y muestra qué
//                                          patrones detecta (sin persistir nada).
// `aria-core redactor stats [--days N]` -- imprime stats de aria_llm_egress_log.
// `aria-core redactor reveal-aliases T` -- expande [TOKEN-XXXX] -> displayValue.
//                                          Admin-only; toca aria_redaction_aliases.

func cmdRedactor() {
	if len(os.Args) < 3 {
		printRedactorUsage()
		exitFunc(2)
		return
	}
	sub := os.Args[2]
	args := os.Args[3:]
	switch sub {
	case "scan":
		redactorScan(args)
	case "stats":
		runRedactorWithDB(args, redactorStats)
	case "reveal-aliases", "reveal":
		runRedactorWithDB(args, redactorReveal)
	case "help", "--help", "-h":
		printRedactorUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown redactor subcommand: %s\n\n", sub)
		printRedactorUsage()
		exitFunc(2)
	}
}

func printRedactorUsage() {
	fmt.Println(`aria-core redactor — bóveda de cliente / PII scrubber

Subcommands:
  scan FILE                     Escanea archivo y reporta qué se redactaría
                                (no requiere DB; solo regex local).
  stats [--days N]              Stats de envíos a LLM externos
                                (lee aria_llm_egress_log; default --days 30).
  reveal-aliases TOKEN          Expande [TOKEN-XXXX] -> displayValue real.
                                Admin-only; consulta aria_redaction_aliases.

Requiere ARIA_CORE_DATABASE_URL para stats / reveal-aliases.`)
}

func runRedactorWithDB(args []string, fn func(ctx context.Context, db *sql.DB, args []string) error) {
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
	if err := fn(context.Background(), db, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

func redactorScan(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: aria-core redactor scan FILE")
		exitFunc(2)
		return
	}
	path := args[0]
	content, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read %s: %v\n", path, err)
		exitFunc(1)
		return
	}
	svc := redactor.New(redactor.Config{}) // no DB needed for scan
	res, err := svc.Scrub(context.Background(), string(content), redactor.ScrubOptions{Mode: redactor.ModeTokens})
	if err != nil {
		fmt.Fprintf(os.Stderr, "scrub: %v\n", err)
		exitFunc(1)
		return
	}
	fmt.Printf("Archivo: %s (%d bytes)\n", path, len(content))
	fmt.Printf("Redacciones detectadas: %d tipos\n\n", len(res.Redactions))
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIPO\tCOUNT\tEJEMPLO")
	for _, r := range res.Redactions {
		example := ""
		if len(r.ReplacedWith) > 0 {
			example = r.ReplacedWith[0]
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\n", r.Type, r.Count, example)
	}
	tw.Flush()
	fmt.Println("\n--- preview output ---")
	preview := res.Output
	if len(preview) > 800 {
		preview = preview[:800] + "..."
	}
	fmt.Println(preview)
}

func redactorStats(ctx context.Context, db *sql.DB, args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	days := fs.Int("days", 30, "ventana de tiempo (default 30)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	st, err := redactor.Stats(ctx, db, *days)
	if err != nil {
		return err
	}
	fmt.Printf("Egress audit (últimos %d días):\n", st.WindowDays)
	fmt.Printf("  total requests:   %d\n", st.TotalRequests)
	fmt.Printf("  scrubbed:         %d\n", st.TotalScrubbed)
	fmt.Printf("  sin scrub:        %d\n", st.TotalBypassed)
	fmt.Printf("  bytes enviados:   %d\n", st.BytesSent)
	if len(st.ByProvider) > 0 {
		fmt.Println("\nPor proveedor:")
		for p, c := range st.ByProvider {
			fmt.Printf("  %-20s %d\n", p, c)
		}
	}
	if len(st.ByReason) > 0 {
		fmt.Println("\nPor razón:")
		for r, c := range st.ByReason {
			label := r
			if label == "" {
				label = "(sin razón)"
			}
			fmt.Printf("  %-30s %d\n", label, c)
		}
	}
	return nil
}

func redactorReveal(ctx context.Context, db *sql.DB, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: aria-core redactor reveal-aliases TOKEN")
	}
	token := args[0]
	entityType, displayValue, err := redactor.RevealAlias(ctx, db, token)
	if err != nil {
		return err
	}
	fmt.Printf("token:        %s\n", token)
	fmt.Printf("entity_type:  %s\n", entityType)
	fmt.Printf("display:      %s\n", displayValue)
	return nil
}

func adminActivate(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("activate", flag.ContinueOnError)
	uid := fs.String("uid", "", "uid del usuario")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := store.SetActive(ctx, *uid, true); err != nil {
		return err
	}
	fmt.Printf("✓ uid=%s activado\n", *uid)
	return nil
}
