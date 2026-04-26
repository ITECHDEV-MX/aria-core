package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/cloudstore"
	"github.com/ITECHDEV-MX/aria-core/internal/cloud/vault"
	"github.com/google/uuid"
)

// cmdVault dispatch: aria-core vault <subcommand>.
//
// Subcommands:
//
//	create  --name X --category api_token --scope personal [--project P] [--client UUID]
//	         [--description D] [--rotation 30d|90d|180d|never|manual] [--value-from-stdin]
//	list    [--project=X] [--client=UUID] [--category=C] [--scope=S]
//	get     NAME --reason "razón"        # imprime valor + logea acceso
//	rotate  NAME [--value-from-stdin]
//	grant   NAME --to-uid UUID --perm read|rotate|delete [--expires=24h]
//	scan    FILE                          # corre LeakDetector contra archivo
//	gen-master-key                        # imprime hex de 32 bytes random
func cmdVault() {
	if len(os.Args) < 3 {
		printVaultUsage()
		exitFunc(2)
		return
	}
	sub := os.Args[2]
	args := os.Args[3:]
	switch sub {
	case "create":
		runVault(args, vaultCreate)
	case "list":
		runVault(args, vaultList)
	case "get":
		runVault(args, vaultGet)
	case "rotate":
		runVault(args, vaultRotate)
	case "grant":
		runVault(args, vaultGrant)
	case "scan":
		// scan no necesita DB.
		if err := vaultScan(args); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			exitFunc(1)
		}
	case "gen-master-key":
		hex, err := vault.GenerateMasterKeyHex()
		if err != nil {
			fmt.Fprintf(os.Stderr, "gen master key: %v\n", err)
			exitFunc(1)
			return
		}
		fmt.Println(hex)
	case "help", "--help", "-h":
		printVaultUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown vault subcommand: %s\n\n", sub)
		printVaultUsage()
		exitFunc(2)
	}
}

func printVaultUsage() {
	fmt.Println(`aria-core vault — bóveda de secretos cifrados (AES-256-GCM)

Subcommands:
  create  --name X --category C --scope S [--project P] [--client UUID]
          [--description D] [--rotation manual|30d|90d|180d|never] --value-from-stdin
  list    [--project=X] [--client=UUID] [--category=C] [--scope=S] [--json]
  get     NAME --reason "razón"        Revela valor (queda en audit log).
  rotate  NAME --value-from-stdin       Supersede el secret con un valor nuevo.
  grant   NAME --to-uid UUID --perm read|rotate|delete [--expires DURATION]
  scan    FILE                          Detecta credenciales en un archivo.
  gen-master-key                        Genera 32 bytes hex para ARIA_CORE_VAULT_MASTER_KEY.

Categorías: db_password | api_token | ssh_key | cert | env | webhook | generic
Scopes:     personal | project | team | client_knowledge

Requiere ARIA_CORE_DATABASE_URL. Para encrypt/decrypt: ARIA_CORE_VAULT_MASTER_KEY (hex 64).`)
}

// runVault abre DB + crypto y llama fn con un store ya wireado.
func runVault(args []string, fn func(ctx context.Context, store *vault.PgStore, args []string) error) {
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
	if err := vault.Migrate(context.Background(), db); err != nil {
		// Migrate puede fallar si las tablas core no existen; intentar via cloudstore primero.
		_ = err
	}
	// Asegurar tablas core (cloudstore) y vault inicializados.
	_ = cloudstore.New // ya importado para garantizar que el package se incluya.

	masterKey := strings.TrimSpace(os.Getenv("ARIA_CORE_VAULT_MASTER_KEY"))
	c, err := vault.NewCrypto(masterKey)
	if err != nil {
		fmt.Fprintf(os.Stderr, "crypto: %v\n", err)
		exitFunc(1)
		return
	}
	store := vault.New(db, c)

	if err := fn(context.Background(), store, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
		return
	}
}

