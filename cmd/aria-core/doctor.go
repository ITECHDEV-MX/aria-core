package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ITECHDEV-MX/aria-core/internal/doctor"
	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// cmdDoctor runs read-only diagnostics on the local store.
//
// Usage:
//
//	aria-core doctor          # human-readable output
//	aria-core doctor --json   # machine-readable JSON
//
// Exit codes:
//
//	0 — all checks OK or only informational items
//	1 — at least one warning
//	2 — at least one error
func cmdDoctor(cfg store.Config) {
	jsonOut := false
	for i := 2; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--json", "-j":
			jsonOut = true
		case "-h", "--help":
			fmt.Println("Usage: aria-core doctor [--json]")
			fmt.Println("Read-only operational diagnostics for the local store.")
			return
		}
	}

	s, err := storeNew(cfg)
	if err != nil {
		// Doctor must still produce a useful report when the store can't open.
		// We synthesize a minimal report indicating the open failure.
		report := doctor.Report{
			Version:       version,
			OverallStatus: doctor.StatusError,
			Checks: []doctor.Check{{
				Name:   "store_open",
				Status: doctor.StatusError,
				Detail: fmt.Sprintf("could not open store: %v", err),
			}},
		}
		printReport(report, jsonOut)
		exitFunc(2)
		return
	}
	defer s.Close()

	report := doctor.Diagnose(s.DB(), cfg, version)
	printReport(report, jsonOut)

	switch report.OverallStatus {
	case doctor.StatusError:
		exitFunc(2)
	case doctor.StatusWarn:
		exitFunc(1)
	}
}

func printReport(r doctor.Report, asJSON bool) {
	r = r.SortedByName()
	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(r)
		return
	}

	// Text format
	fmt.Println("ARIA Core Doctor — diagnostic report")
	fmt.Println()
	fmt.Printf("  Status: %s %s\n", statusIcon(r.OverallStatus), r.OverallStatus)
	fmt.Println()

	maxName := 0
	for _, c := range r.Checks {
		if len(c.Name) > maxName {
			maxName = len(c.Name)
		}
	}
	for _, c := range r.Checks {
		fmt.Printf("  %s %-*s  %s\n", statusIcon(c.Status), maxName, c.Name, c.Detail)
	}

	fmt.Println()
	totals := summarize(r.Checks)
	fmt.Printf("  Issues: %d critical, %d warnings, %d informational\n",
		totals[doctor.StatusError], totals[doctor.StatusWarn], totals[doctor.StatusInfo])
	fmt.Printf("  Took: %dms\n", totalDurationMs(r.Checks))
}

func statusIcon(s doctor.Status) string {
	switch s {
	case doctor.StatusOK:
		return "✓"
	case doctor.StatusWarn:
		return "⚠"
	case doctor.StatusError:
		return "✗"
	case doctor.StatusInfo:
		return "ⓘ"
	}
	return "?"
}

func summarize(checks []doctor.Check) map[doctor.Status]int {
	m := map[doctor.Status]int{}
	for _, c := range checks {
		m[c.Status]++
	}
	return m
}

func totalDurationMs(checks []doctor.Check) int64 {
	var total int64
	for _, c := range checks {
		total += c.DurationMs
	}
	return total
}

// silence unused-import build errors if strings ever unused
var _ = strings.TrimSpace
