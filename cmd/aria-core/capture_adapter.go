package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ITECHDEV-MX/aria-core/internal/cloud/capture"
	"github.com/ITECHDEV-MX/aria-core/internal/store"
)

// cmdHooks implements `aria-core hooks <subcommand>`.
//
// Subcommands:
//
//	install     — write managed git hooks into the target repo (or globally)
//	uninstall   — remove managed hooks; leave foreign hooks intact
//	help        — print usage
//
// Flags (install/uninstall):
//
//	--repo=PATH   target git repo (default: cwd)
//	--global      install into ~/.git-hooks-global and configure core.hooksPath
//	--force       overwrite foreign hooks during install
func cmdHooks(_ store.Config) {
	if len(os.Args) < 3 {
		printHooksUsage()
		exitFunc(1)
	}
	sub := os.Args[2]
	switch sub {
	case "install":
		runHooksInstall(false)
	case "uninstall":
		runHooksInstall(true)
	case "help", "--help", "-h":
		printHooksUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown hooks subcommand: %s\n\n", sub)
		printHooksUsage()
		exitFunc(1)
	}
}

func runHooksInstall(uninstall bool) {
	opts := capture.InstallOptions{}
	for i := 3; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case strings.HasPrefix(arg, "--repo="):
			opts.RepoDir = strings.TrimPrefix(arg, "--repo=")
		case arg == "--repo":
			if i+1 < len(os.Args) {
				opts.RepoDir = os.Args[i+1]
				i++
			}
		case arg == "--global":
			opts.Global = true
		case arg == "--force":
			opts.Force = true
		case arg == "--uninstall":
			uninstall = true
		}
	}

	var (
		res *capture.InstallResult
		err error
	)
	if uninstall {
		res, err = capture.Uninstall(opts)
	} else {
		res, err = capture.Install(opts)
	}
	if err != nil {
		fatal(err)
	}

	verb := "installed"
	if uninstall {
		verb = "removed"
	}
	fmt.Printf("aria-core hooks: %s %d hook(s) at %s\n", verb, len(res.Installed), res.HooksDir)
	for _, name := range res.Installed {
		fmt.Printf("  ✓ %s\n", name)
	}
	for _, name := range res.Skipped {
		fmt.Printf("  · %s (skipped)\n", name)
	}
	if warn := capture.PlatformWarning(); warn != "" {
		fmt.Fprintln(os.Stderr, warn)
	}
	if res.Global && !uninstall {
		fmt.Println("\nGlobal hooksPath configured. Override per-repo with: git config --local --unset core.hooksPath")
	}
}

func printHooksUsage() {
	fmt.Print(`aria-core hooks — install/uninstall git hooks for ARIA passive capture

Usage:
  aria-core hooks install   [--repo=PATH] [--global] [--force]
  aria-core hooks uninstall [--repo=PATH] [--global]

Hooks installed:
  prepare-commit-msg  — suggest a conventional-commit title from the staged diff
  post-commit         — capture substantive commits as ARIA observations (5s timeout)
  post-merge          — auto-summarize merged branches into ARIA

Notes:
  · Foreign hooks are preserved unless --force is passed.
  · --global writes to ~/.git-hooks-global and configures git core.hooksPath.
`)
}

// cmdHooksHelper implements `aria-core hooks-helper <subcommand>` — the
// internal entry point invoked by the bash hook scripts. It runs in the
// foreground but applies a hard timeout so a slow store never blocks a commit.
func cmdHooksHelper(cfg store.Config) {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: aria-core hooks-helper <prepare-commit-msg|post-commit|post-merge|auto-capture> [flags]")
		exitFunc(1)
	}
	sub := os.Args[2]
	switch sub {
	case "prepare-commit-msg":
		runHelperPrepareCommitMsg()
	case "post-commit":
		runHelperPostCommit(cfg)
	case "post-merge":
		runHelperPostMerge(cfg)
	case "auto-capture":
		// Generic alias for arbitrary scripts: equivalent to post-commit but
		// tolerates missing diff/message values. Keeps the public CLI stable
		// for outside scripts that want to push observations.
		runHelperPostCommit(cfg)
	default:
		fmt.Fprintf(os.Stderr, "unknown hooks-helper subcommand: %s\n", sub)
		exitFunc(1)
	}
}

