package eventcmd

import (
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestLedgerRoot covers E-1729's routing helper: in an active sandbox the
// ledger follows the sandbox config dir (never the passed project root); in a
// plain (main) context it stays the project root. Sandbox detection asks
// monitor's resolver whether ConfigDir() IS a worktree's sandbox (E-1964),
// which these cases toggle by pointing XDG_CONFIG_HOME at one.
func TestLedgerRoot(t *testing.T) {
	const projectRoot = "/real/checkout"

	t.Run("sandbox context routes to the sandbox config dir", func(t *testing.T) {
		// ConfigDir() = XDG_CONFIG_HOME/endless, which must BE the sandbox of
		// the worktree it sits in for IsSandboxActive() to fire.
		wt := filepath.Join(t.TempDir(), ".endless", "worktrees", "e-9999")
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(wt, ".endless", "sandbox"))

		if !monitor.IsSandboxActive() {
			t.Fatal("precondition: expected sandbox routing to be active")
		}
		got := ledgerRoot(projectRoot)
		if got != monitor.ConfigDir() {
			t.Errorf("ledgerRoot(%q) = %q, want ConfigDir() %q", projectRoot, got, monitor.ConfigDir())
		}
		if got == projectRoot {
			t.Errorf("ledgerRoot(%q) must NOT return the project root in a sandbox", projectRoot)
		}
	})

	t.Run("main context returns the project root unchanged", func(t *testing.T) {
		config := filepath.Join(t.TempDir(), "myconfig")
		t.Setenv("XDG_CONFIG_HOME", config)

		if monitor.IsSandboxActive() {
			t.Fatal("precondition: expected no sandbox routing")
		}
		if got := ledgerRoot(projectRoot); got != projectRoot {
			t.Errorf("ledgerRoot(%q) = %q, want the project root unchanged", projectRoot, got)
		}
	})
}