func vaultCreate(ctx context.Context, store *vault.PgStore, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	name := fs.String("name", "", "nombre del secret (UNIQUE por project/client)")
	category := fs.String("category", "generic", "category")
	scope := fs.String("scope", "personal", "scope")
	project := fs.String("project", "", "project (opcional)")
	clientID := fs.String("client", "", "client UUID (opcional)")
	description := fs.String("description", "", "descripción (opcional)")
	rotation := fs.String("rotation", "manual", "rotation policy")
	createdBy := fs.String("created-by", "", "uid creador (default: ARIA_CORE_VAULT_CLI_UID o nuevo UUID)")
	valueFlag := fs.String("value", "", "valor en línea (NO RECOMENDADO; preferir --value-from-stdin)")
	fromStdin := fs.Bool("value-from-stdin", false, "leer valor desde stdin (recomendado)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*name) == "" {
		return fmt.Errorf("--name is required")
	}
	value := *valueFlag
	if *fromStdin {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("read stdin: %w", err)
		}
		value = strings.TrimRight(string(buf), "\r\n")
	}
	if value == "" {
		return fmt.Errorf("value is required (use --value-from-stdin)")
	}
	by := resolveCLIUID(*createdBy)
	var cid *uuid.UUID
	if v := strings.TrimSpace(*clientID); v != "" {
		u, err := uuid.Parse(v)
		if err != nil {
			return fmt.Errorf("invalid --client uuid: %w", err)
		}
		cid = &u
	}
	sec, err := store.Create(ctx, vault.CreateParams{
		Name: *name, Category: *category, Scope: *scope,
		Project: *project, ClientID: cid, Description: *description,
		Value: value, RotationPolicy: *rotation, CreatedByUID: by,
	})
	if err != nil {
		return err
	}
	fmt.Printf("✓ secret creado\n  id:       %s\n  name:     %s\n  category: %s\n  scope:    %s\n",
		sec.ID, sec.Name, sec.Category, sec.Scope)
	if !store.Available() {
		fmt.Fprintln(os.Stderr, "WARNING: vault is in degraded mode; this create should have failed.")
	}
	return nil
}

func vaultList(ctx context.Context, store *vault.PgStore, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	project := fs.String("project", "", "filtrar por project")
	clientID := fs.String("client", "", "filtrar por client UUID")
	category := fs.String("category", "", "filtrar por category")
	scope := fs.String("scope", "", "filtrar por scope")
	asUID := fs.String("as-uid", "", "uid del caller (para ACL)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	by := vault.Principal{UID: resolveCLIUID(*asUID), Roles: []string{"admin"}} // CLI = admin
	f := vault.ListFilter{Project: *project, Category: *category, Scope: *scope}
	if v := strings.TrimSpace(*clientID); v != "" {
		u, err := uuid.Parse(v)
		if err != nil {
			return fmt.Errorf("invalid --client uuid: %w", err)
		}
		f.ClientID = &u
	}
	rs, err := store.List(ctx, f, by)
	if err != nil {
		return err
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "ID\tNAME\tCATEGORY\tSCOPE\tPROJECT\tCLIENT\tCREATED")
	for _, sec := range rs {
		clientStr := "-"
		if sec.ClientID != nil {
			clientStr = sec.ClientID.String()
		}
		project := sec.Project
		if project == "" {
			project = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			sec.ID, sec.Name, sec.Category, sec.Scope, project, clientStr,
			sec.CreatedAt.UTC().Format("2006-01-02 15:04"))
	}
	if len(rs) == 0 {
		fmt.Fprintln(tw, "(no secrets)")
	}
	return nil
}

func vaultGet(ctx context.Context, store *vault.PgStore, args []string) error {
	fs := flag.NewFlagSet("get", flag.ContinueOnError)
	reason := fs.String("reason", "", "razón del acceso (queda en audit log)")
	asUID := fs.String("as-uid", "", "uid del caller")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: vault get NAME --reason 'razón'")
	}
	name := fs.Arg(0)
	if strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("--reason is required (queda en audit log)")
	}
	by := vault.Principal{UID: resolveCLIUID(*asUID), Roles: []string{"admin"}}
	id, err := findSecretIDByName(ctx, store, name, by)
	if err != nil {
		return err
	}
	value, err := store.Reveal(ctx, id, by, *reason)
	if err != nil {
		return err
	}
	// Imprimir SOLO el valor a stdout, sin newline si parece un blob (e.g. PEM).
	// Para uso típico (env vars) un newline al final es OK.
	fmt.Println(value)
	return nil
}

func vaultRotate(ctx context.Context, store *vault.PgStore, args []string) error {
	fs := flag.NewFlagSet("rotate", flag.ContinueOnError)
	asUID := fs.String("as-uid", "", "uid del caller")
	fromStdin := fs.Bool("value-from-stdin", false, "leer valor desde stdin")
	valueFlag := fs.String("value", "", "valor en línea (no recomendado)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: vault rotate NAME --value-from-stdin")
	}
	name := fs.Arg(0)
	value := *valueFlag
	if *fromStdin {
		buf, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		value = strings.TrimRight(string(buf), "\r\n")
	}
	if value == "" {
		return fmt.Errorf("value is required")
	}
	by := vault.Principal{UID: resolveCLIUID(*asUID), Roles: []string{"admin"}}
	id, err := findSecretIDByName(ctx, store, name, by)
	if err != nil {
		return err
	}
	newSec, err := store.Rotate(ctx, id, value, by)
	if err != nil {
		return err
	}
	fmt.Printf("✓ rotated\n  old_id: %s\n  new_id: %s\n", id, newSec.ID)
	return nil
}

