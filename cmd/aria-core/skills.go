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

Examples:
  aria-core skills validate
  aria-core skills validate ./skills --strict
  aria-core skills validate /repo/skills --json | jq .findings`)
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
