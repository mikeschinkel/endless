package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestMarkSettingsSkipWorktree_TrackedFileSetsBit(t *testing.T) {
	dir := initTestGitRepo(t)
	writeSettingsFixture(t, dir, `{}`)
	gitOrFatal(t, dir, "add", ".claude/settings.json")
	gitOrFatal(t, dir, "commit", "-m", "add settings")

	if err := markSettingsSkipWorktree(dir); err != nil {
		t.Fatalf("markSettingsSkipWorktree: %v", err)
	}

	out := gitOutputOrFatal(t, dir, "ls-files", "-v", ".claude/settings.json")
	if !strings.HasPrefix(out, "S ") {
		t.Fatalf("expected leading 'S ' (skip-worktree), got %q", out)
	}
}

func TestMarkSettingsSkipWorktree_UntrackedFileNoError(t *testing.T) {
	dir := initTestGitRepo(t)
	writeSettingsFixture(t, dir, `{}`)

	if err := markSettingsSkipWorktree(dir); err != nil {
		t.Fatalf("expected no error for untracked file, got: %v", err)
	}
}

func TestMarkSettingsSkipWorktree_NonGitDirNoError(t *testing.T) {
	dir := t.TempDir()

	if err := markSettingsSkipWorktree(dir); err != nil {
		t.Fatalf("expected no error for non-git directory, got: %v", err)
	}
}

func TestMarkSettingsSkipWorktree_Idempotent(t *testing.T) {
	dir := initTestGitRepo(t)
	writeSettingsFixture(t, dir, `{}`)
	gitOrFatal(t, dir, "add", ".claude/settings.json")
	gitOrFatal(t, dir, "commit", "-m", "add settings")

	for i := 0; i < 2; i++ {
		if err := markSettingsSkipWorktree(dir); err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
	}
	out := gitOutputOrFatal(t, dir, "ls-files", "-v", ".claude/settings.json")
	if !strings.HasPrefix(out, "S ") {
		t.Fatalf("expected leading 'S ' after repeat, got %q", out)
	}
}

func initTestGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitOrFatal(t, dir, "init", "-q", "-b", "main")
	gitOrFatal(t, dir, "config", "user.email", "test@example.com")
	gitOrFatal(t, dir, "config", "user.name", "test")
	gitOrFatal(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func writeSettingsFixture(t *testing.T, dir, body string) {
	t.Helper()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func gitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
}

func gitOutputOrFatal(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out)
}
