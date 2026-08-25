// Tests for epic_id population on interactive task claim/release
// (E-1624). execTaskClaimed must set sessions.epic_id to the nearest
// type='epic' ancestor of the claimed task (the task itself if it is an epic,
// NULL if none), and execTaskReleased must clear it alongside task_id.
package events

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

// newClaimTestDB stands up a schema-applied SQLite DB with a seeded project.
func newClaimTestDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	// Route ConfigDir() (and thus the machine-local diagnostic log written by
	// the claim/release executors) to an isolated temp dir so tests never append
	// to the developer's real ~/.config/endless log.
	t.Cleanup(monitor.SetTestDB(db))
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-06-23T00:00:00', '2026-06-23T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

// seedClaimSession inserts one live session row (sessions.id == id).
func seedClaimSession(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, session_id, project_id, state, started_at)
		 VALUES (?, ?, 1, 'working', '2026-06-23T00:00:00')`,
		id, "sess-"+strconv.FormatInt(id, 10),
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

// claimEvent builds a task.claimed event for taskID by sessionID (sessions.id).
func claimEvent(t *testing.T, taskID, sessionID int64) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskClaimedPayload{SessionID: sessionID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-06-23T00:00:00",
		Kind:    KindTaskClaimed,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Payload: payload,
	}
}

// releaseEvent builds a task.released event for taskID by sessionID.
func releaseEvent(t *testing.T, taskID, sessionID int64) *Event {
	t.Helper()
	payload, err := json.Marshal(TaskReleasedPayload{SessionID: sessionID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-06-23T00:00:01",
		Kind:    KindTaskReleased,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: strconv.FormatInt(taskID, 10)},
		Payload: payload,
	}
}

// sessionBinding reads a session's task_id and epic_id.
func sessionBinding(t *testing.T, db *sql.DB, sessionID int64) (taskID, epicID sql.NullInt64) {
	t.Helper()
	if err := db.QueryRow(
		"SELECT task_id, epic_id FROM sessions WHERE id = ?", sessionID,
	).Scan(&taskID, &epicID); err != nil {
		t.Fatalf("read session %d: %v", sessionID, err)
	}
	return taskID, epicID
}

// TestClaim_ChildOfEpicSetsEpicID: claiming a child of an epic sets
// epic_id to the epic and task_id to the child.
func TestClaim_ChildOfEpicSetsEpicID(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "underway")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	taskID, epicID := sessionBinding(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("task_id = %v, want 100", taskID)
	}
	if !epicID.Valid || epicID.Int64 != 1 {
		t.Errorf("epic_id = %v, want 1 (epic ancestor)", epicID)
	}
}

// TestClaim_NestedChildResolvesNearestEpic: a grand-child under epic(1) ->
// epic(2) -> task(100) resolves to the NEAREST epic (2).
func TestClaim_NestedChildResolvesNearestEpic(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "underway")
	seedTask(t, db, 2, ptr(1), int(tasktype.TaskTypeEpic), "underway")
	seedTask(t, db, 100, ptr(2), int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	_, epicID := sessionBinding(t, db, 42)
	if !epicID.Valid || epicID.Int64 != 2 {
		t.Errorf("epic_id = %v, want 2 (nearest epic ancestor)", epicID)
	}
}

// TestClaim_StandaloneTaskClearsEpicID: claiming a task with no epic ancestor
// leaves epic_id NULL.
func TestClaim_StandaloneTaskNullEpicID(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 100, nil, int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	taskID, epicID := sessionBinding(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("task_id = %v, want 100", taskID)
	}
	if epicID.Valid {
		t.Errorf("epic_id = %v, want NULL (no epic ancestor)", epicID)
	}
}

// TestClaim_EpicDirectlyResolvesToSelf: claiming an epic directly sets
// epic_id to the epic's own id (depth-0 inclusion).
func TestClaim_EpicDirectlyResolvesToSelf(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "underway")

	if _, err := execTaskClaimed(db, claimEvent(t, 1, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	taskID, epicID := sessionBinding(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 1 {
		t.Errorf("task_id = %v, want 1", taskID)
	}
	if !epicID.Valid || epicID.Int64 != 1 {
		t.Errorf("epic_id = %v, want 1 (epic resolves to self)", epicID)
	}
}

// TestClaim_RefusesRepointingABoundSession replaces the former
// TestClaim_ClearsStaleEpicFromPriorClaim.
//
// That test claimed an epic child and then a standalone task on the SAME
// session, to prove the second claim cleared the stale epic_id. Under ED-1560
// (enforced by E-1969's write-once trigger) a session never holds a second task,
// so the state it described is unreachable and the clearing it pinned can never
// run. What replaces it is the refusal itself: the second claim aborts, and
// BOTH columns keep the first claim's values — a half-applied re-claim that left
// the task behind but moved the epic would be worse than either outcome.
//
// The "standalone task leaves epic_id NULL" half is still covered, on a fresh
// session, by TestClaim_StandaloneTaskLeavesEpicNull above.
func TestClaim_RefusesRepointingABoundSession(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "underway")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "ready")
	seedTask(t, db, 200, nil, int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("claim epic child: %v", err)
	}
	if _, epicID := sessionBinding(t, db, 42); !epicID.Valid || epicID.Int64 != 1 {
		t.Fatalf("setup: epic_id = %v, want 1", epicID)
	}

	_, err := execTaskClaimed(db, claimEvent(t, 200, 42))
	if err == nil {
		t.Fatal("claiming a second task succeeded, want write-once abort")
	}
	if !strings.Contains(err.Error(), "write-once") {
		t.Errorf("error = %v, want it to name the write-once constraint", err)
	}

	taskID, epicID := sessionBinding(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("task_id = %v, want 100 (refused claim must not move it)", taskID)
	}
	if !epicID.Valid || epicID.Int64 != 1 {
		t.Errorf("epic_id = %v, want 1 (refused claim must not move it either)", epicID)
	}
}

// TestClaim_ReaffirmingTheSameTaskIsAllowed: write-once forbids a CHANGE, not a
// repeat. The claim executor is idempotent — a re-emitted task.claimed, or the
// hook's defense-in-depth re-bind, names the value already there and passes the
// trigger's `NEW.task_id IS NOT OLD.task_id` test.
func TestClaim_ReaffirmingTheSameTaskIsAllowed(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "underway")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("re-claim of the same task: %v", err)
	}

	taskID, epicID := sessionBinding(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("task_id = %v, want 100", taskID)
	}
	if !epicID.Valid || epicID.Int64 != 1 {
		t.Errorf("epic_id = %v, want 1", epicID)
	}
}

// TestClaim_RevivesEndedSession (E-1686): binding a task to a session whose row
// is 'ended' must lift it back to a live state, so `task bind` can't silently
// land on (and report success against) a row that every `state != 'ended'`
// reader hides. Mirrors the TouchSession revival on the bind path.
func TestClaim_RevivesEndedSession(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	if _, err := db.Exec(
		"UPDATE sessions SET state='ended' WHERE id=?", int64(42),
	); err != nil {
		t.Fatalf("force ended: %v", err)
	}
	seedTask(t, db, 100, nil, int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	var state string
	if err := db.QueryRow(
		"SELECT state FROM sessions WHERE id=?", int64(42),
	).Scan(&state); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state != "needs_input" {
		t.Errorf("state = %q, want needs_input (bind didn't revive ended row)", state)
	}
	if taskID, _ := sessionBinding(t, db, 42); !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("task_id = %v, want 100", taskID)
	}
}

// TestClaim_PreservesLiveSessionState (E-1686 guard): claiming onto a live
// session must NOT rewrite its state — only an 'ended' row is revived.
func TestClaim_PreservesLiveSessionState(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42) // seeded 'working'
	seedTask(t, db, 100, nil, int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("execTaskClaimed: %v", err)
	}

	var state string
	if err := db.QueryRow(
		"SELECT state FROM sessions WHERE id=?", int64(42),
	).Scan(&state); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state != "working" {
		t.Errorf("state = %q, want working (claim clobbered a live state)", state)
	}
}

// TestRelease_IsRefusedByWriteOnce replaces the former
// TestRelease_ClearsBothTaskAndEpic (and TestRelease_LogsClear, folded in here).
//
// Release used to NULL both columns. ED-1560 makes sessions.task_id write-once —
// never cleared, not just never repointed — so the release executor now aborts
// on contact. That is not a regression to route around: E-1968 left it no live
// producer, and it survives only to replay historical ledger entries. The
// projector has no case for task.claimed/task.released either, so `rebuild-db`
// never reaches it. This pins the abort, that the binding survives it, and that
// nothing is written to the diagnostic log for a release that did not happen.
func TestRelease_IsRefusedByWriteOnce(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "underway")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err := execTaskReleased(db, releaseEvent(t, 100, 42))
	if err == nil {
		t.Fatal("release succeeded, want write-once abort")
	}
	if !strings.Contains(err.Error(), "write-once") {
		t.Errorf("error = %v, want it to name the write-once constraint", err)
	}

	taskID, epicID := sessionBinding(t, db, 42)
	if !taskID.Valid || taskID.Int64 != 100 {
		t.Errorf("task_id = %v, want 100 (refused release must not clear it)", taskID)
	}
	if !epicID.Valid || epicID.Int64 != 1 {
		t.Errorf("epic_id = %v, want 1 (refused release must not clear it either)", epicID)
	}

	for _, e := range readDiagLog(t) {
		if e["reason"] == "release" {
			t.Errorf("diagnostic log has a release line for a release that aborted: %v", e)
		}
	}
}

// readDiagLog decodes every line of the machine-local diagnostic log at the
// current (test-isolated) ConfigDir(). Absent file → nil.
func readDiagLog(t *testing.T) []map[string]any {
	t.Helper()
	f, err := os.Open(filepath.Join(monitor.ConfigDir(), "log", "user-machine.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("open diag log: %v", err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("decode diag line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

// TestClaim_LogsBinding (E-1857, narrowed by E-1969): the claim executor records
// the task_id transition in the diagnostic log.
//
// It used to pin the 100 -> 200 REBIND line, which write-once now makes
// unreachable — so what is pinned is the transition that still happens: the
// first bind, NULL -> 100. The JSON keys are `old_task_id` / `new_task_id`
// (E-1969 renamed them from `old_active_task_id` / `new_active_task_id` with the
// column; older lines in an existing log keep the old spelling, and nothing
// reconciles that because nothing parses this file).
//
// The refused second claim writes NO line, which is the property worth having:
// the log records what the database did, and the database did nothing.
func TestClaim_LogsBinding(t *testing.T) {
	db := newClaimTestDB(t)
	seedClaimSession(t, db, 42)
	seedTask(t, db, 100, nil, int(tasktype.TaskTypeTask), "ready")
	seedTask(t, db, 200, nil, int(tasktype.TaskTypeTask), "ready")

	if _, err := execTaskClaimed(db, claimEvent(t, 100, 42)); err != nil {
		t.Fatalf("claim 100: %v", err)
	}
	if _, err := execTaskClaimed(db, claimEvent(t, 200, 42)); err == nil {
		t.Fatal("claim 200 succeeded, want write-once abort")
	}

	entries := readDiagLog(t)
	if len(entries) != 1 {
		t.Fatalf("diag entries = %d, want 1 (the refused claim must log nothing): %v",
			len(entries), entries)
	}
	bind := entries[0]
	if bind["reason"] != "claim-event" {
		t.Errorf("reason = %v, want claim-event", bind["reason"])
	}
	if _, present := bind["old_task_id"]; present {
		t.Errorf("old_task_id present = %v, want omitted (was NULL)", bind["old_task_id"])
	}
	if bind["new_task_id"] != float64(100) {
		t.Errorf("new_task_id = %v, want 100", bind["new_task_id"])
	}
	if bind["session_id"] != "sess-42" {
		t.Errorf("session_id = %v, want sess-42", bind["session_id"])
	}
}
