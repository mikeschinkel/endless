package sandboxcmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubCmds swaps runGuardCmd for a fixture keyed by the first argument
// ("worktree", "branch", "list-windows"), restoring the real one at cleanup.
func stubCmds(t *testing.T, out map[string]string, fail map[string]bool) {
	t.Helper()
	prev := runGuardCmd
	t.Cleanup(func() { runGuardCmd = prev })
	runGuardCmd = func(dir, name string, args ...string) (string, error) {
		key := ""
		for _, a := range args {
			switch a {
			case "worktree", "branch", "list-windows", "rev-parse":
				key = a
			}
			if key != "" {
				break
			}
		}
		if fail[key] {
			return out[key], errors.New("stub failure: " + key)
		}
		return out[key], nil
	}
}

// newTestGuard builds a guard over a temp project root with stubbed probes.
func newTestGuard(t *testing.T, out map[string]string, fail map[string]bool) *ReapGuard {
	t.Helper()
	stubCmds(t, out, fail)
	guard, err := NewReapGuard(t.TempDir())
	if err != nil {
		t.Fatalf("NewReapGuard: %v", err)
	}
	return guard
}

func emptyGuard(t *testing.T) *ReapGuard {
	t.Helper()
	return newTestGuard(t, map[string]string{}, nil)
}

// guardWithTmuxWindows builds a guard whose tmux probe reports exactly these
// window names.
func guardWithTmuxWindows(t *testing.T, windows ...string) *ReapGuard {
	t.Helper()
	return newTestGuard(t, map[string]string{
		"list-windows": strings.Join(windows, "\n") + "\n",
	}, nil)
}

// guardWithTmux builds a guard seeing a window named for `name`'s task in the
// form windows carry today (E-2102), plus one for an unrelated task.
func guardWithTmux(t *testing.T, name string) *ReapGuard {
	t.Helper()
	id := strings.TrimPrefix(name, "e-")
	return guardWithTmuxWindows(t, "E-"+id, "E-9999")
}

func guardWithUnmerged(t *testing.T, name string) *ReapGuard {
	t.Helper()
	id := strings.TrimPrefix(name, "e-")
	return newTestGuard(t, map[string]string{
		"branch": "task/" + id + "-some-slug\nmain\n",
	}, nil)
}

// TestReapGuardProtectionConditions pins each of the four conditions
// independently. Conditions 3 and 4 are the ones that survive worktree
// deletion — omitting them destroyed 10 real sandboxes (E-1904), so each gets
// an explicit case rather than being folded into a table of "protected".
func TestReapGuardProtectionConditions(t *testing.T) {
	t.Run("unprotected_when_nothing_references_it", func(t *testing.T) {
		guard := emptyGuard(t)
		protected, reason := guard.Protected("e-1")
		if protected {
			t.Fatalf("expected unprotected, got protected: %s", reason)
		}
	})

	t.Run("protected_by_live_tmux_window", func(t *testing.T) {
		guard := guardWithTmux(t, "e-42")
		protected, reason := guard.Protected("e-42")
		if !protected {
			t.Fatal("a live tmux window must protect the sandbox")
		}
		if reason != reasonTmuxWindow {
			t.Fatalf("reason = %q, want %q", reason, reasonTmuxWindow)
		}
	})

	t.Run("protected_by_unmerged_branch", func(t *testing.T) {
		guard := guardWithUnmerged(t, "e-77")
		protected, reason := guard.Protected("e-77")
		if !protected {
			t.Fatal("an unmerged task branch must protect the sandbox")
		}
		if reason != reasonUnmergedBranch {
			t.Fatalf("reason = %q, want %q", reason, reasonUnmergedBranch)
		}
	})

	t.Run("protected_by_git_tracked_worktree", func(t *testing.T) {
		guard := newTestGuard(t, map[string]string{
			"worktree": "worktree /repo/.endless/worktrees/e-55\nHEAD abc\n",
		}, nil)
		protected, reason := guard.Protected("e-55")
		if !protected {
			t.Fatal("a git-tracked worktree must protect the sandbox")
		}
		if reason != reasonGitWorktree {
			t.Fatalf("reason = %q, want %q", reason, reasonGitWorktree)
		}
	})

	t.Run("protected_by_existing_worktree_dir", func(t *testing.T) {
		stubCmds(t, map[string]string{}, nil)
		root := t.TempDir()
		err := os.MkdirAll(filepath.Join(root, ".endless", "worktrees", "e-9"), 0o755)
		if err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		guard, err := NewReapGuard(root)
		if err != nil {
			t.Fatalf("NewReapGuard: %v", err)
		}
		protected, reason := guard.Protected("e-9")
		if !protected {
			t.Fatal("an existing worktree dir must protect the sandbox")
		}
		if reason != reasonWorktreeDir {
			t.Fatalf("reason = %q, want %q", reason, reasonWorktreeDir)
		}
	})

	t.Run("ephemeral_names_are_not_worktree_bound", func(t *testing.T) {
		guard := guardWithTmux(t, "e-42")
		protected, _ := guard.Protected("a1b2c3d4")
		if protected {
			t.Fatal("a random-hex ephemeral name must not be worktree-protected")
		}
	})
}