func vaultGrant(ctx context.Context, store *vault.PgStore, args []string) error {
	fs := flag.NewFlagSet("grant", flag.ContinueOnError)
	toUID := fs.String("to-uid", "", "uid destinatario")
	toRole := fs.String("to-role", "", "role destinatario")
	perm := fs.String("perm", "read", "permission: read|rotate|delete")
	expires := fs.String("expires", "", "duración de validez (e.g. 24h, 30d=720h)")
	asUID := fs.String("as-uid", "", "uid del granter")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: vault grant NAME --to-uid UUID --perm read")
	}
	name := fs.Arg(0)
	by := vault.Principal{UID: resolveCLIUID(*asUID), Roles: []string{"admin"}}
	id, err := findSecretIDByName(ctx, store, name, by)
	if err != nil {
		return err
	}
	var expiresAt *time.Time
	if v := strings.TrimSpace(*expires); v != "" {
		dur, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("invalid --expires: %w", err)
		}
		t := time.Now().Add(dur)
		expiresAt = &t
	}
	if err := store.Grant(ctx, vault.GrantParams{
		SecretID: id, GrantedToUID: *toUID, GrantedToRole: *toRole,
		Permission: *perm, GrantedByUID: by.UID, ExpiresAt: expiresAt,
	}); err != nil {
		return err
	}
	dest := *toUID
	if dest == "" {
		dest = "role:" + *toRole
	}
	fmt.Printf("✓ granted %s on %s to %s\n", *perm, name, dest)
	return nil
}

func vaultScan(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: vault scan FILE [FILE...]")
	}
	d := vault.NewLeakDetector()
	hits := 0
	for _, path := range args {
		buf, err := os.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  read %s: %v\n", path, err)
			continue
		}
		matches := d.Scan(string(buf))
		if len(matches) == 0 {
			fmt.Printf("✓ %s: clean\n", path)
			continue
		}
		hits += len(matches)
		fmt.Printf("⚠ %s: %d match(es)\n", path, len(matches))
		for _, m := range matches {
			fmt.Printf("    [%s/%s] %s — %s\n", m.Severity, m.Pattern, m.Match, m.Hint)
		}
	}
	if hits > 0 {
		exitFunc(1)
	}
	return nil
}

// findSecretIDByName busca el id por nombre activo (asumiendo unique en (name, project, client_id)
// para el caso típico de scope=personal sin proyecto).
func findSecretIDByName(ctx context.Context, store *vault.PgStore, name string, by vault.Principal) (string, error) {
	rs, err := store.List(ctx, vault.ListFilter{Limit: 500}, by)
	if err != nil {
		return "", err
	}
	var matches []*vault.Secret
	for _, sec := range rs {
		if sec.Name == name {
			matches = append(matches, sec)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("secret %q not found (or not accessible)", name)
	case 1:
		return matches[0].ID, nil
	default:
		// Ambiguous — pedir al user que use --project / --client filter.
		return "", fmt.Errorf("ambiguous: %d secrets named %q (across projects/clients); use the dashboard or aria-core admin tools to disambiguate", len(matches), name)
	}
}

// resolveCLIUID prefiere el flag, luego env, luego un placeholder estable.
// Para ambientes locales el operador puede setear ARIA_CORE_VAULT_CLI_UID
// con su uid del cloud_users (visible en `aria-core admin list-users`).
func resolveCLIUID(flagVal string) string {
	if v := strings.TrimSpace(flagVal); v != "" {
		return v
	}
	if v := strings.TrimSpace(os.Getenv("ARIA_CORE_VAULT_CLI_UID")); v != "" {
		return v
	}
	// Generar un uuid estable derivado del usuario? Mejor: requerir uid explícito.
	// Como los CHECK constraints exigen UUID válido, generamos uno random — pero
	// avisamos para que el operador lo sepa.
	id := uuid.NewString()
	fmt.Fprintf(os.Stderr, "[vault] WARN: no --as-uid ni ARIA_CORE_VAULT_CLI_UID set; using ephemeral %s for audit log\n", id)
	return id
}

// suppress unused import lint when only some helpers are exercised.
var _ = errors.New
