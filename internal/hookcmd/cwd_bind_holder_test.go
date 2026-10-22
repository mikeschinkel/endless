package hookcmd

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// The SessionStart cwd auto-bind continues a task only from its earlier session
// (E-2418). Before, it was guarded by the worktree lock alone, and only a
// session that STARTED in the worktree takes that lock — so a session that
// claimed from the main checkout held the task without it, and a second Claude
// started in the worktree bound to the same task. Every case below binds when
// the task has no other holder; what differs is who else holds it.

const heldTask = 2418

// holderFixture is one project with task E-2418's worktree, a tmux server whose
// panes %1 (another pane) and %2 (this session's) are both observed live, and a
// fault store to read back. Returns the database and the worktree path.
func holderFixture(t *testing.T) (*sql.DB, string) {
	t.Helper()
	db := newBindTestDB(t)
	projectRoot := t.TempDir()
	seedProjectAndTasks(t, db, projectRoot, heldTask)
	wt := seedWorktree(t, projectRoot, heldTask)
	t.Cleanup(monitor.SetTestTmuxObservation("srv", map[string]string{
		"%1": "claude", "%2": "claude",
	}))
	t.Setenv("CLAUDE_JOB_DIR", "")
	t.Setenv("TMUX_PANE", "%2")
	t.Setenv(newTranscriptEnv, "")
	bindFaultStore(t)
	return db, wt
}

// seedHolder records session guid as E-2418's holder in pane (empty for none),
// ended when ended is set.
func seedHolder(t *testing.T, guid, pane string, ended bool) {
	t.Helper()
	if err := monitor.TouchSession(guid, "claude", pane, 1); err != nil {
		t.Fatalf("touch %s: %v", guid, err)
	}
	t.Setenv("TMUX_PANE", pane)
	err := monitor.BindSessionToTask(guid, 1, heldTask)
	t.Setenv("TMUX_PANE", "%2")
	if err != nil {
		t.Fatalf("bind %s: %v", guid, err)
	}
	if ended {
		if err := monitor.EndSession(guid); err != nil {
			t.Fatalf("end %s: %v", guid, err)
		}
	}
}

// startIn starts session guid in pane %2 with the given SessionStart source and
// runs the auto-bind on it.
func startIn(t *testing.T, wt, guid, source string) *cwdBindRefusal {
	t.Helper()
	if err := monitor.TouchSession(guid, "claude", "%2", 1); err != nil {
		t.Fatalf("touch %s: %v", guid, err)
	}
	return maybeCwdBind(1, claudePayload{SessionID: guid, CWD: wt, Source: source})
}

func boundTo(t *testing.T, db *sql.DB, guid string) int64 {
	t.Helper()
	id := sessionTaskID(t, db, guid)
	if id == nil {
		return 0
	}
	return *id
}

func taskHeldIncidents(t *testing.T) int {
	t.Helper()
	incidents, err := faults.List(faults.AllProjects, false, 0)
	if err != nil {
		t.Fatalf("list faults: %v", err)
	}
	n := 0
	for _, i := range incidents {
		if i.Code == faults.ErrCodeTaskHeldElsewhere.ID {
			n++
		}
	}
	return n
}

// P5: a present holder in another pane — the defect itself.
func TestCwdBind_PresentHolderInAnotherPaneIsRefused(t *testing.T) {
	for _, source := range []string{"startup", "resume", "clear", "compact"} {
		t.Run(source, func(t *testing.T) {
			db, wt := holderFixture(t)
			seedHolder(t, "holder", "%1", false)

			refused := startIn(t, wt, "second", source)

			if got := boundTo(t, db, "second"); got != 0 {
				t.Fatalf("second session bound to E-%d while another session holds it", got)
			}
			if refused == nil {
				t.Fatal("no refusal: neither the agent nor the user was told")
			}
			for name, text := range map[string]string{"agent": refused.agent, "user": refused.user} {
				if !strings.Contains(text, "endless session goto E-2418`") {
					t.Errorf("%s text does not name `endless session goto E-2418`: %q", name, text)
				}
				if !strings.Contains(text, "close this session") {
					t.Errorf("%s text does not say to close this session: %q", name, text)
				}
				if !strings.Contains(text, "%1") || !strings.Contains(text, "ES-") {
					t.Errorf("%s text does not name the holder and its pane: %q", name, text)
				}
			}
			if n := taskHeldIncidents(t); n != 1 {
				t.Errorf("recorded %d task-held-elsewhere faults, want 1", n)
			}
		})
	}
}

// The refused session gives back the worktree lock SessionStart's adoption gave
// it, or the session the user is sent to would be refused by that lock.
func TestCwdBind_ARefusedSessionReleasesItsWorktreeLock(t *testing.T) {
	_, wt := holderFixture(t)
	seedHolder(t, "holder", "%1", true)
	if err := monitor.ClaimWorktreeLock(wt, monitor.WorktreeLock{
		SessionID: "second", PID: 1, ClaimedAt: "2026-10-22T00:00:00Z",
	}); err != nil {
		t.Fatalf("seed lock: %v", err)
	}

	if startIn(t, wt, "second", "startup") == nil {
		t.Fatal("expected a refusal")
	}
	if lock, _ := monitor.ReadWorktreeLock(wt); lock != nil {
		t.Errorf("the refused session still holds the worktree lock: %+v", lock)
	}
}