type helperFlags struct {
	commit    string
	message   string
	diffStat  string
	rangeRef  string
	summary   string
	timeout   time.Duration
	project   string
}

func parseHelperFlags() helperFlags {
	f := helperFlags{timeout: 5 * time.Second}
	for i := 3; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case strings.HasPrefix(arg, "--commit="):
			f.commit = strings.TrimPrefix(arg, "--commit=")
		case strings.HasPrefix(arg, "--message="):
			f.message = strings.TrimPrefix(arg, "--message=")
		case strings.HasPrefix(arg, "--diff-stat="):
			f.diffStat = strings.TrimPrefix(arg, "--diff-stat=")
		case strings.HasPrefix(arg, "--range="):
			f.rangeRef = strings.TrimPrefix(arg, "--range=")
		case strings.HasPrefix(arg, "--summary="):
			f.summary = strings.TrimPrefix(arg, "--summary=")
		case strings.HasPrefix(arg, "--project="):
			f.project = strings.TrimPrefix(arg, "--project=")
		case strings.HasPrefix(arg, "--timeout="):
			raw := strings.TrimPrefix(arg, "--timeout=")
			if d, err := time.ParseDuration(raw); err == nil {
				f.timeout = d
			} else if d, err := time.ParseDuration(raw + "s"); err == nil {
				f.timeout = d
			}
		}
	}
	return f
}

// runHelperPrepareCommitMsg prints a suggested commit message line to stdout.
// It must be silent on error so the bash wrapper doesn't pollute the user's
// editor with junk.
func runHelperPrepareCommitMsg() {
	f := parseHelperFlags()
	sug := capture.Suggest("", f.diffStat)
	if sug.Skip {
		return
	}
	// We don't have a real title yet; emit a templated stub so the user sees
	// it as a starting point and overwrites it.
	prefix := "chore"
	switch sug.Type {
	case capture.TypeDecision:
		prefix = "feat"
	case capture.TypeLearning:
		prefix = "fix"
	case capture.TypeTechDebt:
		prefix = "refactor"
	}
	fmt.Printf("%s: ", prefix)
}

func runHelperPostCommit(cfg store.Config) {
	f := parseHelperFlags()
	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		processCommitObservation(cfg, f, "post-commit")
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// Silent: hooks must never block the user.
	}
}

