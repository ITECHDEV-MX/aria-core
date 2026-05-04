package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ITECHDEV-MX/aria-core/internal/skills"
)

// cmdSkills dispatches "aria-core skills <subcommand>".
//
// Subcommands:
//
//	validate [path]   Run the SKILL.md validator. Default path: ./skills.
//	                  Flags: --strict (errors fail with exit 1), --json (machine output).
//
// Future (F1.b):
//
//	maintainer add|list|revoke
//	lock              (write skills/MANIFEST.yaml content hashes)
//	check-drift       (compare local vs cloud canonical)
func cmdSkills() {
	if len(os.Args) < 3 {
		printSkillsUsage()
		exitFunc(1)
		return
	}

	switch os.Args[2] {
	case "validate":
		cmdSkillsValidate()
	case "lock":
		cmdSkillsLock()
	case "check-drift":
		cmdSkillsCheckDrift()
	case "versions":
		cmdSkillsVersions()
	case "-h", "--help":
		printSkillsUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand: %s\n", os.Args[2])
		printSkillsUsage()
		exitFunc(1)
	}
}

func printSkillsUsage() {
	fmt.Println(`Usage: aria-core skills <subcommand>

Subcommands:
  validate [path]   Validate SKILL.md files against the canonical schema.
                    Path defaults to ./skills. Flags:
                      --strict    Exit 1 on errors (default: warn-only).
                      --json      Emit machine-readable JSON output.

  lock [path]       Generate skills/MANIFEST.yaml with content hashes.
                    Path defaults to ./skills.

  check-drift [path] [--json]
                    Compare working tree against MANIFEST.yaml. Exit 1 on
                    drift.

  versions [path]   Auto-generate skills/VERSIONS.md from current
                    SKILL.md frontmatter. Path defaults to ./skills.

Examples:
  aria-core skills validate
  aria-core skills lock
  aria-core skills check-drift --json | jq .entries`)
}

func cmdSkillsValidate() {
	path := "./skills"
	strict := false
	asJSON := false

	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--strict":
			strict = true
		case "--json":
			asJSON = true
		case "-h", "--help":
			printSkillsUsage()
			return
		default:
			if !startsWith(os.Args[i], "-") {
				path = os.Args[i]
			}
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
		exitFunc(2)
		return
	}

	res, err := skills.ValidateDir(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "validate: %v\n", err)
		exitFunc(2)
		return
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(res)
	} else {
		fmt.Println(res.Pretty())
	}

	if res.HasErrors() {
		if strict {
			exitFunc(1)
			return
		}
		// Soft mode: errors print but don't fail the command. This is
		// what local pre-commit uses so devs aren't blocked mid-edit.
		fmt.Fprintln(os.Stderr, "(soft mode: errors did not fail the command — re-run with --strict to enforce)")
	}
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// cmdSkillsLock writes/regenerates skills/MANIFEST.yaml with current
// SHA-256 content hashes. Caller usually commits the result.
func cmdSkillsLock() {
	path := "./skills"
	for i := 3; i < len(os.Args); i++ {
		if !startsWith(os.Args[i], "-") {
			path = os.Args[i]
			break
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
		exitFunc(2)
		return
	}

	lf, err := skills.BuildLockfile(abs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build lockfile: %v\n", err)
		exitFunc(2)
		return
	}
	if err := skills.WriteLockfile(abs, lf); err != nil {
		fmt.Fprintf(os.Stderr, "write lockfile: %v\n", err)
		exitFunc(2)
		return
	}
	fmt.Printf("✓ Wrote %s/%s with %d skill(s)\n", abs, skills.LockfileName, len(lf.Skills))
}

// cmdSkillsCheckDrift compares the working tree to MANIFEST.yaml.
// Exits 0 if no drift, 1 if drift, 2 on I/O failure.
func cmdSkillsCheckDrift() {
	path := "./skills"
	asJSON := false
	for i := 3; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "--json":
			asJSON = true
		case "-h", "--help":
			printSkillsUsage()
			return
		default:
			if !startsWith(os.Args[i], "-") {
				path = os.Args[i]
			}
		}
	}

	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
		exitFunc(2)
		return
	}

	report, err := skills.CheckDrift(abs)
	if err != nil {
		if err == skills.ErrLockfileMissing {
			fmt.Fprintf(os.Stderr, "no lockfile found at %s/%s — run `aria-core skills lock` first\n", abs, skills.LockfileName)
			exitFunc(1)
			return
		}
		fmt.Fprintf(os.Stderr, "drift check: %v\n", err)
		exitFunc(2)
		return
	}

	if asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(report)
	} else {
		if !report.HasErrors {
			fmt.Printf("✓ No drift across %d skill(s).\n", report.Skills)
		} else {
			fmt.Printf("✗ Drift detected across %d skill(s):\n\n", report.Skills)
			for _, e := range report.Entries {
				fmt.Printf("  [%s] %s — %s\n", e.Kind, e.Name, e.Note)
			}
			fmt.Println()
			fmt.Println("Run `aria-core skills lock` to regenerate the lockfile after intentional changes.")
		}
	}

	if report.HasErrors {
		exitFunc(1)
	}
}


// cmdSkillsVersions writes/regenerates skills/VERSIONS.md based on
// the current per-skill frontmatter. Read-only against SKILL.md
// files; only writes the index.
func cmdSkillsVersions() {
	path := "./skills"
	for i := 3; i < len(os.Args); i++ {
		if !startsWith(os.Args[i], "-") {
			path = os.Args[i]
			break
		}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "resolve path: %v\n", err)
		exitFunc(2)
		return
	}
	if err := skills.WriteVersionsFile(abs); err != nil {
		fmt.Fprintf(os.Stderr, "write versions: %v\n", err)
		exitFunc(2)
		return
	}
	fmt.Printf("✓ Wrote %s/%s\n", abs, skills.VersionsFile)
}