// TestReapGuardTmuxWindowForms pins which window names spare a sandbox.
//
// The window name is the user's own record that a task is still in play, and
// it is one of the two protections that outlive the worktree directory — so
// the set of names that count is load-bearing, not cosmetic. E-2102 renamed
// windows from `<project>_<slug>[E-NNNN]` to the bare id; both forms must
// match, because windows named before the rename stay open until their
// sessions end, and a guard that protected only the new ones would drop a
// sandbox with no error and no signal.
//
// The negative cases are the other half: both forms are anchored to the whole
// name, so a window that merely mentions an id in passing spares nothing.
func TestReapGuardTmuxWindowForms(t *testing.T) {
	tests := []struct {
		name    string
		window  string
		protect bool
	}{
		{"the bare id windows carry today", "E-42", true},
		{"the bracketed form from before E-2102", "endless_some-slug[E-42]", true},
		{"an id merely mentioned in the name", "notes on E-42", false},
		{"the id with anything after it", "E-42 scratch", false},
		{"the bracketed id with anything after it", "endless_x[E-42] scratch", false},
		{"a longer id that starts with it", "E-420", false},
		{"the command name tmux used to fall back to", "claude", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			guard := guardWithTmuxWindows(t, tc.window)
			protected, reason := guard.Protected("e-42")
			if protected != tc.protect {
				t.Fatalf("window %q: protected = %v (%s), want %v",
					tc.window, protected, reason, tc.protect)
			}
			if protected && reason != reasonTmuxWindow {
				t.Fatalf("window %q: reason = %q, want %q",
					tc.window, reason, reasonTmuxWindow)
			}
		})
	}
}

// TestReapGuardFailsClosed asserts a probe error surfaces rather than yielding
// a guard that silently protects nothing. Callers refuse to reap on error, so
// swallowing one here would unprotect every sandbox at once.
func TestReapGuardFailsClosed(t *testing.T) {
	for _, probe := range []string{"worktree", "branch"} {
		t.Run(probe, func(t *testing.T) {
			stubCmds(t, map[string]string{}, map[string]bool{probe: true})
			_, err := NewReapGuard(t.TempDir())
			if err == nil {
				t.Fatalf("a failing %q probe must return an error", probe)
			}
		})
	}
}

// TestReapGuardNoTmuxServerIsEmptyNotError pins the one tmux failure that is
// legitimately "no live windows" rather than a broken probe — otherwise every
// reap on a machine without tmux would refuse.
func TestReapGuardNoTmuxServerIsEmptyNotError(t *testing.T) {
	stubCmds(t, map[string]string{
		"list-windows": "no server running on /tmp/tmux-501/default",
	}, map[string]bool{"list-windows": true})

	guard, err := NewReapGuard(t.TempDir())
	if err != nil {
		t.Fatalf("no tmux server must not be an error, got: %v", err)
	}
	protected, _ := guard.Protected("e-1")
	if protected {
		t.Fatal("no tmux server means no window protection")
	}
}
