package monitor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newWorktree builds <tmp>/.endless/worktrees/<name> and returns the worktree
// dir. cfg, when non-empty, is written to <tmp>/.endless/config.json.
func newWorktree(t *testing.T, name, cfg string) string {
	t.Helper()
	root := t.TempDir()
	endless := filepath.Join(root, ".endless")
	wt := filepath.Join(endless, "worktrees", name)
	if err := os.MkdirAll(wt, 0755); err != nil {
		t.Fatal(err)
	}
	if cfg != "" {
		if err := os.WriteFile(filepath.Join(endless, "config.json"), []byte(cfg), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return wt
}

func TestWorktreeSandboxDir(t *testing.T) {
	t.Run("in-tree by default", func(t *testing.T) {
		wt := newWorktree(t, "e-1964", `{"self_dev": true}`)
		want := filepath.Join(wt, ".endless", "sandbox")
		if got := WorktreeSandboxDir(wt); got != want {
			t.Errorf("WorktreeSandboxDir(%q) = %q, want %q", wt, got, want)
		}
		if got := WorktreeSandboxConfigDir(wt); got != filepath.Join(want, "endless") {
			t.Errorf("WorktreeSandboxConfigDir = %q, want %q", got, filepath.Join(want, "endless"))
		}
	})

	t.Run("resolves from a path deeper inside the worktree", func(t *testing.T) {
		wt := newWorktree(t, "e-1964", "")
		deep := filepath.Join(wt, "internal", "monitor")
		want := filepath.Join(wt, ".endless", "sandbox")
		if got := WorktreeSandboxDir(deep); got != want {
			t.Errorf("WorktreeSandboxDir(%q) = %q, want %q", deep, got, want)
		}
	})

	t.Run("project override moves it out of tree", func(t *testing.T) {
		out := t.TempDir()
		wt := newWorktree(t, "e-1964", `{"sandbox_root": "`+out+`"}`)
		want := filepath.Join(out, "e-1964")
		if got := WorktreeSandboxDir(wt); got != want {
			t.Errorf("WorktreeSandboxDir(%q) = %q, want %q", wt, got, want)
		}
	})

	t.Run("outside a worktree there is no sandbox", func(t *testing.T) {
		if got := WorktreeSandboxDir(t.TempDir()); got != "" {
			t.Errorf("WorktreeSandboxDir = %q, want empty outside a worktree", got)
		}
	})

	t.Run("a named-alternate dir is not a task worktree (ED-1515)", func(t *testing.T) {
		wt := newWorktree(t, "e-1964-my-slug", "")
		if got := WorktreeSandboxDir(wt); got != "" {
			t.Errorf("WorktreeSandboxDir = %q, want empty for a non-canonical worktree name", got)
		}
	})
}

// TestIsSandboxActive covers the E-1964 detection: the answer is whether
// ConfigDir() IS the resolved sandbox of a worktree, not whether it sits under
// some shared root. Both layouts are exercised, because they are answered by
// different halves of the check — in-tree from ConfigDir() itself, out-of-tree
// from cwd.
func TestIsSandboxActive(t *testing.T) {
	t.Run("no XDG_CONFIG_HOME, default ~/.config", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("XDG_CONFIG_HOME", "")
		if IsSandboxActive() {
			t.Error("IsSandboxActive() = true for the default config dir")
		}
	})

	t.Run("config IS a worktree's in-tree sandbox", func(t *testing.T) {
		resetDBContext(t)
		wt := newWorktree(t, "e-1354", `{"self_dev": true}`)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(wt, ".endless", "sandbox"))
		if !IsSandboxActive() {
			t.Errorf("IsSandboxActive() = false for ConfigDir() = %q", ConfigDir())
		}
	})

	t.Run("config is some other dir inside a worktree", func(t *testing.T) {
		resetDBContext(t)
		wt := newWorktree(t, "e-1354", `{"self_dev": true}`)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(wt, ".endless", "elsewhere"))
		if IsSandboxActive() {
			t.Errorf("IsSandboxActive() = true for ConfigDir() = %q, which is not the sandbox", ConfigDir())
		}
	})

	t.Run("config outside any worktree", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "elsewhere"))
		if IsSandboxActive() {
			t.Error("IsSandboxActive() = true outside a worktree")
		}
	})

	t.Run("out-of-tree override, answered from cwd", func(t *testing.T) {
		resetDBContext(t)
		out := t.TempDir()
		wt := newWorktree(t, "e-1354", `{"self_dev": true, "sandbox_root": "`+out+`"}`)
		t.Chdir(wt)
		// The override path names no worktree, so only cwd can say which
		// worktree this config dir belongs to.
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(out, "e-1354"))
		if !IsSandboxActive() {
			t.Errorf("IsSandboxActive() = false for override ConfigDir() = %q", ConfigDir())
		}
	})

	t.Run("override root, but cwd is a DIFFERENT worktree", func(t *testing.T) {
		resetDBContext(t)
		out := t.TempDir()
		wt := newWorktree(t, "e-1354", `{"self_dev": true, "sandbox_root": "`+out+`"}`)
		t.Chdir(wt)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(out, "e-9999"))
		if IsSandboxActive() {
			t.Error("IsSandboxActive() = true for another worktree's sandbox")
		}
	})
}

