package hookcmd

import (
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// stubWindow fakes the two tmux window reads markAutoSpawned consults.
func stubWindow(t *testing.T, autoSpawned bool, windowTask int64) {
	t.Helper()
	prevAuto, prevTask := tmuxWindowAutoSpawned, tmuxTaskID
	tmuxWindowAutoSpawned = func() bool { return autoSpawned }
	tmuxTaskID = func() int64 { return windowTask }
	t.Cleanup(func() { tmuxWindowAutoSpawned, tmuxTaskID = prevAuto, prevTask })
}

// TestAutoBindFromCwd_MarksAutoSpawned covers E-1814's provenance write and the
// E-1983 guard on it. A session is flagged auto-spawned only when its window
// carries the marker AND names the task the session just bound to: a window
// outlives its session, so the marker alone would flag whatever session started
// in that window next.
func TestAutoBindFromCwd_MarksAutoSpawned(t *testing.T) {
	cases := []struct {
		name       string
		marker     bool
		windowTask int64
		want       int
	}{
		{"auto-spawned window for this task", true, 4101, 1},
		{"auto-spawned window for a different task (stale window)", true, 4999, 0},
		{"ordinary window", false, 4101, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newBindTestDB(t)
			projectRoot := t.TempDir()
			if _, err := db.Exec("INSERT INTO projects (id, name, path) VALUES (1, 'p', ?)", projectRoot); err != nil {
				t.Fatalf("seed project: %v", err)
			}
			if _, err := db.Exec("INSERT INTO tasks (id, project_id, title, status) VALUES (4101, 1, 't', 'underway')"); err != nil {
				t.Fatalf("seed task: %v", err)
			}
			wt := seedWorktree(t, projectRoot, 4101)
			t.Setenv("CLAUDE_JOB_DIR", "")
			t.Setenv("TMUX_PANE", "")
			stubWindow(t, tc.marker, tc.windowTask)

			maybeCwdBind(1, claudePayload{SessionID: "sess-auto", CWD: wt})

			var taskID int64
			var got int
			if err := db.QueryRow(
				"SELECT task_id, auto_spawned FROM sessions WHERE session_id = 'sess-auto'",
			).Scan(&taskID, &got); err != nil {
				t.Fatalf("read session: %v", err)
			}
			if taskID != 4101 {
				t.Fatalf("task_id = %d, want 4101 (the bind itself must not depend on the marker)", taskID)
			}
			if got != tc.want {
				t.Errorf("auto_spawned = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestMarkSessionAutoSpawned_Idempotent: the setter may run on every
// SessionStart of the same session (resume, /clear) without harm.
func TestMarkSessionAutoSpawned_Idempotent(t *testing.T) {
	db := newBindTestDB(t)
	if _, err := db.Exec("INSERT INTO sessions (session_id) VALUES ('s1')"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for range 2 {
		if err := monitor.MarkSessionAutoSpawned("s1"); err != nil {
			t.Fatalf("MarkSessionAutoSpawned: %v", err)
		}
	}
	var got int
	if err := db.QueryRow("SELECT auto_spawned FROM sessions WHERE session_id = 's1'").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Errorf("auto_spawned = %d, want 1", got)
	}
}
