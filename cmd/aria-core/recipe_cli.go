package main

import (
	"bufio"
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

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/recipes"
)

// cmdRecipe — CLI dispatch for `aria-core recipe <subcommand>`.
//
//	list     Lista recipes con executable badge + success rate.
//	show     Muestra los steps de un recipe por key.
//	run      Ejecuta un recipe sincrónicamente con --var key=value.
//	history  Lista últimas N ejecuciones (filtrable por --key, --days).
//	seed     Carga las 3 recipes builtin (deploy / backup / onboard).
func cmdRecipe() {
	if len(os.Args) < 3 {
		printRecipeUsage()
		exitFunc(2)
		return
	}
	sub := os.Args[2]
	args := os.Args[3:]
	switch sub {
	case "list":
		runRecipeCmd(args, recipeList)
	case "show":
		runRecipeCmd(args, recipeShow)
	case "run":
		runRecipeCmd(args, recipeRun)
	case "history":
		runRecipeCmd(args, recipeHistory)
	case "seed":
		runRecipeCmd(args, recipeSeed)
	case "help", "--help", "-h":
		printRecipeUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown recipe subcommand: %s\n\n", sub)
		printRecipeUsage()
		exitFunc(2)
	}
}

func printRecipeUsage() {
	fmt.Println(`aria-core recipe — executable recipe runner

Subcommands:
  list                              Lista recipes con executable badge + success rate.
  show <key>                        Muestra los steps de un recipe.
  run <key> [--var key=value...] [--dry-run] [--project NAME]
                                    Ejecuta el recipe sincrónicamente.
  history [--key=X] [--days=30] [--limit=50]
                                    Últimas ejecuciones.
  seed                              Carga 3 recipes builtin (idempotente).

Requiere ARIA_CORE_DATABASE_URL=postgres://...

Variables se pasan a step templates como {{ .Vars.key }} y al previo step
como {{ .Prev.Stdout }} (ver internal/cloud/recipes/templates.go).`)
}

// runRecipeCmd opens a DB connection (with vault crypto if available) and
// hands a Runner to the subcommand handler.
func runRecipeCmd(args []string, fn func(ctx context.Context, r *recipes.Runner, args []string) error) {
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
	store := recipes.NewPgStore(db)
	r := recipes.NewRunner(store, recipes.WithPauseHandler(ttyPauseHandler{}))
	if err := fn(context.Background(), r, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

func recipeList(ctx context.Context, r *recipes.Runner, args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	executableOnly := fs.Bool("executable", false, "solo recipes con executable=true")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rs, err := r.ListRecipes(ctx, *executableOnly)
	if err != nil {
		return err
	}
	since := time.Now().UTC().Add(-30 * 24 * time.Hour)
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "KEY\tEXEC\tSTEPS\tSUCCESS RATE (30d)\tAVG\tPATTERN")
	for _, rec := range rs {
		stats, _ := r.Stats(ctx, rec.Key, since)
		rate := "—"
		if stats.TotalRuns > 0 {
			rate = fmt.Sprintf("%.0f%% (%d)", stats.SuccessRate*100, stats.TotalRuns)
		}
		avg := "—"
		if stats.AvgDurationMs > 0 {
			avg = fmt.Sprintf("%.1fs", float64(stats.AvgDurationMs)/1000.0)
		}
		execStr := "manual"
		if rec.Executable {
			execStr = "yes"
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\n", rec.Key, execStr, len(rec.Steps), rate, avg, rec.TaskPattern)
	}
	if len(rs) == 0 {
		fmt.Fprintln(tw, "(no recipes — corré `aria-core recipe seed`)")
	}
	return nil
}

func recipeShow(ctx context.Context, r *recipes.Runner, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: recipe show <key>")
	}
	rec, err := r.GetRecipe(ctx, args[0])
	if err != nil {
		return err
	}
	fmt.Printf("Recipe: %s\nID: %s\nPattern: %s\nStack: %v\nExecutable: %v\nExpected duration: %ds\nSteps:\n",
		rec.Key, rec.ID, rec.TaskPattern, rec.Stack, rec.Executable, rec.ExpectedDurationSeconds)
	for i, s := range rec.Steps {
		fmt.Printf("  [%d] %s — %s\n", i, s.Kind, s.Label)
		switch s.Kind {
		case recipes.StepShell:
			fmt.Printf("      cmd: %s\n", s.Command)
		case recipes.StepHTTP:
			fmt.Printf("      %s %s (expect %d)\n", s.Method, s.URL, s.ExpectedStatus)
		case recipes.StepAriaSave:
			if s.Save != nil {
				fmt.Printf("      title: %s | topic: %s\n", s.Save.Title, s.Save.TopicKey)
			}
		case recipes.StepVaultUse:
			if s.VaultUse != nil {
				fmt.Printf("      cmd: %s | secrets: %v\n", s.VaultUse.Command, s.VaultUse.SecretNames)
			}
		case recipes.StepPause:
			fmt.Printf("      prompt: %s\n", s.PausePrompt)
		}
	}
	return nil
}

func recipeRun(ctx context.Context, r *recipes.Runner, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	dryRun := fs.Bool("dry-run", false, "no ejecutar; solo retornar plan")
	project := fs.String("project", "", "project para telemetría")
	uid := fs.String("as-uid", "", "uid del caller")
	var varList multiFlag
	fs.Var(&varList, "var", "variable key=value (puede repetirse)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: recipe run <key> [--var key=value ...] [--dry-run]")
	}
	key := fs.Arg(0)
	vars := make(map[string]string, len(varList))
	for _, kv := range varList {
		idx := strings.IndexByte(kv, '=')
		if idx <= 0 {
			return fmt.Errorf("--var requires key=value: got %q", kv)
		}
		vars[kv[:idx]] = kv[idx+1:]
	}
	exec, err := r.Execute(ctx, recipes.ExecuteParams{
		RecipeKey:     key,
		Vars:          vars,
		Project:       *project,
		ExecutedByUID: *uid,
		DryRun:        *dryRun,
	})
	if err != nil {
		return err
	}
	fmt.Printf("Execution: %s\nStatus: %s\nDuration: %.1fs\nSteps: %d/%d\n",
		exec.ID, exec.Status, float64(exec.TotalDurationMs)/1000.0, exec.CompletedSteps, exec.TotalSteps)
	for _, s := range exec.Steps {
		fmt.Printf("  [%d] %s — %s — %s — %.1fs\n", s.Index, s.Kind, s.Label, s.Status, float64(s.Duration/time.Millisecond)/1000.0)
		if s.Stderr != "" {
			fmt.Printf("      stderr: %s\n", truncateForCLI(s.Stderr))
		}
	}
	if exec.Status == recipes.StatusFailed {
		exitFunc(1)
	}
	return nil
}

