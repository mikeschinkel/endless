package resumewindowscmd

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
	"github.com/mikeschinkel/endless/internal/tmuxservers"
)

const testTasksSocket = "/tmp/endless-test/endless-tasks"

// stubSweep stands in for the task-session server: running or not, and one
// window E-7 whose only pane is %3. It records the socket every tmux call was
// addressed to.
func stubSweep(t *testing.T, running bool) *[]string {
	t.Helper()
	prevOut, prevRunning, prevIn := tmuxOut, runningTasks, inTasksServer
	t.Cleanup(func() { tmuxOut, runningTasks, inTasksServer = prevOut, prevRunning, prevIn })
	runningTasks = func() (tmuxservers.Server, bool) {
		if !running {
			return tmuxservers.Server{}, false
		}
		return tmuxservers.Server{Role: tmuxservers.RoleTasks, Socket: testTasksSocket, Session: "tasks"}, true
	}
	inTasksServer = tmuxservers.InTasksServer
	var sockets []string
	tmuxOut = func(args ...string) (string, error) {
		sockets = append(sockets, socket)
		switch args[0] {
		case "list-windows":
			return "@1\ttasks\t1\tE-7\n", nil
		case "list-panes":
			return "@1\t%3\tzsh\t/p\n", nil
		}
		t.Errorf("unexpected tmux call %q", args)
		return "", nil
	}
	return &sockets
}

// TestRun_RefusesWhileEndlessIsNotRunning: with the task-session server down
// the sweep refuses with the not-running message and asks no tmux server
// anything — not the caller's, not the default one (E-2644).
func TestRun_RefusesWhileEndlessIsNotRunning(t *testing.T) {
	sockets := stubSweep(t, false)
	var out, errb strings.Builder
	if code := run([]string{"--all-tmux-sessions"}, &out, &errb); code != 1 {
		t.Errorf("run = %d, want 1", code)
	}
	if !strings.Contains(errb.String(), spawnlaunchcmd.NotRunningMessage) {
		t.Errorf("stderr = %q, want the not-running message", errb.String())
	}
	if len(*sockets) != 0 {
		t.Errorf("tmux was asked %d times with Endless down", len(*sockets))
	}
	if out.Len() != 0 {
		t.Errorf("stdout = %q, want nothing", out.String())
	}
}

// TestRun_SweepsTheTasksServerFromOutsideIt: typed in the console, the sweep
// lists the task-session server's windows by its socket, and finds a session
// live in pane %3 THERE — not the console's %3 — so it types nothing over a
// running Claude (E-2644).
func TestRun_SweepsTheTasksServerFromOutsideIt(t *testing.T) {
	db := sweepDB(t)
	t.Cleanup(monitor.SetTestDB(db))
	t.Cleanup(monitor.SetTestTmuxServer(monitor.TestServerUUID))
	pid, err := monitor.SeedPaneProcess(db, monitor.TestServerUUID, "%3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO sessions (id, session_id, project_id, state, process_id, started_at)
		VALUES (41, 'sess-41', 1, 'working', ?, '2026-06-17T00:00:00')`, pid); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(monitor.SetTestCallerOutsideTasksServer("c0501e00"))
	t.Setenv("TMUX_PANE", "%3")
	sockets := stubSweep(t, true)

	var out, errb strings.Builder
	if code := run([]string{"--all-tmux-sessions", "--dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("run = %d: %s", code, errb.String())
	}
	for _, s := range *sockets {
		if s != testTasksSocket {
			t.Errorf("a tmux call went to %q, want the task-session socket", s)
		}
	}
	if !strings.Contains(out.String(), "already running Claude (ES-41)") {
		t.Errorf("summary = %q, want E-7 skipped as already running ES-41", out.String())
	}
	if socket != "" {
		t.Errorf("socket left set to %q after the sweep", socket)
	}
}

// TestRun_OwnPaneCountsOnlyInsideTheTasksServer: in the task-session server
// the caller's pane is its own window's, which the sweep never touches.
func TestRun_OwnPaneCountsOnlyInsideTheTasksServer(t *testing.T) {
	t.Cleanup(monitor.SetTestTmuxServer(monitor.TestServerUUID))
	t.Setenv("TMUX_PANE", "%3")
	stubSweep(t, true)

	var out, errb strings.Builder
	if code := run([]string{"--all-tmux-sessions", "--dry-run"}, &out, &errb); code != 0 {
		t.Fatalf("run = %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "this command is running in it") {
		t.Errorf("summary = %q, want E-7 skipped as the caller's own window", out.String())
	}
}

func sweepDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if err = schema.Migrate(db); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		VALUES (1, 'test', '/tmp/test', 'active', '2026-05-16T00:00:00', '2026-05-16T00:00:00')`); err != nil {
		t.Fatal(err)
	}
	return db
}