func runHelperPostMerge(cfg store.Config) {
	f := parseHelperFlags()
	ctx, cancel := context.WithTimeout(context.Background(), f.timeout)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Treat the merge summary as a commit-style observation.
		mergeFlags := f
		mergeFlags.message = f.summary
		mergeFlags.commit = f.rangeRef
		processCommitObservation(cfg, mergeFlags, "post-merge")
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func processCommitObservation(cfg store.Config, f helperFlags, source string) {
	sug := capture.Suggest(f.message, f.diffStat)
	if sug.Skip {
		return
	}

	project := f.project
	if project == "" {
		cwd, _ := os.Getwd()
		project = detectProject(cwd)
	}

	s, err := storeNew(cfg)
	if err != nil {
		// Hooks are best-effort; fail silently.
		return
	}
	defer s.Close()

	sessionID := source + "-" + truncate(f.commit, 12)
	if sessionID == source+"-" {
		sessionID = source + "-anon"
	}
	cwd, _ := os.Getwd()
	s.CreateSession(sessionID, project, cwd)

	body := strings.TrimSpace(f.message)
	if f.diffStat != "" {
		body = body + "\n\n--- diff ---\n" + f.diffStat
	}

	topicKey := ""
	if f.commit != "" {
		topicKey = source + "-" + truncate(f.commit, 12)
	}

	id, err := storeAddObservation(s, store.AddObservationParams{
		SessionID: sessionID,
		Type:      sug.Type,
		Title:     sug.Title,
		Content:   body,
		Project:   project,
		Scope:     sug.Scope,
		TopicKey:  topicKey,
		ToolName:  source,
	})
	if err != nil {
		return
	}
	fmt.Fprintf(os.Stdout, "✓ ARIA: observation guardada (id=%d)\n", id)
}

// cmdCI implements `aria-core ci <subcommand>` — the GitHub Action helper.
//
//	capture  --workflow=NAME --run=ID --repo=OWNER/REPO [--log-file=PATH]
//
// The log content is read from --log-file, or stdin if no flag is provided.
// This keeps the action template simple: pipe `gh run view --log` into the
// command. Network-bound match/save logic is intentionally local-only in
// this wave: it stores observations in the local store so they propagate
// via the existing autosync pipeline.
func cmdCI(cfg store.Config) {
	if len(os.Args) < 3 {
		printCIUsage()
		exitFunc(1)
	}
	switch os.Args[2] {
	case "capture":
		runCICapture(cfg)
	case "help", "--help", "-h":
		printCIUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown ci subcommand: %s\n", os.Args[2])
		printCIUsage()
		exitFunc(1)
	}
}

func runCICapture(cfg store.Config) {
	req := capture.CICaptureRequest{}
	logFile := ""
	for i := 3; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case strings.HasPrefix(arg, "--workflow="):
			req.Workflow = strings.TrimPrefix(arg, "--workflow=")
		case strings.HasPrefix(arg, "--run="):
			req.RunID = strings.TrimPrefix(arg, "--run=")
		case strings.HasPrefix(arg, "--repo="):
			req.Repo = strings.TrimPrefix(arg, "--repo=")
		case strings.HasPrefix(arg, "--branch="):
			req.Branch = strings.TrimPrefix(arg, "--branch=")
		case strings.HasPrefix(arg, "--commit="):
			req.Commit = strings.TrimPrefix(arg, "--commit=")
		case strings.HasPrefix(arg, "--log-file="):
			logFile = strings.TrimPrefix(arg, "--log-file=")
		}
	}

	var reader io.Reader = os.Stdin
	if logFile != "" {
		f, err := os.Open(logFile)
		if err != nil {
			fatal(err)
		}
		defer f.Close()
		reader = f
	}

	errs := capture.ParseCILog(reader)
	if len(errs) == 0 {
		fmt.Println("aria-core ci: no errors detected in log")
		return
	}

	outcomes := capture.BuildOutcomes(req, errs)

	s, err := storeNew(cfg)
	if err != nil {
		fatal(err)
	}
	defer s.Close()

	project := req.Repo
	if project == "" {
		cwd, _ := os.Getwd()
		project = detectProject(cwd)
	}

	sessionID := "ci-" + req.RunID
	if sessionID == "ci-" {
		sessionID = "ci-anon"
	}
	cwd, _ := os.Getwd()
	s.CreateSession(sessionID, project, cwd)

	saved := 0
	for _, oc := range outcomes {
		title := oc.Error.Title
		if title == "" {
			title = "CI error: " + oc.Error.Signature
		}
		content := oc.Comment + "\n\n```\n" + oc.Error.Snippet + "\n```"
		_, err := storeAddObservation(s, store.AddObservationParams{
			SessionID: sessionID,
			Type:      capture.TypeRisk,
			Title:     title,
			Content:   content,
			Project:   project,
			Scope:     capture.ScopeTeam,
			TopicKey:  oc.TopicKey,
			ToolName:  "ci-capture",
		})
		if err == nil {
			saved++
		}
	}
	fmt.Printf("aria-core ci: parsed %d errors, saved %d observation(s)\n", len(errs), saved)
}

