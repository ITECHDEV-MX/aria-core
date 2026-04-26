package main

import (
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/roi"
)

// cmdROI dispatch: aria-core roi <subcommand>.
//
// Subcommands:
//
//	report             [--days=30] [--dev=uid] [--project=X] [--format=table|csv|json]
//	calculate-savings  --dev=uid --since=YYYY-MM-DD [--until=YYYY-MM-DD] [--format=...]
func cmdROI() {
	if len(os.Args) < 3 {
		printROIUsage()
		exitFunc(2)
		return
	}
	sub := os.Args[2]
	args := os.Args[3:]
	switch sub {
	case "report":
		runROI(args, roiReport)
	case "calculate-savings":
		runROI(args, roiCalculateSavings)
	case "help", "--help", "-h":
		printROIUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown roi subcommand: %s\n\n", sub)
		printROIUsage()
		exitFunc(2)
	}
}

func printROIUsage() {
	fmt.Println(`aria-core roi — métricas de Return-On-Investment de ARIA Core

Subcommands:
  report             [--days=30] [--dev=uid] [--project=X] [--format=table|csv|json]
                      Imprime TTC/RDR/CWR/SVR/DTT + savings consolidado.
  calculate-savings  --dev=uid --since=YYYY-MM-DD [--until=YYYY-MM-DD] [--format=...]
                      Devuelve breakdown por pillar para una ventana específica.

Env:
  ARIA_CORE_DATABASE_URL              postgres://... (requerido)
  ARIA_CORE_DEV_COST_MXN_PER_HOUR     default 250 MXN/h (~4.17 MXN/min)`)
}

// runROI abre DB y delega a fn con un MetricsStore wireado.
func runROI(args []string, fn func(ctx context.Context, store *roi.MetricsStore, args []string) error) {
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
	// Migrate idempotente (asegura aria_search_log existe).
	if err := roi.Migrate(context.Background(), db); err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		exitFunc(1)
		return
	}
	store := roi.NewMetricsStore(db)
	if err := fn(context.Background(), store, args); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		exitFunc(1)
	}
}

// roiReport imprime el resumen ROI consolidado.
func roiReport(ctx context.Context, store *roi.MetricsStore, args []string) error {
	fs := flag.NewFlagSet("roi report", flag.ContinueOnError)
	days := fs.Int("days", 30, "ventana en días")
	dev := fs.String("dev", "", "developer_uid (UUID); vacío = global")
	_ = fs.String("project", "", "filtrar por proyecto (no implementado en métricas, planeado)")
	format := fs.String("format", "table", "table|csv|json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	until := time.Now().UTC()
	since := until.AddDate(0, 0, -*days)
	view, err := store.CalculateSavings(ctx, *dev, since, until)
	if err != nil {
		return err
	}
	return printROIView(view, *format, since, until)
}

// roiCalculateSavings es el cálculo explícito con --since/--until.
func roiCalculateSavings(ctx context.Context, store *roi.MetricsStore, args []string) error {
	fs := flag.NewFlagSet("roi calculate-savings", flag.ContinueOnError)
	dev := fs.String("dev", "", "developer_uid (UUID); vacío = global")
	sinceRaw := fs.String("since", "", "YYYY-MM-DD (requerido)")
	untilRaw := fs.String("until", "", "YYYY-MM-DD (default: now)")
	format := fs.String("format", "table", "table|csv|json")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*sinceRaw) == "" {
		return fmt.Errorf("--since YYYY-MM-DD is required")
	}
	since, err := time.Parse("2006-01-02", *sinceRaw)
	if err != nil {
		return fmt.Errorf("--since: %w", err)
	}
	until := time.Now().UTC()
	if strings.TrimSpace(*untilRaw) != "" {
		until, err = time.Parse("2006-01-02", *untilRaw)
		if err != nil {
			return fmt.Errorf("--until: %w", err)
		}
	}
	view, err := store.CalculateSavings(ctx, *dev, since.UTC(), until.UTC())
	if err != nil {
		return err
	}
	return printROIView(view, *format, since.UTC(), until.UTC())
}

func printROIView(view *roi.SavingsView, format string, since, until time.Time) error {
	switch format {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(view)
	case "csv":
		cw := csv.NewWriter(os.Stdout)
		_ = cw.Write([]string{"key", "value"})
		_ = cw.Write([]string{"window_days", fmt.Sprintf("%d", view.WindowDays)})
		_ = cw.Write([]string{"saved_minutes", fmt.Sprintf("%.2f", view.TotalSavedMinutes)})
		_ = cw.Write([]string{"saved_mxn", fmt.Sprintf("%.2f", view.TotalSavedMXN)})
		_ = cw.Write([]string{"workday_pct_saved", fmt.Sprintf("%.4f", view.WorkdayPctSaved)})
		_ = cw.Write([]string{"ttc_min", fmt.Sprintf("%.2f", view.TTC)})
		_ = cw.Write([]string{"rdr", fmt.Sprintf("%.4f", view.RDR)})
		_ = cw.Write([]string{"cwr", fmt.Sprintf("%.4f", view.CWR)})
		_ = cw.Write([]string{"svr", fmt.Sprintf("%.4f", view.SVR)})
		_ = cw.Write([]string{"dtt_min", fmt.Sprintf("%.2f", view.DTT)})
		for k, v := range view.ByPillar {
			_ = cw.Write([]string{"pillar_" + k + "_min", fmt.Sprintf("%.2f", v)})
		}
		cw.Flush()
		return cw.Error()
	default: // table
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintf(w, "ARIA Core ROI · %s → %s (%d días)\n",
			since.Format("2006-01-02"), until.Format("2006-01-02"), view.WindowDays)
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "Saved minutes:\t%.1f min (%.1f h)\n", view.TotalSavedMinutes, view.TotalSavedMinutes/60)
		fmt.Fprintf(w, "Saved MXN:\t$%.2f\n", view.TotalSavedMXN)
		fmt.Fprintf(w, "Workday %%:\t%.2f%% of 8h\n", view.WorkdayPctSaved*100)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Métricas crudas:")
		fmt.Fprintf(w, "  TTC (Time To Context):\t%.1f min\n", view.TTC)
		fmt.Fprintf(w, "  RDR (Re-Discovery Rate):\t%.2f%%\n", view.RDR*100)
		fmt.Fprintf(w, "  CWR (Cred Without Reveal):\t%.2f%%\n", view.CWR*100)
		fmt.Fprintf(w, "  SVR (Skill Validation):\t%.2f%%\n", view.SVR*100)
		fmt.Fprintf(w, "  DTT (Deploy Total Time):\t%.1f min\n", view.DTT)
		fmt.Fprintln(w, "")
		fmt.Fprintln(w, "Breakdown por pillar (minutos):")
		for k, v := range view.ByPillar {
			fmt.Fprintf(w, "  %s:\t%.1f min ($%.2f MXN)\n", k, v, view.ByPillarMXN[k])
		}
		fmt.Fprintln(w, "")
		fmt.Fprintf(w, "vs período anterior:\t%+.1f min (%+.1f%% delta)\n",
			view.Compared.DeltaMinutes, view.Compared.DeltaPct*100)
		return w.Flush()
	}
}
