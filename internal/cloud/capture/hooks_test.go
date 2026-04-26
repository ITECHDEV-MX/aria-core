package capture

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// initFakeRepo creates a minimal git repo skeleton (just `.git/hooks`) so
// findGitDir resolves cleanly without invoking git itself.
func initFakeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "hooks"), 0o755); err != nil {
		t.Fatalf("init repo: %v", err)
	}
	return dir
}

func TestInstall_WritesAllHooks(t *testing.T) {
	repo := initFakeRepo(t)
	res, err := Install(InstallOptions{RepoDir: repo})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if len(res.Installed) != len(HookNames) {
		t.Fatalf("want %d hooks installed, got %d (%v)", len(HookNames), len(res.Installed), res.Installed)
	}
	for _, name := range HookNames {
		path := filepath.Join(repo, ".git", "hooks", name)
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if !strings.HasPrefix(string(body), "#!") {
			t.Fatalf("%s missing shebang", name)
		}
		if !strings.Contains(string(body), HookHeader) {
			t.Fatalf("%s missing managed header", name)
		}
		if runtime.GOOS != "windows" {
			info, err := os.Stat(path)
			if err != nil {
				t.Fatalf("stat: %v", err)
			}
			if info.Mode().Perm()&0o111 == 0 {
				t.Fatalf("%s not executable: %v", name, info.Mode())
			}
		}
	}
}

func TestInstall_PreservesForeignHook(t *testing.T) {
	repo := initFakeRepo(t)
	foreign := []byte("#!/bin/sh\n# user wrote this\necho hi\n")
	target := filepath.Join(repo, ".git", "hooks", "post-commit")
	if err := os.WriteFile(target, foreign, 0o755); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res, err := Install(InstallOptions{RepoDir: repo})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !contains(res.Skipped, "post-commit") {
		t.Fatalf("post-commit should be skipped, got %+v", res)
	}
	body, _ := os.ReadFile(target)
	if string(body) != string(foreign) {
		t.Fatal("foreign hook was overwritten")
	}
}

func TestInstall_ForceOverwritesForeign(t *testing.T) {
	repo := initFakeRepo(t)
	target := filepath.Join(repo, ".git", "hooks", "post-commit")
	_ = os.WriteFile(target, []byte("#!/bin/sh\necho old\n"), 0o755)
	res, err := Install(InstallOptions{RepoDir: repo, Force: true})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !contains(res.Installed, "post-commit") {
		t.Fatalf("post-commit should be installed with Force: %+v", res)
	}
	body, _ := os.ReadFile(target)
	if !strings.Contains(string(body), HookHeader) {
		t.Fatal("Force install did not overwrite to managed hook")
	}
}

func TestUninstall_RemovesManagedOnly(t *testing.T) {
	repo := initFakeRepo(t)
	if _, err := Install(InstallOptions{RepoDir: repo}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	// Drop a foreign post-merge hook to verify it stays.
	foreign := filepath.Join(repo, ".git", "hooks", "post-merge")
	_ = os.WriteFile(foreign, []byte("#!/bin/sh\necho keep\n"), 0o755)

	res, err := Uninstall(InstallOptions{RepoDir: repo})
	if err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !contains(res.Installed, "post-commit") {
		t.Fatalf("post-commit should be removed, got %+v", res)
	}
	if !contains(res.Skipped, "post-merge") {
		t.Fatalf("foreign post-merge should be skipped: %+v", res)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign hook should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".git", "hooks", "post-commit")); !os.IsNotExist(err) {
		t.Fatalf("post-commit should be gone: %v", err)
	}
}

func TestFindGitDir_NotARepo(t *testing.T) {
	tmp := t.TempDir()
	if _, err := findGitDir(tmp); err == nil {
		t.Fatal("expected error for non-repo")
	}
}

func TestFindGitDir_Worktree(t *testing.T) {
	main := t.TempDir()
	gitdir := filepath.Join(main, ".git", "worktrees", "wt")
	if err := os.MkdirAll(gitdir, 0o755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(wt, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, ".git"),
		[]byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := findGitDir(wt)
	if err != nil {
		t.Fatalf("findGitDir: %v", err)
	}
	if got != gitdir {
		t.Fatalf("want %q got %q", gitdir, got)
	}
}

func TestInjectHeader_Idempotent(t *testing.T) {
	body := []byte("#!/usr/bin/env bash\necho hi\n")
	once := injectHeader(body)
	twice := injectHeader(once)
	if string(once) != string(twice) {
		t.Fatal("injectHeader must be idempotent")
	}
	if !strings.Contains(string(once), HookHeader) {
		t.Fatal("header missing")
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
