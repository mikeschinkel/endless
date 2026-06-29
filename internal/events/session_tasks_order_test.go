package events

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// seedSession inserts a minimal live sessions row so the "__session_id=N"
// sentinel resolves in execSessionTasksOrdered.
func seedSession(t *testing.T, db *sql.DB, id int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (id, state) VALUES (?, 'working')`, id,
	); err != nil {
		t.Fatalf("seed session %d: %v", id, err)
	}
}

// seedSessionTask inserts a session_tasks row with the given do_order
// (passing -1 leaves do_order NULL).
func seedSessionTask(t *testing.T, db *sql.DB, sessionID, taskID int64, order int) {
	t.Helper()
	const ts = "2026-06-29T00:00:00"
	if order < 0 {
		if _, err := db.Exec(
			`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?)`,
			sessionID, taskID, ts, ts,
		); err != nil {
			t.Fatalf("seed session_task (%d,%d): %v", sessionID, taskID, err)
		}
		return
	}
	if _, err := db.Exec(
		`INSERT INTO session_tasks (session_id, task_id, created_at, updated_at, do_order)
		 VALUES (?, ?, ?, ?, ?)`,
		sessionID, taskID, ts, ts, order,
	); err != nil {
		t.Fatalf("seed session_task (%d,%d): %v", sessionID, taskID, err)
	}
}

func doOrder(t *testing.T, db *sql.DB, sessionID, taskID int64) (val int, isNull bool) {
	t.Helper()
	var v sql.NullInt64
	err := db.QueryRow(
		`SELECT do_order FROM session_tasks WHERE session_id = ? AND task_id = ?`,
		sessionID, taskID,
	).Scan(&v)
	if err != nil {
		t.Fatalf("read do_order (%d,%d): %v", sessionID, taskID, err)
	}
	if !v.Valid {
		return 0, true
	}
	return int(v.Int64), false
}

func orderedEvent(t *testing.T, sessionID int64, groups [][]string) *Event {
	t.Helper()
	sid := strconv.FormatInt(sessionID, 10)
	payload, err := json.Marshal(SessionTasksOrderedPayload{
		Process: sessionIDSentinelPrefix + sid,
		Groups:  groups,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return &Event{
		V:       1,
		TS:      "2026-06-29T00:00:01",
		Kind:    KindSessionTasksOrdered,
		Project: "test",
		Entity:  EntityRef{Type: EntitySessionTasks, ID: "0"},
		Actor:   Actor{Kind: ActorCLI, ID: "user@host", SessionID: sid},
		Payload: payload,
	}
}

// TestSessionTasksOrdered_AssignsSequenceAndParallel verifies the canonical
// plan example: "E-100 E-101|E-102 E-103" -> 1, 2, 2, 3.
func TestSessionTasksOrdered_AssignsSequenceAndParallel(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	for _, id := range []int64{100, 101, 102, 103} {
		seedSessionTask(t, db, 42, id, -1)
	}

	evt := orderedEvent(t, 42, [][]string{{"E-100"}, {"E-101", "E-102"}, {"E-103"}})
	res, err := dispatch(db, evt, nil)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if res == nil || res.Markdown == "" {
		t.Fatal("expected non-empty markdown result")
	}

	want := map[int64]int{100: 1, 101: 2, 102: 2, 103: 3}
	for id, exp := range want {
		got, isNull := doOrder(t, db, 42, id)
		if isNull || got != exp {
			t.Errorf("E-%d: expected do_order %d, got %d (null=%v)", id, exp, got, isNull)
		}
	}
}

// TestSessionTasksOrdered_ReplaceAllResetsOmitted verifies replace-all
// semantics: a task previously ordered but absent from the new spec is reset
// to NULL.
func TestSessionTasksOrdered_ReplaceAllResetsOmitted(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedSessionTask(t, db, 42, 100, 1)
	seedSessionTask(t, db, 42, 101, 2) // will be omitted from the new spec

	evt := orderedEvent(t, 42, [][]string{{"E-100"}})
	if _, err := dispatch(db, evt, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if got, isNull := doOrder(t, db, 42, 100); isNull || got != 1 {
		t.Errorf("E-100: expected do_order 1, got %d (null=%v)", got, isNull)
	}
	if _, isNull := doOrder(t, db, 42, 101); !isNull {
		t.Error("E-101: expected do_order reset to NULL (omitted from spec)")
	}
}

// TestSessionTasksOrdered_RejectsForeignTask verifies an id not in this
// session's session_tasks is rejected and nothing is mutated.
func TestSessionTasksOrdered_RejectsForeignTask(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedSessionTask(t, db, 42, 100, -1)

	evt := orderedEvent(t, 42, [][]string{{"E-100"}, {"E-999"}})
	_, err := dispatch(db, evt, nil)
	if err == nil {
		t.Fatal("expected error for foreign task id E-999")
	}
	if !strings.Contains(err.Error(), "E-999") {
		t.Errorf("error should name the foreign id; got %v", err)
	}
	// E-100 must remain untouched (no partial application).
	if _, isNull := doOrder(t, db, 42, 100); !isNull {
		t.Error("E-100 do_order should be unchanged (NULL) after a rejected op")
	}
}

// TestSessionTasksOrdered_RejectsDuplicate verifies an id appearing in more
// than one group is rejected.
func TestSessionTasksOrdered_RejectsDuplicate(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedSessionTask(t, db, 42, 100, -1)

	evt := orderedEvent(t, 42, [][]string{{"E-100"}, {"E-100"}})
	if _, err := dispatch(db, evt, nil); err == nil {
		t.Fatal("expected error for duplicate task id across groups")
	}
}

// TestSessionTasksOrdered_DoesNotBumpUpdatedAt verifies reordering leaves
// session_tasks.updated_at unchanged (the reap-worktrees staleness clock).
func TestSessionTasksOrdered_DoesNotBumpUpdatedAt(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedSessionTask(t, db, 42, 100, -1)

	_, before, _ := sessionTasksRow(t, db, 42, 100)
	evt := orderedEvent(t, 42, [][]string{{"E-100"}})
	if _, err := dispatch(db, evt, nil); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	_, after, _ := sessionTasksRow(t, db, 42, 100)
	if before != after {
		t.Errorf("updated_at must not change on reorder; was %q now %q", before, after)
	}
}

// TestSessionTasksOrdered_RejectsEmptyGroup verifies a structurally empty
// group is rejected (defense in depth; the Python layer also guards this).
func TestSessionTasksOrdered_RejectsEmptyGroup(t *testing.T) {
	db := newSessionTasksTestDB(t)
	seedSession(t, db, 42)
	seedSessionTask(t, db, 42, 100, -1)

	evt := orderedEvent(t, 42, [][]string{{"E-100"}, {}})
	if _, err := dispatch(db, evt, nil); err == nil {
		t.Fatal("expected error for empty group")
	}
}