// P6: a fresh `claude` started by hand where an ended session held the task.
func TestCwdBind_FreshStartAfterAnEndedHolderIsRefused(t *testing.T) {
	db, wt := holderFixture(t)
	seedHolder(t, "holder", "%1", true)

	refused := startIn(t, wt, "fresh", "startup")

	if got := boundTo(t, db, "fresh"); got != 0 {
		t.Fatalf("fresh session bound to E-%d although an ended session held it", got)
	}
	if refused == nil {
		t.Fatal("no refusal")
	}
	for name, text := range map[string]string{"agent": refused.agent, "user": refused.user} {
		if !strings.Contains(text, "endless session goto E-2418 --resume") {
			t.Errorf("%s text does not name goto --resume: %q", name, text)
		}
	}
	if n := taskHeldIncidents(t); n != 0 {
		t.Errorf("recorded %d faults; a user starting claude by hand is not a defect", n)
	}
}

// P6 / P7: `--new-transcript` marks its launch, and only for its own task.
func TestCwdBind_NewTranscriptMarkAllowsTheFreshStart(t *testing.T) {
	db, wt := holderFixture(t)
	seedHolder(t, "holder", "%1", true)
	t.Setenv(newTranscriptEnv, "2418")

	if refused := startIn(t, wt, "fresh", "startup"); refused != nil {
		t.Fatalf("refused a --new-transcript launch: %+v", refused)
	}
	if got := boundTo(t, db, "fresh"); got != heldTask {
		t.Fatalf("bound to E-%d, want E-%d", got, heldTask)
	}
}

func TestCwdBind_AMarkForAnotherTaskDoesNotCount(t *testing.T) {
	_, wt := holderFixture(t)
	seedHolder(t, "holder", "%1", true)
	t.Setenv(newTranscriptEnv, "999")

	if startIn(t, wt, "fresh", "startup") == nil {
		t.Fatal("a mark naming E-999 let a fresh start take E-2418")
	}
}

// P8: a resume, /clear or /compact continues a conversation line.
func TestCwdBind_NonStartupSourcesBindPastAnEndedHolder(t *testing.T) {
	for _, source := range []string{"resume", "clear", "compact"} {
		t.Run(source, func(t *testing.T) {
			db, wt := holderFixture(t)
			seedHolder(t, "holder", "%1", true)

			if refused := startIn(t, wt, "next", source); refused != nil {
				t.Fatalf("refused: %+v", refused)
			}
			if got := boundTo(t, db, "next"); got != heldTask {
				t.Fatalf("bound to E-%d, want E-%d", got, heldTask)
			}
		})
	}
}

// P8: the earlier holder in this same pane is this pane's own previous session.
func TestCwdBind_AHolderInTheSamePaneIsPassedOver(t *testing.T) {
	for name, ended := range map[string]bool{"ended": true, "present": false} {
		t.Run(name, func(t *testing.T) {
			db, wt := holderFixture(t)
			seedHolder(t, "previous", "%2", ended)

			if refused := startIn(t, wt, "next", "startup"); refused != nil {
				t.Fatalf("refused: %+v", refused)
			}
			if got := boundTo(t, db, "next"); got != heldTask {
				t.Fatalf("bound to E-%d, want E-%d", got, heldTask)
			}
		})
	}
}

// P9: a session `task spawn` started. Spawn refuses a task anyone held, so the
// spawned session finds no holder at all.
func TestCwdBind_ASpawnedSessionStillBinds(t *testing.T) {
	db, wt := holderFixture(t)

	if refused := startIn(t, wt, "spawned", "startup"); refused != nil {
		t.Fatalf("refused: %+v", refused)
	}
	if got := boundTo(t, db, "spawned"); got != heldTask {
		t.Fatalf("bound to E-%d, want E-%d", got, heldTask)
	}
}

// A session that already holds the task is re-affirming its own binding, even
// with another holder about.
func TestCwdBind_TheHoldersOwnRestartIsNotRefused(t *testing.T) {
	_, wt := holderFixture(t)
	seedHolder(t, "other", "%1", false)
	seedHolder(t, "mine", "%2", false)

	if refused := startIn(t, wt, "mine", "startup"); refused != nil {
		t.Fatalf("refused the holder itself: %+v", refused)
	}
}

// A present holder with no pane binding can be shown neither gone nor elsewhere,
// so it is read as ended: refused on a fresh start, with no fault.
func TestCwdBind_APanelessHolderIsTakenAsEnded(t *testing.T) {
	_, wt := holderFixture(t)
	seedHolder(t, "paneless", "", false)

	if startIn(t, wt, "fresh", "startup") == nil {
		t.Fatal("expected the ended-holder refusal")
	}
	if n := taskHeldIncidents(t); n != 0 {
		t.Errorf("recorded %d faults for a paneless holder", n)
	}
	if refused := startIn(t, wt, "cleared", "clear"); refused != nil {
		t.Fatalf("a /clear past a paneless holder was refused: %+v", refused)
	}
}

// The refusal reaches both audiences: additionalContext nested under
// hookSpecificOutput for the agent (contextInjection's one accepted shape,
// E-2001), and the top-level systemMessage for the user.
func TestCwdBind_TheRefusalCarriesBothChannels(t *testing.T) {
	out, err := json.Marshal(sessionStartRefusal{
		HookSpecificOutput: hookContextOutput{HookEventName: "SessionStart", AdditionalContext: "to the agent"},
		SystemMessage:      "to the user",
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	hso, _ := got["hookSpecificOutput"].(map[string]any)
	if hso["hookEventName"] != "SessionStart" || hso["additionalContext"] != "to the agent" {
		t.Errorf("hookSpecificOutput = %v", hso)
	}
	if got["systemMessage"] != "to the user" {
		t.Errorf("systemMessage = %v", got["systemMessage"])
	}
}