func TestForceRealDB(t *testing.T) {
	realSuffix := filepath.Join(".config", "endless", "endless.db")

	t.Run("redirects DB to real path without mutating env", func(t *testing.T) {
		resetDBContext(t)
		wt := newWorktree(t, "e-1450", `{"self_dev": true}`)
		sandbox := filepath.Join(wt, ".endless", "sandbox")
		t.Setenv("XDG_CONFIG_HOME", sandbox)

		if !IsSandboxActive() {
			t.Fatal("precondition: expected sandbox routing to be active")
		}
		if got := DBPath(); !strings.HasPrefix(got, sandbox) {
			t.Fatalf("precondition: DBPath() = %q, want under sandbox %q", got, sandbox)
		}

		ForceRealDB()

		if got := DBPath(); !strings.HasSuffix(got, realSuffix) {
			t.Errorf("after ForceRealDB(): DBPath() = %q, want suffix %q", got, realSuffix)
		}
		if strings.HasPrefix(DBPath(), sandbox) {
			t.Errorf("after ForceRealDB(): DBPath() = %q still under sandbox", DBPath())
		}
		// Env must be untouched: XDG_CONFIG_HOME stays sandbox-routed, so
		// IsSandboxActive() (and the log / config.json reads built on it) is
		// unaffected — only the DB path is redirected.
		if !IsSandboxActive() {
			t.Error("ForceRealDB() must not mutate env: IsSandboxActive() should still be true")
		}
		// Backups follow the DB, not ConfigDir().
		wantBackups := filepath.Join(filepath.Dir(DBPath()), "backups")
		if strings.HasPrefix(wantBackups, sandbox) {
			t.Errorf("backup dir %q should not be under sandbox after override", wantBackups)
		}
	})

	t.Run("no-op when not sandbox-routed", func(t *testing.T) {
		resetDBContext(t)
		config := filepath.Join(t.TempDir(), "myconfig")
		t.Setenv("XDG_CONFIG_HOME", config)

		if IsSandboxActive() {
			t.Fatal("precondition: expected no sandbox routing")
		}
		before := DBPath()

		ForceRealDB()

		if got := DBPath(); got != before {
			t.Errorf("ForceRealDB() should be a no-op outside a sandbox: DBPath() = %q, want %q", got, before)
		}
	})
}

// TestGuardRefusesMissingSandbox is the E-1964 refusal. A self-dev worktree
// with no sandbox must say so and name the command that recreates it — never
// route to the absent path and never build one.
func TestGuardRefusesMissingSandbox(t *testing.T) {
	t.Run("missing sandbox names the worktree and the remedy", func(t *testing.T) {
		resetDBContext(t)
		wt := newWorktree(t, "e-1964", `{"self_dev": true}`)
		t.Chdir(wt)

		err := guardWorktreeDBContext()
		if err == nil {
			t.Fatal("guardWorktreeDBContext() = nil, want a refusal")
		}
		msg := err.Error()
		for _, want := range []string{"no sandbox", "just dev-sandbox-init", "e-1964"} {
			if !strings.Contains(msg, want) {
				t.Errorf("refusal does not mention %q:\n%s", want, msg)
			}
		}
	})

	t.Run("present sandbox falls through to the ordinary --db refusal", func(t *testing.T) {
		resetDBContext(t)
		wt := newWorktree(t, "e-1964", `{"self_dev": true}`)
		if err := os.MkdirAll(filepath.Join(wt, ".endless", "sandbox"), 0755); err != nil {
			t.Fatal(err)
		}
		t.Chdir(wt)

		if err := guardWorktreeDBContext(); err != worktreeDBContextRefusal {
			t.Errorf("guardWorktreeDBContext() = %v, want the ordinary --db refusal", err)
		}
	})

	t.Run("an explicit context still wins over a missing sandbox", func(t *testing.T) {
		resetDBContext(t)
		wt := newWorktree(t, "e-1964", `{"self_dev": true}`)
		t.Chdir(wt)
		SetDBContextDir(t.TempDir())

		if err := guardWorktreeDBContext(); err != nil {
			t.Errorf("guardWorktreeDBContext() = %v, want nil (--db main must work in a sandbox-less worktree)", err)
		}
	})
}
