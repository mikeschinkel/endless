package events

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// seedLiveTask inserts a live task so the membership executors' live_tasks check
// passes. `session task add` refuses ids that name no task, so every queued
// fixture needs a real row behind it.
func seedLiveTask(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	const ts = "2026-08-21T00:00:00"
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, title, status, phase, created_at, updated_at)
		 VALUES (?, 1, 'seeded', 'ready', 'now', ?, ?)`,
		id, ts, ts,
	); err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

func membershipEvent(t *testing.T, kind Kind, sessionID int64, taskIDs []string) *Event {
	t.Helper()
	sid := strconv.FormatInt(sessionID, 10)
	payload, err := json.Marshal(SessionTaskMembershipPayload{
		Process: sessionIDSentinelPrefix + sid,
		TaskIDs: taskIDs,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-08-21T00:00:01",
		Kind:    kind,
		Project: "test",
		Entity:  EntityRef{Type: EntitySessionTasks, ID: "0"},
		Actor:   Actor{Kind: ActorCLI, ID: "user@host", SessionID: sid},
		Payload: payload,
	}
}

func hasSessionTask(t *testing.T, db *sql.DB, sessionID, taskID int64) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM session_tasks WHERE session_id = ? AND task_id = ?`,
		sessionID, taskID,
	).Scan(&n); err != nil {
		t.Fatalf("count session_tasks: %v", err)
	}
	return n > 0
}

// TestSessionTasksQueued_AddsAndPromotes covers the two things `session task
// add` is for: enrolling a task the session has never touched (the case no
// automatic capture can reach, since nothing happened to it), and strengthening
// one it merely touched.
func TestSessionTasksQueued_AddsAndPromotes(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	seedLiveTask(t, db, 101)

	// 101 is already revisited; 100 is untouched.
	if err := upsertSessionTask(db, "42", 101, sessiontaskrelation.RelationRevisited); err != nil {
		t.Fatalf("seed revisited: %v", err)
	}

	if _, err := dispatch(db, membershipEvent(
		t, KindSessionTasksQueued, 42, []string{"E-100", "E-101"}), nil,
	); err != nil {
		t.Fatalf("dispatch queued: %v", err)
	}
	for _, id := range []int64{100, 101} {
		if got := sessionTaskRelation(t, db, 42, id); got != "queued" {
			t.Errorf("E-%d: relation = %q, want queued", id, got)
		}
	}
}

// TestSessionTasksQueued_LeavesClaimedAlone pins that queuing the session's own
// claimed task does not demote it. The upgrade-only ladder is what enforces
// this, so the executor needs no special case — but the behavior is load-bearing
// and would be invisible until someone lost a `claimed` classification.
func TestSessionTasksQueued_LeavesClaimedAlone(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	if err := upsertSessionTask(db, "42", 100, sessiontaskrelation.RelationClaimed); err != nil {
		t.Fatalf("seed claim: %v", err)
	}

	res, err := dispatch(db, membershipEvent(
		t, KindSessionTasksQueued, 42, []string{"E-100"}), nil,
	)
	if err != nil {
		t.Fatalf("dispatch queued: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "claimed" {
		t.Errorf("claim was demoted: relation = %q, want claimed", got)
	}
	if res == nil || !strings.Contains(res.Markdown, "already claimed by this session") {
		t.Errorf("expected the no-op to be reported, got %q", markdownOf(res))
	}
}

// TestSessionTasksQueued_RejectsUnknownTask pins that a typo fails the whole
// call rather than silently queuing nothing. Matches session_tasks.ordered's
// treatment of ids that name no task.
func TestSessionTasksQueued_RejectsUnknownTask(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)

	_, err := dispatch(db, membershipEvent(
		t, KindSessionTasksQueued, 42, []string{"E-100", "E-999"}), nil,
	)
	if err == nil || !strings.Contains(err.Error(), "E-999") {
		t.Fatalf("expected an error naming E-999, got %v", err)
	}
	if hasSessionTask(t, db, 42, 100) {
		t.Error("the valid id was queued despite the call failing")
	}
}

