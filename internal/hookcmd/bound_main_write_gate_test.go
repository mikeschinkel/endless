package hookcmd

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// boundMainWriteChildEnv marks the re-exec'd child of
// TestWorktreeGate_RefusesBoundSessionWriteInMain.
const boundMainWriteChildEnv = "ENDLESS_TEST_BOUND_MAIN_WRITE_CHILD"

// TestWorktreeGate_RefusesBoundSessionWriteInMain is P17 of E-2291: binding is
// the step before a claim, and it opens no new way to write. A session bound
// to an unplanned task has no worktree — bind creates none — so its Write in
// the main checkout meets the same refusal as any session's.
//
// The refusal exits the process (refusal.Block), so the gate runs in a
// re-exec'd child and the parent asserts its exit code and stderr.
func TestWorktreeGate_RefusesBoundSessionWriteInMain(t *testing.T) {
	if os.Getenv(boundMainWriteChildEnv) == "1" {
		runBoundMainWriteChild(t)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWorktreeGate_RefusesBoundSessionWriteInMain$")
	cmd.Env = append(os.Environ(), boundMainWriteChildEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 2 {
		t.Fatalf("gate did not block (err=%v); stderr:\n%s", err, stderr.String())
	}
	if !strings.Contains(stderr.String(), "edits in the main checkout are refused") {
		t.Errorf("blocked, but not by the main-checkout refusal:\n%s", stderr.String())
	}
}

func runBoundMainWriteChild(t *testing.T) {
	db := newBindTestDB(t)
	projectRoot := t.TempDir()
	if out, err := exec.Command("git", "-C", projectRoot, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	seedProjectAndTasks(t, db, projectRoot, 2291)
	if _, err := db.Exec(`UPDATE tasks SET status = 'unplanned' WHERE id = 2291`); err != nil {
		t.Fatalf("set unplanned: %v", err)
	}
	t.Setenv("TMUX_PANE", "")
	if err := monitor.TouchSession("sess-bound", "claude", "", 1); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	if err := monitor.BindSessionToTask("sess-bound", 1, 2291); err != nil {
		t.Fatalf("BindSessionToTask: %v", err)
	}

	enforceWorktreeGate(1, writePayload("sess-bound", projectRoot))
	// Reaching here means the gate let the write through; exit 0 so the parent
	// reports that rather than a test-framework failure.
	os.Exit(0)
}
