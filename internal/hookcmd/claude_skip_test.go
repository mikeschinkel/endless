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

// writeSettingsOverride writes a worktree-level .claude/settings.local.json
// that registers <worktreeRoot>/bin/endless-go as a hook command, simulating
// what claude-settings-init produces.
func writeSettingsOverride(t *testing.T, worktreeRoot string) {
	t.Helper()
	writeOverrideTo(t, worktreeRoot, "settings.local.json")
}

// writeLegacySettingsOverride writes the same override into the TRACKED
// settings.json, which is where claude-settings-init put it before E-1347. A
// worktree bootstrapped then still carries it there until the repair runs, and
// the detector has to keep recognizing it.
func writeLegacySettingsOverride(t *testing.T, worktreeRoot string) {
	t.Helper()
	writeOverrideTo(t, worktreeRoot, "settings.json")
}

func writeOverrideTo(t *testing.T, worktreeRoot, filename string) {
	t.Helper()
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
	settings := fmt.Sprintf(`{"hooks":{"SessionStart":[{"hooks":[{"command":"%s claude","type":"command"}]}]}}`, worktreeBin)
	writeTestFile(t, filepath.Join(worktreeRoot, ".claude", filename), settings)
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
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
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
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
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
}

func TestShouldSkipForWorktreeAt_SelfIsWorktreeBinary(t *testing.T) {
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	writeSettingsOverride(t, worktreeRoot)
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
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
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
	writeTestFile(t, worktreeBin, "#!/bin/sh\nexit 0\n")
	globalBin := filepath.Join(t.TempDir(), "endless-go")
	writeTestFile(t, globalBin, "#!/bin/sh\nexit 1\n")
	setOsExecutable(t, globalBin)
	if !shouldSkipForWorktreeAt(worktreeRoot, projectRoot) {
		t.Fatal("expected skip when self is the global binary")
	}
	if !strings.Contains(buf.String(), "deferring to") {
		t.Fatalf("expected 'deferring to' log line; got: %q", buf.String())
	}
}

func TestShouldSkipForWorktreeAt_SelfIsGlobal_LegacySettingsJSON(t *testing.T) {
	projectRoot, worktreeRoot := makeWorktreeLayout(t)
	writeLegacySettingsOverride(t, worktreeRoot)
	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
	writeTestFile(t, worktreeBin, "#!/bin/sh\nexit 0\n")
	globalBin := filepath.Join(t.TempDir(), "endless-go")
	writeTestFile(t, globalBin, "#!/bin/sh\nexit 1\n")
	setOsExecutable(t, globalBin)
	if !shouldSkipForWorktreeAt(worktreeRoot, projectRoot) {
		t.Fatal("expected skip when the override is still in the tracked settings.json")
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

// TestWorktreeOverrideRegistered_SurvivesPathSpelling is E-1983's guard on the
// self-skip check. FindWorktreeRoot now resolves symlinks, so the worktree root
// this side derives can be spelled differently from the absolute path
// claude-settings-init recorded in the settings file — /private/var vs /var on
// macOS, or any symlinked parent. Matching on the worktree-relative tail is what
// keeps the two agreeing; an absolute-path match silently stops recognizing the
// override, the global binary stops deferring, and every hook fires twice.
func TestWorktreeOverrideRegistered_SurvivesPathSpelling(t *testing.T) {
	_, worktreeRoot := makeWorktreeLayout(t)
	// The settings file records the override under a DIFFERENT spelling of the
	// same project — the shape a symlinked path produces.
	writeTestFile(t,
		filepath.Join(worktreeRoot, ".claude", "settings.local.json"),
		`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":`+
			`"/elsewhere/spelled/project/.endless/worktrees/e-test/bin/endless-go hook claude"}]}]}}`)

	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
	if !worktreeOverrideRegistered(worktreeRoot, worktreeBin) {
		t.Fatal("override not recognized when the settings file spells the project root differently")
	}
}

// TestWorktreeOverrideRegistered_DoesNotMatchAnotherWorktree pins that the
// relative-tail match above stays specific: the worktree's own name is part of
// the needle, so a sibling worktree's override is not mistaken for this one's.
func TestWorktreeOverrideRegistered_DoesNotMatchAnotherWorktree(t *testing.T) {
	_, worktreeRoot := makeWorktreeLayout(t)
	writeTestFile(t,
		filepath.Join(worktreeRoot, ".claude", "settings.local.json"),
		`{"hooks":{"PreToolUse":[{"hooks":[{"type":"command","command":`+
			`"/p/.endless/worktrees/e-other/bin/endless-go hook claude"}]}]}}`)

	worktreeBin := filepath.Join(worktreeRoot, "bin", "endless-go")
	if worktreeOverrideRegistered(worktreeRoot, worktreeBin) {
		t.Fatal("a sibling worktree's override was mistaken for this worktree's")
	}
}
