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
// seedSession inserts a minimal live sessions row so the "__session_id=N"
// sentinel resolves in the membership executors. It lived in
// session_tasks_order_test.go until E-2142 retired `session order`.
func seedSession(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, state) VALUES (?, 'working')`, id,
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

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

// TestSessionTasksRemoved_RefusesBound pins E-2291: a bound task is the
// session's own task exactly as a claimed one is — sessions.task_id names it
// either way — so remove refuses it the same way and names session hide --task.
func TestSessionTasksRemoved_RefusesBound(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	if err := upsertSessionTask(db, "42", 100, sessiontaskrelation.RelationBound); err != nil {
		t.Fatalf("seed bind: %v", err)
	}

	_, err := dispatch(db, membershipEvent(
		t, KindSessionTasksRemoved, 42, []string{"E-100"}), nil,
	)
	if err == nil || !strings.Contains(err.Error(), "E-100") ||
		!strings.Contains(err.Error(), "session hide --task") {
		t.Fatalf("expected a refusal naming E-100 and session hide --task, got %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "bound" {
		t.Errorf("bound row changed despite the refusal: relation = %q", got)
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

// TestSessionTasksTouched_EnrollsWithoutTouchingTheTask is `touch`'s whole
// contract (E-2173): the task joins the session's scope at `touched` (E-2291;
// it was `revisited` before), and
// nothing about the task itself changes. The second half is the point of the
// verb — the workaround it replaces rewrote tasks.phase to buy the same display
// effect, so a test that only checked the session_tasks row would pass for the
// bug as readily as for the fix.
func TestSessionTasksTouched_EnrollsWithoutTouchingTheTask(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)
	before := taskRowSnapshot(t, db, 100)

	res, err := dispatch(db, membershipEvent(
		t, KindSessionTasksTouched, 42, []string{"E-100"}), nil,
	)
	if err != nil {
		t.Fatalf("dispatch touched: %v", err)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "touched" {
		t.Errorf("relation = %q, want touched", got)
	}
	if after := taskRowSnapshot(t, db, 100); after != before {
		t.Errorf("touch altered the task row:\n before %s\n after  %s", before, after)
	}
	if !strings.Contains(markdownOf(res), "E-100") {
		t.Errorf("expected the touch to be reported, got %q", markdownOf(res))
	}
}

// TestSessionTasksTouched_LeavesStrongerRelationsAlone pins that touching the
// session's own task leaves it as it is (P14 of E-2291 for bound). Two
// relations outrank `touched` — claimed and bound — and a demotion would lose
// the ownership record. The report has to name which one survived, or the
// reader cannot tell a claim from a bind.
func TestSessionTasksTouched_LeavesStrongerRelationsAlone(t *testing.T) {
	for _, tc := range []struct {
		rel   sessiontaskrelation.Relation
		slug  string
		label string
	}{
		{sessiontaskrelation.RelationClaimed, "claimed", "Claimed"},
		{sessiontaskrelation.RelationBound, "bound", "Bound"},
	} {
		db := newSessionTasksTestDB(t)
		seedSession(t, db, 42)
		seedLiveTask(t, db, 100)
		if err := upsertSessionTask(db, "42", 100, tc.rel); err != nil {
			t.Fatalf("seed %s: %v", tc.slug, err)
		}

		res, err := dispatch(db, membershipEvent(
			t, KindSessionTasksTouched, 42, []string{"E-100"}), nil,
		)
		if err != nil {
			t.Fatalf("%s: dispatch touched: %v", tc.slug, err)
		}
		if got := sessionTaskRelation(t, db, 42, 100); got != tc.slug {
			t.Errorf("%s was demoted: relation = %q", tc.slug, got)
		}
		want := "already in this session as " + tc.label
		if !strings.Contains(markdownOf(res), want) {
			t.Errorf("%s: expected %q in the report, got %q",
				tc.slug, want, markdownOf(res))
		}
	}
}

// TestSessionTasksTouched_UpgradesWeaker pins the other direction: filed,
// revisited and referenced captures are strengthened by an explicit touch,
// because asking for a task to be in scope is a decision about the session's
// work, which none of those incidental captures is.
func TestSessionTasksTouched_UpgradesWeaker(t *testing.T) {
	for _, rel := range []sessiontaskrelation.Relation{
		sessiontaskrelation.RelationFiled,
		sessiontaskrelation.RelationRevisited,
		sessiontaskrelation.RelationReferenced,
	} {
		db := newSessionTasksTestDB(t)
		seedSession(t, db, 42)
		seedLiveTask(t, db, 100)
		if err := upsertSessionTask(db, "42", 100, rel); err != nil {
			t.Fatalf("seed %s: %v", rel, err)
		}

		if _, err := dispatch(db, membershipEvent(
			t, KindSessionTasksTouched, 42, []string{"E-100"}), nil,
		); err != nil {
			t.Fatalf("%s: dispatch touched: %v", rel, err)
		}
		if got := sessionTaskRelation(t, db, 42, 100); got != "touched" {
			t.Errorf("%s: relation = %q, want touched", rel, got)
		}
	}
}

// TestSessionTasksTouched_RejectsUnknownTask pins that `touch` refuses a typo
// for the whole call, exactly as `session task add` does — both go through
// enrollSessionTasks, and a silently-enrolled-nothing would hide the typo.
func TestSessionTasksTouched_RejectsUnknownTask(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)

	_, err := dispatch(db, membershipEvent(
		t, KindSessionTasksTouched, 42, []string{"E-100", "E-999"}), nil,
	)
	if err == nil || !strings.Contains(err.Error(), "E-999") {
		t.Fatalf("expected an error naming E-999, got %v", err)
	}
	if hasSessionTask(t, db, 42, 100) {
		t.Error("the valid id was enrolled despite the call failing")
	}
}

// taskRowSnapshot renders the fields a mutation would move, so a test can
// assert a whole task row went unchanged rather than naming one column and
// missing the next one somebody writes.
func taskRowSnapshot(t *testing.T, db *sql.DB, taskID int64) string {
	t.Helper()
	var title, status, phase, updatedAt sql.NullString
	if err := db.QueryRow(
		`SELECT title, status, phase, updated_at FROM tasks WHERE id = ?`, taskID,
	).Scan(&title, &status, &phase, &updatedAt); err != nil {
		t.Fatalf("snapshot task %d: %v", taskID, err)
	}
	return strings.Join([]string{
		title.String, status.String, phase.String, updatedAt.String,
	}, "|")
}

// TestSessionTasksSpawned_RecordsRelationSeven applies a session_tasks.spawned
// event and reads session_tasks back (E-2405, P16): the SPAWNING session gets a
// row for the spawned task at relation_id 7, written through the event
// pipeline. The relation_id is asserted as the raw integer, not the slug,
// because 7 is what is persisted — a seed row with the wrong id would still
// join to the right slug on a fresh database and hide the drift.
func TestSessionTasksSpawned_RecordsRelationSeven(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedLiveTask(t, db, 100)

	res, err := dispatch(db, membershipEvent(
		t, KindSessionTasksSpawned, 42, []string{"E-100"}), nil,
	)
	if err != nil {
		t.Fatalf("dispatch spawned: %v", err)
	}
	var relID int64
	if err := db.QueryRow(
		`SELECT relation_id FROM session_tasks WHERE session_id = 42 AND task_id = 100`,
	).Scan(&relID); err != nil {
		t.Fatalf("read session_tasks: %v", err)
	}
	if relID != 7 || sessiontaskrelation.Relation(relID) != sessiontaskrelation.RelationSpawned {
		t.Errorf("relation_id = %d, want 7 (spawned)", relID)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "spawned" {
		t.Errorf("relation slug = %q, want spawned", got)
	}
	if !strings.Contains(markdownOf(res), "Recorded E-100 as spawned by this session.") {
		t.Errorf("unexpected result markdown: %q", markdownOf(res))
	}
	// Spawning does not move the spawner's focus: the spawned session works it.
	if got := sessionFocus(t, db, 42); got != 0 {
		t.Errorf("spawner focus = %d, want unset", got)
	}
}

// TestSessionTasksSpawned_UpgradesWeakerKeepsOwned pins P17 end to end through
// the executor: a task the session filed, edited or touched and then spawned
// reads Spawned, while spawning a task the session owns (claimed or bound)
// leaves it as it was and says which it is.
func TestSessionTasksSpawned_UpgradesWeakerKeepsOwned(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	prior := map[int64]sessiontaskrelation.Relation{
		100: sessiontaskrelation.RelationFiled,
		101: sessiontaskrelation.RelationRevisited,
		102: sessiontaskrelation.RelationTouched,
		103: sessiontaskrelation.RelationClaimed,
		104: sessiontaskrelation.RelationBound,
	}
	for id, rel := range prior {
		seedLiveTask(t, db, id)
		if err := upsertSessionTask(db, "42", id, rel); err != nil {
			t.Fatalf("seed E-%d as %s: %v", id, rel, err)
		}
	}

	res, err := dispatch(db, membershipEvent(
		t, KindSessionTasksSpawned, 42, []string{"E-100", "E-101", "E-102", "E-103", "E-104"}), nil,
	)
	if err != nil {
		t.Fatalf("dispatch spawned: %v", err)
	}
	for _, id := range []int64{100, 101, 102} {
		if got := sessionTaskRelation(t, db, 42, id); got != "spawned" {
			t.Errorf("E-%d (was %s): relation = %q, want spawned", id, prior[id], got)
		}
	}
	if got := sessionTaskRelation(t, db, 42, 103); got != "claimed" {
		t.Errorf("claim was demoted: relation = %q, want claimed", got)
	}
	if got := sessionTaskRelation(t, db, 42, 104); got != "bound" {
		t.Errorf("bind was demoted: relation = %q, want bound", got)
	}
	for _, want := range []string{
		"E-103 already in this session as Claimed — left as is.",
		"E-104 already in this session as Bound — left as is.",
	} {
		if !strings.Contains(markdownOf(res), want) {
			t.Errorf("expected %q in the result, got %q", want, markdownOf(res))
		}
	}
}

// TestSessionTasksMembership_RejectsEmptyList pins that a payload naming no
// tasks fails rather than reporting a successful no-op, for every verb.
func TestSessionTasksMembership_RejectsEmptyList(t *testing.T) {
	for _, kind := range []Kind{
		KindSessionTasksTouched, KindSessionTasksRemoved,
	} {
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
