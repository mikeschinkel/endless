// The session-notice fan-out, driven through the executor (E-2422).
//
// These are the trigger tests internal/monitor carried until E-2422 (E-1917's
// TestNoticeTrigger_* and E-1531's content cases), ported from raw UPDATEs to
// real events: the rules now live in dispatchWithNotices, and an UPDATE that
// bypasses the executor no longer notifies anybody. Self-suppression has its
// own suite in notice_actor_test.go, and landings in task_landed_notice_test.go.
package events_test

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/events"
)

// noticeChanges returns the decoded `changes` of every notice sent to a
// session, oldest first.
func noticeChanges(t *testing.T, db *sql.DB, sessionID int64) []map[string]map[string]any {
	t.Helper()
	rows, err := db.Query(
		"SELECT changes FROM session_notices WHERE session_id = ? ORDER BY id", sessionID)
	if err != nil {
		t.Fatalf("query notices: %v", err)
	}
	defer rows.Close()
	var out []map[string]map[string]any
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan notice: %v", err)
		}
		var changes map[string]map[string]any
		if err := json.Unmarshal([]byte(raw), &changes); err != nil {
			t.Fatalf("changes is not JSON: %s", raw)
		}
		out = append(out, changes)
	}
	return out
}

// updateFields applies one task.fields_updated event as a person would: no
// harness, so nobody is suppressed.
func updateFields(t *testing.T, taskID int64, ts string, fields map[string]any) error {
	t.Helper()
	evt := taskEvent(t, events.KindTaskFieldsUpdated, taskID, ts,
		events.TaskFieldsUpdatedPayload{Fields: fields})
	_, err := events.Execute(&evt, nil)
	return err
}

func mustUpdateFields(t *testing.T, taskID int64, ts string, fields map[string]any) {
	t.Helper()
	if err := updateFields(t, taskID, ts, fields); err != nil {
		t.Fatalf("Execute fields_updated %v: %v", fields, err)
	}
}

// TestNotice_NullActorNotifiesEveryHolder is the case the whole feature exists
// for: the user changes a task from a bare terminal, where there is no session
// to attribute the change to. A NULL actor must suppress nobody.
func TestNotice_NullActorNotifiesEveryHolder(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)
	holdTask(t, db, 500, 9002, "working")

	if _, err := events.Execute(newStatusChangedEvent(t, 500, "ready", "revisit"), nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for _, sid := range []int64{9001, 9002} {
		got := noticeChanges(t, db, sid)
		if len(got) != 1 {
			t.Fatalf("session %d: want 1 notice, got %v", sid, got)
		}
		if s := got[0]["status"]; s["before"] != "ready" || s["after"] != "revisit" {
			t.Errorf("session %d: status change = %v, want ready → revisit", sid, s)
		}
		if actor := noticeActorFor(t, db, sid); actor.Valid {
			t.Errorf("session %d: changed_by_session should be NULL, got %d", sid, actor.Int64)
		}
	}
}

// TestNotice_NoNoticeWhenNothingChanged is the "changed → notify; unchanged →
// don't" contract the triggers existed to guarantee. Setting a field to the
// value it already holds is not an edit, and an unwatched field moving is not a
// notice-worthy one.
func TestNotice_NoNoticeWhenNothingChanged(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)

	mustUpdateFields(t, 500, "5WYM00000101", map[string]any{"phase": "now"})
	mustUpdateFields(t, 500, "5WYM00000102", map[string]any{"title": "renamed"})

	if got := noticeChanges(t, db, 9001); len(got) != 0 {
		t.Errorf("a no-op or unwatched update must write no notices, got %v", got)
	}
}

// TestNotice_OneNoticePerEvent pins the granularity: one `task update` that
// moves several fields is ONE edit, and must render as one line and flip
// `notified` once. Ratings render as slugs.
func TestNotice_OneNoticePerEvent(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)

	mustUpdateFields(t, 500, "5WYM00000103", map[string]any{
		"status": "revisit", "phase": "next", "complexity": "low", "risk": "high",
	})

	got := noticeChanges(t, db, 9001)
	if len(got) != 1 {
		t.Fatalf("want 1 notice, got %d: %v", len(got), got)
	}
	want := map[string][2]any{
		"status":     {"ready", "revisit"},
		"phase":      {"now", "next"},
		"complexity": {nil, "low"},
		"risk":       {nil, "high"},
	}
	if len(got[0]) != len(want) {
		t.Errorf("want exactly %d changed fields, got %v", len(want), got[0])
	}
	for field, ba := range want {
		c := got[0][field]
		if c == nil || c["before"] != ba[0] || c["after"] != ba[1] {
			t.Errorf("%s change = %v, want %v → %v", field, c, ba[0], ba[1])
		}
	}
}

// TestNotice_FreeformNeverLeaksContent is the privacy/bloat guarantee: the
// notice says a freeform field CHANGED without reproducing it.
func TestNotice_FreeformNeverLeaksContent(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)

	const secret = "the-actual-body"
	mustUpdateFields(t, 500, "5WYM00000104", map[string]any{
		"description": secret, "plan": secret, "analysis": secret, "notes": secret,
	})

	var raw string
	if err := db.QueryRow(
		"SELECT changes FROM session_notices WHERE session_id = 9001").Scan(&raw); err != nil {
		t.Fatalf("read notice: %v", err)
	}
	if strings.Contains(raw, secret) {
		t.Errorf("notice leaked freeform content: %s", raw)
	}
	for _, field := range []string{"plan", "analysis", "notes"} {
		if !strings.Contains(raw, `"`+field+`":{"before":null,"after":"…"}`) {
			t.Errorf("notice does not elide %s as added: %s", field, raw)
		}
	}
}

