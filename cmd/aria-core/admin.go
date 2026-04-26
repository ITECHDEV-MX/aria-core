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
