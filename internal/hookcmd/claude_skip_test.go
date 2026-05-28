package hookcmd

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })
	return &buf
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func makeWorktreeLayout(t *testing.T) (projectRoot, worktreeRoot string) {
	t.Helper()
	projectRoot = t.TempDir()
	worktreeRoot = filepath.Join(projectRoot, ".endless", "worktrees", "e-test")
	writeTestFile(t, filepath.Join(worktreeRoot, ".endless", "worktree.json"), `{}`)
	return projectRoot, worktreeRoot
}

// writeSettingsOverride writes a worktree-level .claude/settings.json that
// registers <worktreeRoot>/bin/endless-hook as a hook command, simulating
// what claude-settings-init produces.
func writeSettingsOverride(t *testing.T, worktreeRoot string) {
	t.Helper()
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-hook")
	settings := fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":"%s claude","type":"command"}]}]}}`, worktreeBin)
	writeTestFile(t, filepath.Join(worktreeRoot, ".claude", "settings.json"), settings)
}

func setOsExecutable(t *testing.T, path string) {
	t.Helper()
	prev := osExecutable
	osExecutable = func() (string, error) { return path, nil }
	t.Cleanup(func() { osExecutable = prev })
}

func TestShouldSkipForWorktreeAt_CwdOutsideProject(t *testing.T) {
	projectRoot, _ := makeWorktreeLayout(t)
	other := t.TempDir()
	if shouldSkipForWorktreeAt(other, projectRoot) {
		t.Fatal("expected no skip when cwd is outside project")
	}
}

func TestShouldSkipForWorktreeAt_MainCheckoutNoCompanion(t *testing.T) {
	projectRoot := t.TempDir()
	cwd := filepath.Join(projectRoot, "src")
	if err := os.MkdirAll(cwd, 0755); err != nil {
		t.Fatal(err)
	}
	if shouldSkipForWorktreeAt(cwd, projectRoot) {
		t.Fatal("expected no skip in main checkout (no worktree companion)")
	}
}

func TestShouldSkipForWorktreeAt_NoSettingsFile(t *testing.T) {
	buf := captureLog(t)
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-hook")
	writeTestFile(t, worktreeBin, "#!/bin/sh\nexit 0\n")
	if shouldSkipForWorktreeAt(worktreeRoot, projectRoot) {
		t.Fatal("expected no skip when settings.json is missing (no override registered)")
	}
	if strings.Contains(buf.String(), "WARN") {
		t.Fatalf("expected no WARN when settings.json is missing; got: %q", buf.String())
	}
}

func TestShouldSkipForWorktreeAt_SettingsWithoutOverride(t *testing.T) {
	buf := captureLog(t)
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-hook")
	writeTestFile(t, worktreeBin, "#!/bin/sh\nexit 0\n")
	writeTestFile(t, filepath.Join(worktreeRoot, ".claude", "settings.json"), `{"enabledPlugins":{"frontend-design@claude-plugins-official":true}}`)
	if shouldSkipForWorktreeAt(worktreeRoot, projectRoot) {
		t.Fatal("expected no skip when settings.json doesn't reference worktree binary")
	}
	if strings.Contains(buf.String(), "WARN") {
		t.Fatalf("expected no WARN when settings.json has no override; got: %q", buf.String())
	}
}

func TestShouldSkipForWorktreeAt_WorktreeBinaryMissing(t *testing.T) {
	buf := captureLog(t)
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	writeSettingsOverride(t, worktreeRoot)
	if shouldSkipForWorktreeAt(worktreeRoot, projectRoot) {
		t.Fatal("expected no skip (fallback to global) when worktree binary missing")
	}
	logs := buf.String()
	if !strings.Contains(logs, "WARN") {
		t.Fatalf("expected WARN in log; got: %q", logs)
	}
	if !strings.Contains(logs, "does not exist") {
		t.Fatalf("expected 'does not exist' in log; got: %q", logs)
	}
	if !strings.Contains(logs, "just build") {
		t.Fatalf("expected remediation hint 'just build' in log; got: %q", logs)
	}
}

func TestShouldSkipForWorktreeAt_SelfIsWorktreeBinary(t *testing.T) {
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	writeSettingsOverride(t, worktreeRoot)
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-hook")
	writeTestFile(t, worktreeBin, "#!/bin/sh\nexit 0\n")
	setOsExecutable(t, worktreeBin)
	if shouldSkipForWorktreeAt(worktreeRoot, projectRoot) {
		t.Fatal("expected no skip when self IS the worktree binary")
	}
}

func TestShouldSkipForWorktreeAt_SelfIsGlobal(t *testing.T) {
	buf := captureLog(t)
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	writeSettingsOverride(t, worktreeRoot)
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-hook")
	writeTestFile(t, worktreeBin, "#!/bin/sh\nexit 0\n")
	globalBin := filepath.Join(t.TempDir(), "endless-hook")
	writeTestFile(t, globalBin, "#!/bin/sh\nexit 1\n")
	setOsExecutable(t, globalBin)
	if !shouldSkipForWorktreeAt(worktreeRoot, projectRoot) {
		t.Fatal("expected skip when self is the global binary")
	}
	if !strings.Contains(buf.String(), "deferring to") {
		t.Fatalf("expected 'deferring to' log line; got: %q", buf.String())
	}
}

func TestShouldSkipForWorktree_ZeroProjectID(t *testing.T) {
	if shouldSkipForWorktree(0, "/some/cwd") {
		t.Fatal("expected no skip when projectID is 0")
	}
}

func TestShouldSkipForWorktree_EmptyCwd(t *testing.T) {
	if shouldSkipForWorktree(42, "") {
		t.Fatal("expected no skip when cwd is empty")
	}
}
