package capture

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed templates/prepare-commit-msg templates/post-commit templates/post-merge
var hookTemplates embed.FS

// HookNames lists the bash scripts shipped with this package, in install order.
var HookNames = []string{"prepare-commit-msg", "post-commit", "post-merge"}

// HookHeader is prepended to every generated hook so we can identify scripts
// installed by aria-core for safe uninstall.
const HookHeader = "# aria-core-managed-hook"

// InstallOptions controls how Install writes hook scripts to disk.
type InstallOptions struct {
	// RepoDir is the working tree of the target repository. Required when
	// Global=false. When empty, the current working directory is used.
	RepoDir string
	// Global, when true, installs hooks under ~/.git-hooks-global and
	// configures `git config --global core.hooksPath` to point at it.
	Global bool
	// Force overwrites existing non-aria-core hooks instead of erroring out.
	Force bool
}

// InstallResult summarizes the outcome of an Install call.
type InstallResult struct {
	HooksDir  string   // absolute path where hooks landed
	Installed []string // hook filenames that were freshly written
	Skipped   []string // hook filenames that already existed and Force=false
	Global    bool     // whether the global path was configured
}

// Install writes the hook templates to the appropriate hooks directory
// (`<repo>/.git/hooks` by default; ~/.git-hooks-global when Global=true).
func Install(opts InstallOptions) (*InstallResult, error) {
	hooksDir, err := resolveHooksDir(opts)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, fmt.Errorf("capture: mkdir %s: %w", hooksDir, err)
	}

	res := &InstallResult{HooksDir: hooksDir, Global: opts.Global}

	for _, name := range HookNames {
		dst := filepath.Join(hooksDir, name)
		body, readErr := hookTemplates.ReadFile("templates/" + name)
		if readErr != nil {
			return nil, fmt.Errorf("capture: read embedded hook %s: %w", name, readErr)
		}

		// Detect existing hook. If it is not managed by us and Force=false → skip.
		if existing, statErr := os.ReadFile(dst); statErr == nil {
			if !isManagedHook(existing) && !opts.Force {
				res.Skipped = append(res.Skipped, name)
				continue
			}
		}

		// Inject the management header on the line right after the shebang.
		final := injectHeader(body)

		if err := os.WriteFile(dst, final, 0o755); err != nil {
			return nil, fmt.Errorf("capture: write %s: %w", dst, err)
		}
		// Best-effort chmod (no-op on Windows).
		_ = os.Chmod(dst, 0o755)
		res.Installed = append(res.Installed, name)
	}

	if opts.Global {
		if err := configureGlobalHooksPath(hooksDir); err != nil {
			return res, fmt.Errorf("capture: configure global hooksPath: %w", err)
		}
	}

	return res, nil
}

// Uninstall removes hooks managed by aria-core from the target hooks dir.
// Hooks not bearing the management header are left alone.
func Uninstall(opts InstallOptions) (*InstallResult, error) {
	hooksDir, err := resolveHooksDir(opts)
	if err != nil {
		return nil, err
	}
	res := &InstallResult{HooksDir: hooksDir, Global: opts.Global}

	for _, name := range HookNames {
		dst := filepath.Join(hooksDir, name)
		body, readErr := os.ReadFile(dst)
		if readErr != nil {
			res.Skipped = append(res.Skipped, name)
			continue
		}
		if !isManagedHook(body) {
			// Foreign hook — preserve it.
			res.Skipped = append(res.Skipped, name)
			continue
		}
		if err := os.Remove(dst); err != nil {
			return nil, fmt.Errorf("capture: remove %s: %w", dst, err)
		}
		res.Installed = append(res.Installed, name)
	}

	if opts.Global {
		// Best-effort: clear the global hooksPath if it points at our managed dir.
		_ = unsetGlobalHooksPath(hooksDir)
	}
	return res, nil
}

// resolveHooksDir maps InstallOptions to an absolute hooks directory.
func resolveHooksDir(opts InstallOptions) (string, error) {
	if opts.Global {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("capture: locate home: %w", err)
		}
		return filepath.Join(home, ".git-hooks-global"), nil
	}

	repo := strings.TrimSpace(opts.RepoDir)
	if repo == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("capture: getwd: %w", err)
		}
		repo = cwd
	}

	gitDir, err := findGitDir(repo)
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, "hooks"), nil
}

// findGitDir walks up from repo looking for a .git directory or file. It
// supports linked worktrees where .git is a file containing "gitdir: <path>".
func findGitDir(repo string) (string, error) {
	abs, err := filepath.Abs(repo)
	if err != nil {
		return "", err
	}
	dir := abs
	for {
		candidate := filepath.Join(dir, ".git")
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() {
				return candidate, nil
			}
			// .git is a file (worktree). Resolve gitdir: header.
			body, readErr := os.ReadFile(candidate)
			if readErr != nil {
				return "", fmt.Errorf("capture: read .git file: %w", readErr)
			}
			line := strings.TrimSpace(string(body))
			const prefix = "gitdir:"
			if !strings.HasPrefix(line, prefix) {
				return "", fmt.Errorf("capture: malformed .git file: %s", candidate)
			}
			ref := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if !filepath.IsAbs(ref) {
				ref = filepath.Join(dir, ref)
			}
			return ref, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("capture: %s is not a git repository", abs)
		}
		dir = parent
	}
}

// isManagedHook returns true if the hook content was generated by aria-core.
func isManagedHook(body []byte) bool {
	return strings.Contains(string(body), HookHeader)
}

// injectHeader inserts the management header right after the shebang line so
// future installs can recognise our hooks even after manual tweaks.
func injectHeader(body []byte) []byte {
	s := string(body)
	if strings.Contains(s, HookHeader) {
		return body
	}
	if strings.HasPrefix(s, "#!") {
		nl := strings.Index(s, "\n")
		if nl == -1 {
			return []byte(s + "\n" + HookHeader + "\n")
		}
		return []byte(s[:nl+1] + HookHeader + "\n" + s[nl+1:])
	}
	return []byte(HookHeader + "\n" + s)
}

// configureGlobalHooksPath runs `git config --global core.hooksPath <dir>`.
// Falls back to a no-op error message if git is unavailable.
func configureGlobalHooksPath(dir string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git binary not on PATH")
	}
	cmd := exec.Command(git, "config", "--global", "core.hooksPath", dir)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git config: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// unsetGlobalHooksPath removes core.hooksPath if it currently points at dir.
// Best-effort: any error is swallowed by the caller.
func unsetGlobalHooksPath(dir string) error {
	git, err := exec.LookPath("git")
	if err != nil {
		return err
	}
	out, err := exec.Command(git, "config", "--global", "--get", "core.hooksPath").Output()
	if err != nil {
		return err
	}
	current := strings.TrimSpace(string(out))
	if current != dir {
		return nil
	}
	return exec.Command(git, "config", "--global", "--unset", "core.hooksPath").Run()
}

// PlatformWarning returns a non-empty warning string when running on a
// platform where bash hooks may need extra setup (e.g. Windows + git-bash
// works but PowerShell-only environments don't).
func PlatformWarning() string {
	if runtime.GOOS == "windows" {
		return "warning: bash hooks require git-bash or WSL on Windows"
	}
	return ""
}