// TestSessionTasksRemoved_DropsRowAndHide is the verb's whole contract: the
// association is gone, and the hide on the same pair goes with it.
//
// The hide matters because session_hidden_tasks and session_tasks are
// independent tables with no relationship between them — dropping membership
// leaves the hide behind on its own, and a later re-capture would come back
// ALREADY hidden with nothing on screen to explain why.
func TestSessionTasksRemoved_DropsRowAndHide(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	if err := upsertSessionTask(db, "42", 100, sessiontaskrelation.RelationReferenced); err != nil {
		t.Fatalf("seed referenced: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO session_hidden_tasks (session_id, task_id, hidden_at)
		 VALUES (42, 100, '2026-08-21T00:00:00')`,
	); err != nil {
		t.Fatalf("seed hide: %v", err)
	}

	if _, err := dispatch(db, membershipEvent(
		t, KindSessionTasksRemoved, 42, []string{"E-100"}), nil,
	); err != nil {
		t.Fatalf("dispatch removed: %v", err)
	}
	if hasSessionTask(t, db, 42, 100) {
		t.Error("session_tasks row survived remove")
	}
	var hides int
	if err := db.QueryRow(
		`SELECT count(*) FROM session_hidden_tasks WHERE session_id = 42 AND task_id = 100`,
	).Scan(&hides); err != nil {
		t.Fatalf("count hides: %v", err)
	}
	if hides != 0 {
		t.Error("hide survived remove; a re-captured task would return silently hidden")
	}
}

// TestSessionTasksRemoved_RefusesClaimed pins the refusal AND its atomicity: naming
// the claimed task fails the whole call, so `session task remove E-100 E-101` cannot
// half-succeed. The claimed row is not a false positive — the session claimed that
// task — and the next task event would recreate it anyway.
func TestSessionTasksRemoved_RefusesClaimed(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	for _, id := range []int64{100, 101} {
		seedLiveTask(t, db, id)
	}
	if err := upsertSessionTask(db, "42", 100, sessiontaskrelation.RelationClaimed); err != nil {
		t.Fatalf("seed claim: %v", err)
	}
	if err := upsertSessionTask(db, "42", 101, sessiontaskrelation.RelationRevisited); err != nil {
		t.Fatalf("seed revisited: %v", err)
	}

	_, err := dispatch(db, membershipEvent(
		t, KindSessionTasksRemoved, 42, []string{"E-100", "E-101"}), nil,
	)
	if err == nil || !strings.Contains(err.Error(), "E-100") {
		t.Fatalf("expected a refusal naming E-100, got %v", err)
	}
	if !hasSessionTask(t, db, 42, 101) {
		t.Error("the unclaimed row was removed despite the call failing")
	}
}

// TestSessionTasksRemoved_AbsentIsReportedNoOp pins the deliberate asymmetry
// with `add`: removing a task the session never touched is not an error (it
// matches `session unhide --task`, and "make sure this is not on my list" is
// reasonable to ask about a task that already is not), but it IS reported, so a
// typo still surfaces instead of vanishing.
func TestSessionTasksRemoved_AbsentIsReportedNoOp(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)

	res, err := dispatch(db, membershipEvent(
		t, KindSessionTasksRemoved, 42, []string{"E-100"}), nil,
	)
	if err != nil {
		t.Fatalf("removing an untouched task should not error: %v", err)
	}
	if !strings.Contains(markdownOf(res), "not in this session") {
		t.Errorf("expected the no-op to be reported, got %q", markdownOf(res))
	}
}

// TestSessionTasksMembership_RejectsEmptyList pins that a payload naming no
// tasks fails rather than reporting a successful no-op, for both verbs.
func TestSessionTasksMembership_RejectsEmptyList(t *testing.T) {
	for _, kind := range []Kind{KindSessionTasksQueued, KindSessionTasksRemoved} {
		db := newSessionTasksTestDB(t)
		seedSession(t, db, 42)
		if _, err := dispatch(db, membershipEvent(t, kind, 42, nil), nil); err == nil {
			t.Errorf("%s: expected an error for an empty task list", kind)
		}
	}
}

func markdownOf(res *ExecuteResult) string {
	if res == nil {
		return ""
	}
	return res.Markdown
}
