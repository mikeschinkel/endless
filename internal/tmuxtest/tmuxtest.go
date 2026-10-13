// Package tmuxtest gives a Go test a tmux socket directory of its own, so the
// scratch servers tests start never land in the shared system socket directory
// and never reach the server the person running the tests is sitting in.
//
// Only tests import it. Endless's monitor scans every socket in the shared
// directory on each liveness tick, so a test that leaves one behind there
// leaves it for good (E-2310). TestTmuxTestsUsePrivate is the guard.
package tmuxtest

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// tmpRoot is where the private directory is made. NOT the OS temp dir: on
// macOS that is /var/folders/…/T/, and the socket path tmux builds under it
// (<dir>/tmux-<uid>/<name>) can pass the ~104-byte limit on a Unix socket path.
// internal/verifycmd's tmuxTmpRoot makes the same choice for the same reason.
const tmpRoot = "/tmp"

// Private points tmux at a fresh, empty socket directory for the rest of the
// test: it sets TMUX_TMPDIR and unsets TMUX and TMUX_PANE, so `tmux -L <name>`
// resolves inside that directory and a bare `tmux` cannot reach the caller's
// server. On cleanup it kills every server started there and removes the
// directory.
//
// It uses t.Setenv, so a test calling it cannot be parallel.
func Private(t testing.TB) {
	t.Helper()
	dir, err := os.MkdirTemp(tmpRoot, "endless-tmux-")
	if err != nil {
		t.Fatalf("tmuxtest: make private tmux dir: %v", err)
	}
	t.Setenv("TMUX_TMPDIR", dir)
	for _, name := range []string{"TMUX", "TMUX_PANE"} {
		t.Setenv(name, "") // registers the restore
		_ = os.Unsetenv(name)
	}
	t.Cleanup(func() {
		socks, _ := filepath.Glob(filepath.Join(dir, "tmux-*", "*"))
		for _, sock := range socks {
			_ = exec.Command("tmux", "-S", sock, "kill-server").Run()
		}
		_ = os.RemoveAll(dir)
	})
}
