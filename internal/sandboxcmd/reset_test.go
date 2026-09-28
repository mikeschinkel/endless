package sandboxcmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetWorktree lays out <root>/.endless/worktrees/e-42 and returns the
// worktree and its in-tree sandbox. Reset resolves both from the path alone, so
// no git repository is needed.
func resetWorktree(t *testing.T) (worktree, sandbox string) {
	t.Helper()
	root := t.TempDir()
	worktree = filepath.Join(root, ".endless", "worktrees", "e-42")
	sandbox = filepath.Join(worktree, ".endless", "sandbox")
	if err := os.MkdirAll(sandbox, 0o755); err != nil {
		t.Fatal(err)
	}
	return worktree, sandbox
}

func writeSeedHook(t *testing.T, worktree, body string, mode os.FileMode) {
	t.Helper()
	hook := filepath.Join(worktree, SeedSandboxHook)
	if err := os.MkdirAll(filepath.Dir(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hook, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func TestResetClearsSandboxAndRunsSeedHook(t *testing.T) {
	worktree, sandbox := resetWorktree(t)
	stale := filepath.Join(sandbox, "endless", "endless.db")
	if err := os.MkdirAll(filepath.Dir(stale), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeSeedHook(t, worktree,
		"#!/bin/sh\nprintf '%s\\n%s\\n%s\\n' \"$1\" \"$2\" \"$(pwd -P)\" > \"$2/seeded\"\necho hook-says-hi\n",
		0o755)

	var out bytes.Buffer
	got, err := Reset(filepath.Join(worktree, "internal"), &out)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if got != sandbox {
		t.Errorf("Reset returned %q, want %q", got, sandbox)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale content survived the reset: %v", err)
	}
	gi, err := os.ReadFile(filepath.Join(sandbox, ".gitignore"))
	if err != nil || string(gi) != SandboxGitignore {
		t.Errorf(".gitignore not written: %v %q", err, gi)
	}
	seeded, err := os.ReadFile(filepath.Join(sandbox, "seeded"))
	if err != nil {
		t.Fatalf("hook did not run: %v", err)
	}
	realWT, _ := filepath.EvalSymlinks(worktree)
	want := worktree + "\n" + sandbox + "\n" + realWT + "\n"
	if string(seeded) != want {
		t.Errorf("hook args/cwd = %q, want %q", seeded, want)
	}
	if !strings.Contains(out.String(), "hook-says-hi") {
		t.Errorf("hook output not forwarded: %q", out.String())
	}
}

func TestResetWithoutHookLeavesOnlyStandardContents(t *testing.T) {
	worktree, sandbox := resetWorktree(t)
	if err := os.WriteFile(filepath.Join(sandbox, "leftover"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Reset(worktree, &bytes.Buffer{}); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	entries, err := os.ReadDir(sandbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != ".gitignore" {
		t.Errorf("sandbox = %v, want only .gitignore", entries)
	}
}

func TestResetRefusesNonExecutableHook(t *testing.T) {
	worktree, _ := resetWorktree(t)
	writeSeedHook(t, worktree, "#!/bin/sh\n", 0o644)
	_, err := Reset(worktree, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Errorf("err = %v, want a not-executable refusal", err)
	}
}

func TestResetFailsWhenHookFails(t *testing.T) {
	worktree, _ := resetWorktree(t)
	writeSeedHook(t, worktree, "#!/bin/sh\nexit 3\n", 0o755)
	_, err := Reset(worktree, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "exit status 3") {
		t.Errorf("err = %v, want the hook's failure", err)
	}
}

func TestResetRefusesOutsideWorktree(t *testing.T) {
	dir := t.TempDir()
	_, err := Reset(dir, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "not inside a task worktree") {
		t.Errorf("err = %v, want a not-in-worktree refusal", err)
	}
}