func recipeHistory(ctx context.Context, r *recipes.Runner, args []string) error {
	fs := flag.NewFlagSet("history", flag.ContinueOnError)
	key := fs.String("key", "", "filter by recipe key")
	days := fs.Int("days", 30, "ventana en días")
	limit := fs.Int("limit", 50, "máximo")
	asJSON := fs.Bool("json", false, "salida JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	filter := recipes.ExecFilter{RecipeKey: *key}
	if *days > 0 {
		t := time.Now().UTC().Add(-time.Duration(*days) * 24 * time.Hour)
		filter.Since = &t
	}
	rs, err := r.ListExecutions(ctx, filter, *limit)
	if err != nil {
		return err
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rs, "", "  ")
		fmt.Println(string(b))
		return nil
	}
	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	defer tw.Flush()
	fmt.Fprintln(tw, "ID\tKEY\tSTATUS\tSTEPS\tDURATION\tSTARTED\tUID")
	for _, e := range rs {
		uid := e.ExecutedByUID
		if len(uid) > 8 {
			uid = uid[:8] + "…"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d/%d\t%.1fs\t%s\t%s\n",
			e.ID[:8]+"…", e.RecipeKey, e.Status, e.CompletedSteps, e.TotalSteps,
			float64(e.TotalDurationMs)/1000.0,
			e.StartedAt.Format("2006-01-02 15:04"), uid,
		)
	}
	if len(rs) == 0 {
		fmt.Fprintln(tw, "(no executions)")
	}
	return nil
}

func recipeSeed(ctx context.Context, r *recipes.Runner, args []string) error {
	if err := r.SeedBuiltins(ctx); err != nil {
		return err
	}
	fmt.Println("✓ seeded 3 builtin recipes (idempotent):")
	for _, b := range recipes.BuiltinRecipes() {
		fmt.Printf("  - %s — %s\n", b.Key, b.TaskPattern)
	}
	return nil
}

// === helpers ===

// multiFlag implements flag.Value for repeated --var key=value flags.
type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// ttyPauseHandler asks the operator at the TTY. Used by `recipe run`.
type ttyPauseHandler struct{}

func (ttyPauseHandler) HandlePause(ctx context.Context, prompt string) (bool, string, error) {
	fmt.Printf("[pause] %s\n[Y/n] > ", prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false, "no input", nil
	}
	answer := strings.TrimSpace(scanner.Text())
	if answer == "" || strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
		return true, "", nil
	}
	return false, answer, nil
}

func truncateForCLI(s string) string {
	const maxLen = 200
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