// TestNotice_FreeformTransitions pins the transitions the elision preserves on
// the plan: content is never carried, but whether it arrived, left, or merely
// changed still is. Rewriting identical content is no change at all.
func TestNotice_FreeformTransitions(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)

	steps := []struct {
		plan          string
		before, after any
	}{
		{"content", nil, "…"},   // added
		{"different", "…", "…"}, // edited
		{"", "…", nil},          // cleared
		{"content", nil, "…"},   // added again
	}
	for i, step := range steps {
		mustUpdateFields(t, 500, "5WYM0000020"+string(rune('0'+i)),
			map[string]any{"plan": step.plan})
		got := noticeChanges(t, db, 9001)
		if len(got) != i+1 {
			t.Fatalf("step %d: want %d notices, got %d", i, i+1, len(got))
		}
		if p := got[i]["plan"]; p == nil || p["before"] != step.before || p["after"] != step.after {
			t.Errorf("step %d: plan change = %v, want %v → %v", i, p, step.before, step.after)
		}
	}

	mustUpdateFields(t, 500, "5WYM00000209", map[string]any{"plan": "content"})
	if got := noticeChanges(t, db, 9001); len(got) != len(steps) {
		t.Errorf("rewriting the same plan wrote a notice: %v", got[len(got)-1])
	}
}

// TestNotice_DescriptionIsNotWatched pins E-1994: the plan is the spec, so a
// description edit changes nothing a session holding the task works from.
func TestNotice_DescriptionIsNotWatched(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)

	mustUpdateFields(t, 500, "5WYM00000105", map[string]any{"description": "rewritten"})

	if got := noticeChanges(t, db, 9001); len(got) != 0 {
		t.Errorf("a description edit wrote notices: %v", got)
	}
}

// TestNotice_SkipsEndedSessions pins the write-side half of the E-1917 fix: an
// ended session never takes another turn, so its notice would be undeliverable
// the moment it was written. idle and needs_input sessions can come back, and a
// notice surviving until they do is the point of one-shot delivery.
func TestNotice_SkipsEndedSessions(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)
	holdTask(t, db, 500, 9002, "ended")
	holdTask(t, db, 500, 9003, "idle")
	holdTask(t, db, 500, 9004, "needs_input")

	if _, err := events.Execute(newStatusChangedEvent(t, 500, "ready", "revisit"), nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	for sid, want := range map[int64]int{9001: 1, 9002: 0, 9003: 1, 9004: 1} {
		if got := len(noticeChanges(t, db, sid)); got != want {
			t.Errorf("session %d: %d notices, want %d", sid, got, want)
		}
	}
}

// TestNotice_CascadeNotifiesEveryChangedTask covers what the trigger caught for
// free and a per-site port could miss: one event that changes several tasks — a
// cascade down a subtree here, an epic's re-derivation up the chain — owes a
// notice to the holders of each of them, not only of the task it names.
func TestNotice_CascadeNotifiesEveryChangedTask(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)
	if _, err := db.Exec(
		`INSERT INTO tasks (id, project_id, parent_id, title, status, phase)
		 VALUES (501, 1, 500, 'child', 'ready', 'now')`,
	); err != nil {
		t.Fatalf("seed child: %v", err)
	}
	holdTask(t, db, 501, 9002, "working")

	evt := newStatusChangedEvent(t, 500, "ready", "declined")
	payload, err := json.Marshal(events.TaskStatusChangedPayload{
		OldStatus: "ready", NewStatus: "declined", Cascade: true,
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	evt.Payload = payload
	if _, err := events.Execute(evt, nil); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	got := noticeChanges(t, db, 9002)
	if len(got) != 1 || got[0]["status"]["after"] != "declined" {
		t.Errorf("the child's holder should hear its status moved, got %v", got)
	}
}

// TestNotice_FailedEventWritesNothing pins the ordering: notices are written
// only after the task write commits, so an event the executor refuses tells
// nobody about a change that never happened.
func TestNotice_FailedEventWritesNothing(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)

	if err := updateFields(t, 500, "5WYM00000106",
		map[string]any{"phase": "next", "no_such_field": 1}); err == nil {
		t.Fatal("an unknown field should have been refused")
	}
	if got := noticeChanges(t, db, 9001); len(got) != 0 {
		t.Errorf("a refused event wrote notices: %v", got)
	}
}

// TestNotice_RawUpdateNotifiesNobody pins that the triggers are gone: a write
// that bypasses the executor — which nothing may do (ED-903) — fans nothing
// out, so a notice can only come from the one place that owns the rule.
func TestNotice_RawUpdateNotifiesNobody(t *testing.T) {
	db := withExecutorDB(t)
	seedHeldTask(t, db, 500, 9001)

	if _, err := db.Exec("UPDATE tasks SET status = 'revisit' WHERE id = 500"); err != nil {
		t.Fatalf("raw update: %v", err)
	}
	if got := noticeChanges(t, db, 9001); len(got) != 0 {
		t.Errorf("a raw UPDATE wrote notices; a trigger survived: %v", got)
	}
}
