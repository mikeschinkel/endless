package sessionquerycmd

import (
	"database/sql"
	"os/exec"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// TestReapDeadPanes_BinaryEndsGhostOwner pins E-1807's Go half: the
// `reap-dead-panes` verb flips a non-ended session whose owning tmux pane is
// gone to `ended` and NULLs its `process`, matching monitor.ReapDeadTmuxPanes.
// This is the self-heal the Python spawn/claim ownership guard leans on.
//
// Skips when `tmux` isn't on $PATH: the reaper opportunistically shells to
// `tmux list-panes` and bails as a no-op without it, so the row would stay
// non-ended and there'd be nothing to assert (mirrors the monitor reap test).
func TestReapDeadPanes_BinaryEndsGhostOwner(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available; reaper is a no-op")
	}
	cfgDir := t.TempDir()
	projectPath := t.TempDir()
	// A synthetic high-index pane id that cannot match any live pane on the
	// host, so the reaper sees it as dead and ends the row.
	const deadPane = "%999993"
	seedLiveSessionsDB(t, cfgDir, projectPath, []sessionSeed{
		{"ghost-owner", "working", deadPane},
	})

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "reap-dead-panes", "--project-root", projectPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("binary exec failed: %v\nout: %s", err, out)
	}
	// Success emits nothing (the guard invokes this only for its side effect).
	if len(out) != 0 {
		t.Errorf("expected no output on success, got: %s", out)
	}

	dbPath := filepath.Join(cfgDir, "endless.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer db.Close()
	var state string
	var process *string
	if err := db.QueryRow(
		"SELECT state, process FROM sessions WHERE session_id = ?", "ghost-owner",
	).Scan(&state, &process); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if state != "ended" {
		t.Errorf("state = %q, want ended", state)
	}
	if process != nil {
		t.Errorf("process = %q, want NULL after reap", *process)
	}
}

// TestReapDeadPanes_BinaryMissingFlagExitsNonZero pins the input-validation
// contract: omitting --project-root is a usage error (exit non-zero).
func TestReapDeadPanes_BinaryMissingFlagExitsNonZero(t *testing.T) {
	cfgDir := t.TempDir()
	seedLiveSessionsDB(t, cfgDir, t.TempDir(), nil)

	bin := endlessGoBin(t)
	cmd := exec.Command(bin, "--config-dir", cfgDir,
		"session-query", "reap-dead-panes")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit when --project-root is omitted, got success\nout: %s", out)
	}
}
