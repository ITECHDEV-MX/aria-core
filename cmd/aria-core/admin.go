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
	case "set-role":
		runAdmin(args, adminSetRole)
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
  create-user   --email E --name N --role admin|dev --password P
  list-users
  set-role      --uid UUID --role admin|dev
  set-password  --uid UUID --password P
  activate      --uid UUID
  deactivate    --uid UUID

Requiere ARIA_CORE_DATABASE_URL apuntando al Postgres cloud.
Ejecutar en el VPS donde corre el server, o cualquier máquina con red al Postgres.`)
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

func adminCreateUser(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("create-user", flag.ContinueOnError)
	email := fs.String("email", "", "email del usuario (UNIQUE)")
	name := fs.String("name", "", "nombre")
	role := fs.String("role", "dev", "rol: admin | dev")
	password := fs.String("password", "", "password (≥8 caracteres)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	u, err := store.Create(ctx, *email, *name, *role, *password)
	if err != nil {
		return err
	}
	fmt.Printf("✓ usuario creado\n  uid:   %s\n  email: %s\n  role:  %s\n", u.UID, u.Email, u.Role)
	return nil
}

func adminListUsers(ctx context.Context, store *cloudusers.Store, _ []string) error {
	users, err := store.List(ctx)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "UID\tEMAIL\tNAME\tROLE\tACTIVE\tCREATED")
	for _, u := range users {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%v\t%s\n", u.UID, u.Email, u.Name, u.Role, u.IsActive, u.CreatedAt.UTC().Format("2006-01-02 15:04"))
	}
	return nil
}

func adminSetRole(ctx context.Context, store *cloudusers.Store, args []string) error {
	fs := flag.NewFlagSet("set-role", flag.ContinueOnError)
	uid := fs.String("uid", "", "uid del usuario")
	role := fs.String("role", "", "nuevo rol: admin | dev")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if err := store.SetRole(ctx, *uid, *role); err != nil {
		return err
	}
	fmt.Printf("✓ role=%s para uid=%s\n", *role, *uid)
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
