package monitor

import (
	"errors"
	"os"
	"slices"
	"testing"
)

// TestChildDBRoute pins how a Go runner hands a Python `endless` child its own
// database (E-2186): by flag and working directory, never by environment.
func TestChildDBRoute(t *testing.T) {
	t.Run("main: no flag, neutral directory", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Chdir(t.TempDir())

		args, dir, err := ChildDBRoute()
		if err != nil {
			t.Fatalf("ChildDBRoute() error = %v", err)
		}
		if len(args) != 0 {
			t.Errorf("args = %v, want none: the child's default IS main", args)
		}
		if dir != os.TempDir() {
			t.Errorf("dir = %q, want %q", dir, os.TempDir())
		}
	})

	t.Run("main via PinMainDB is still main", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")
		PinMainDB()

		args, _, err := ChildDBRoute()
		if err != nil || len(args) != 0 {
			t.Errorf("ChildDBRoute() = %v, %v; want no flag and no error", args, err)
		}
	})

	t.Run("sandbox: --db sandbox from the worktree root", func(t *testing.T) {
		resetDBContext(t)
		wt, sandboxDir := newGatedWorktree(t, "e-2186", true)
		t.Chdir(wt)
		SetDBContextDir(sandboxDir)

		args, dir, err := ChildDBRoute()
		if err != nil {
			t.Fatalf("ChildDBRoute() error = %v", err)
		}
		if !slices.Equal(args, []string{"--db", "sandbox"}) {
			t.Errorf("args = %v, want [--db sandbox]", args)
		}
		if resolvedPath(dir) != resolvedPath(wt) {
			t.Errorf("dir = %q, want the worktree root %q", dir, wt)
		}
	})

	t.Run("sandbox is recognised even when cwd is elsewhere", func(t *testing.T) {
		resetDBContext(t)
		wt, sandboxDir := newGatedWorktree(t, "e-2186", true)
		t.Chdir(t.TempDir())
		SetDBContextDir(sandboxDir)

		args, dir, err := ChildDBRoute()
		if err != nil {
			t.Fatalf("ChildDBRoute() error = %v", err)
		}
		if !slices.Equal(args, []string{"--db", "sandbox"}) || dir != wt {
			t.Errorf("ChildDBRoute() = %v, %q; want [--db sandbox], %q", args, dir, wt)
		}
	})

	t.Run("any other directory is refused, not routed to main", func(t *testing.T) {
		resetDBContext(t)
		t.Setenv("HOME", t.TempDir())
		t.Setenv("XDG_CONFIG_HOME", "")
		SetDBContextDir(t.TempDir())

		_, _, err := ChildDBRoute()
		if !errors.Is(err, ErrNoChildDBRoute) {
			t.Errorf("ChildDBRoute() error = %v, want ErrNoChildDBRoute", err)
		}
	})
}
