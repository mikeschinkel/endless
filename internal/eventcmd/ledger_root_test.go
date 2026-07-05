package eventcmd

import (
	"path/filepath"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestLedgerRoot covers E-1729's routing helper: in an active sandbox the
// ledger follows the sandbox config dir (never the passed project root); in a
// plain (main) context it stays the project root. Sandbox detection is driven
// entirely by ConfigDir()-under-CacheDir()/sandboxes (ED-1528), which these
// cases toggle via XDG_CONFIG_HOME / XDG_CACHE_HOME.
func TestLedgerRoot(t *testing.T) {
	const projectRoot = "/real/checkout"

	t.Run("sandbox context routes to the sandbox config dir", func(t *testing.T) {
		cache := t.TempDir()
		// ConfigDir() = XDG_CONFIG_HOME/endless, which must land under
		// CacheDir()/sandboxes for IsSandboxActive() to fire.
		t.Setenv("XDG_CACHE_HOME", cache)
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(cache, "endless", "sandboxes", "e-9999"))

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
		cache := t.TempDir()
		config := filepath.Join(t.TempDir(), "myconfig")
		t.Setenv("XDG_CACHE_HOME", cache)
		t.Setenv("XDG_CONFIG_HOME", config)

		if monitor.IsSandboxActive() {
			t.Fatal("precondition: expected no sandbox routing")
		}
		if got := ledgerRoot(projectRoot); got != projectRoot {
			t.Errorf("ledgerRoot(%q) = %q, want the project root unchanged", projectRoot, got)
		}
	})
}
