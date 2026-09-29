package events

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// sessionFocus reads sessions.focus_task_id (E-2188), 0 when NULL.
func sessionFocus(t *testing.T, db *sql.DB, sessionID int64) int64 {
	t.Helper()
	var focus sql.NullInt64
	if err := db.QueryRow(
		`SELECT focus_task_id FROM sessions WHERE id = ?`, sessionID,
	).Scan(&focus); err != nil {
		t.Fatalf("read focus for session %d: %v", sessionID, err)
	}
	return focus.Int64
}

// TestSessionFocus_FollowsWritesNotReads pins E-2188's rule for what moves a
// session's focused task: claimed, surfaced and revisited captures do, on every
// touch including a repeat one; queued and referenced do not.
func TestSessionFocus_FollowsWritesNotReads(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	actor := Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}

	seedLiveTask(t, db, 100)
	if _, err := dispatch(db, taskClaimedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch claim: %v", err)
	}
	if got := sessionFocus(t, db, 42); got != 100 {
		t.Fatalf("after claim: focus = %d, want 100", got)
	}

	if _, err := dispatch(db, taskCreatedEvent(t, 101, actor), nil); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if got := sessionFocus(t, db, 42); got != 101 {
		t.Errorf("after filing: focus = %d, want 101 (surfaced moves focus)", got)
	}

	seedLiveTask(t, db, 102)
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 102, actor), nil); err != nil {
		t.Fatalf("dispatch update: %v", err)
	}
	if got := sessionFocus(t, db, 42); got != 102 {
		t.Errorf("after updating: focus = %d, want 102 (revisited moves focus)", got)
	}

	seedLiveTask(t, db, 103)
	if _, err := dispatch(db, membershipEvent(t, KindSessionTasksQueued, 42, []string{"E-103"}), nil); err != nil {
		t.Fatalf("dispatch queue: %v", err)
	}
	if got := sessionFocus(t, db, 42); got != 102 {
		t.Errorf("after queueing: focus = %d, want 102 (queued must not steal focus)", got)
	}

	if err := upsertSessionTask(db, "42", 104, sessiontaskrelation.RelationReferenced); err != nil {
		t.Fatalf("reference: %v", err)
	}
	if got := sessionFocus(t, db, 42); got != 102 {
		t.Errorf("after a read: focus = %d, want 102 (referenced must not steal focus)", got)
	}

	// A repeat touch of a task already on the board moves focus BACK to it — the
	// relation stays `claimed` (never downgraded), but focus follows the write.
	if _, err := dispatch(db, taskFieldsUpdatedEvent(t, 100, actor), nil); err != nil {
		t.Fatalf("dispatch re-update: %v", err)
	}
	if got := sessionFocus(t, db, 42); got != 100 {
		t.Errorf("after returning to the claimed task: focus = %d, want 100", got)
	}
	if got := sessionTaskRelation(t, db, 42, 100); got != "claimed" {
		t.Errorf("relation downgraded by the repeat touch: %q, want claimed", got)
	}
}

// TestSessionFocus_UnknownTaskIsNotAnError: session_tasks has no FK on task_id,
// so a capture can name a task with no tasks row. The focus write must skip it
// rather than fail the foreign key and take the task mutation down with it.
func TestSessionFocus_UnknownTaskIsNotAnError(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	if err := upsertSessionTask(db, "42", 999, sessiontaskrelation.RelationRevisited); err != nil {
		t.Fatalf("capture for an unknown task failed: %v", err)
	}
	if got := sessionFocus(t, db, 42); got != 0 {
		t.Errorf("focus = %d, want unset", got)
	}
}

// TestSessionFocus_OtherSessionsUntouched: a capture moves only the ACTING
// session's focus.
func TestSessionFocus_OtherSessionsUntouched(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedSession(t, db, 43)

	if _, err := dispatch(db, taskCreatedEvent(t, 100, Actor{Kind: ActorSession, ID: "s1", SessionID: "42"}), nil); err != nil {
		t.Fatalf("dispatch create: %v", err)
	}
	if got := sessionFocus(t, db, 43); got != 0 {
		t.Errorf("session 43's focus = %d, want unset", got)
	}
}

func TestRelation_MovesFocus(t *testing.T) {
	want := map[sessiontaskrelation.Relation]bool{
		sessiontaskrelation.RelationClaimed:    true,
		sessiontaskrelation.RelationSurfaced:   true,
		sessiontaskrelation.RelationRevisited:  true,
		sessiontaskrelation.RelationQueued:     false,
		sessiontaskrelation.RelationReferenced: false,
	}
	for _, rel := range sessiontaskrelation.All() {
		if got := rel.MovesFocus(); got != want[rel] {
			t.Errorf("%v.MovesFocus() = %v, want %v", rel, got, want[rel])
		}
	}
}