func printCIUsage() {
	fmt.Print(`aria-core ci — GitHub Actions integration for passive capture

Usage:
  aria-core ci capture --workflow=NAME --run=ID --repo=OWNER/REPO \
    [--branch=BRANCH] [--commit=SHA] [--log-file=PATH]

Reads the CI log from --log-file, or stdin if not provided. For every
detected error signature an aria_observation of type=risk, scope=team is
recorded against the project (default: --repo).
`)
}

// cmdDaemon implements `aria-core daemon <subcommand>`.
//
//	start  [--bind=HOST:PORT] [--timeout=DURATION] [--interval=DURATION]
//	stop
//	status [--bind=HOST:PORT]
//
// In this wave only `start` runs an in-process daemon. `stop` simply prints
// guidance — process-management lives in the host (systemd / launchd) and is
// outside scope. `status` issues a heartbeat-shaped GET to verify reachability.
func cmdDaemon(_ store.Config) {
	if len(os.Args) < 3 {
		printDaemonUsage()
		exitFunc(1)
	}
	switch os.Args[2] {
	case "start":
		runDaemonStart()
	case "stop":
		fmt.Println("aria-core daemon stop: send SIGTERM to the running process (manage via your service supervisor).")
	case "status":
		runDaemonStatus()
	case "help", "--help", "-h":
		printDaemonUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown daemon subcommand: %s\n", os.Args[2])
		printDaemonUsage()
		exitFunc(1)
	}
}

func runDaemonStart() {
	bind := "127.0.0.1:18081"
	timeout := 5 * time.Minute
	interval := 30 * time.Second
	for i := 3; i < len(os.Args); i++ {
		arg := os.Args[i]
		switch {
		case strings.HasPrefix(arg, "--bind="):
			bind = strings.TrimPrefix(arg, "--bind=")
		case strings.HasPrefix(arg, "--timeout="):
			if d, err := time.ParseDuration(strings.TrimPrefix(arg, "--timeout=")); err == nil {
				timeout = d
			}
		case strings.HasPrefix(arg, "--interval="):
			if d, err := time.ParseDuration(strings.TrimPrefix(arg, "--interval=")); err == nil {
				interval = d
			}
		}
	}

	w := capture.NewWatcher(timeout, func(ev capture.SummaryEvent) {
		fmt.Fprintf(os.Stderr, "[aria-core daemon] timeout session=%s goal=%q files=%d duration=%s reason=%s\n",
			ev.SessionID, ev.Goal, len(ev.Files), ev.Duration, ev.Reason)
	})

	listener, err := net.Listen("tcp", bind)
	if err != nil {
		fatal(err)
	}

	srv := &http.Server{Handler: w.HTTPHandler(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		_ = srv.Serve(listener)
	}()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fmt.Printf("aria-core daemon: listening on %s (timeout=%s, interval=%s)\n", bind, timeout, interval)
	w.Run(ctx, interval)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	_ = srv.Shutdown(shutdownCtx)
}

func runDaemonStatus() {
	bind := "127.0.0.1:18081"
	for i := 3; i < len(os.Args); i++ {
		if strings.HasPrefix(os.Args[i], "--bind=") {
			bind = strings.TrimPrefix(os.Args[i], "--bind=")
		}
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + bind + "/watch/sessions")
	if err != nil {
		fmt.Printf("aria-core daemon: not reachable on %s (%v)\n", bind, err)
		exitFunc(1)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("aria-core daemon: reachable on %s\n", bind)
	if len(body) > 0 {
		fmt.Println(string(body))
	}
}

func printDaemonUsage() {
	fmt.Print(`aria-core daemon — Claude Code session watcher (auto-summary on timeout)

Usage:
  aria-core daemon start  [--bind=HOST:PORT] [--timeout=DURATION] [--interval=DURATION]
  aria-core daemon status [--bind=HOST:PORT]
  aria-core daemon stop   (advisory: send SIGTERM via your supervisor)

Defaults: --bind=127.0.0.1:18081  --timeout=5m  --interval=30s

Endpoints:
  POST /watch/heartbeat  { "session_id": "...", "goal": "...", "files": [...] }
  GET  /watch/sessions   → JSON snapshot
`)
}
