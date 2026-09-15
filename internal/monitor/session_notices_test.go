package monitor

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

// snNotices returns every notice row, oldest first, as (session_id, changes,
// changed_by_session) triples. Reads the table directly rather than through
// PendingNotices so the trigger's behaviour is observable independently of the
// delivery layer built on top of it.
func snNotices(t *testing.T, db *sql.DB) [][3]any {
	t.Helper()
	rows, err := db.Query(
		"SELECT session_id, changes, changed_by_session FROM session_notices ORDER BY id")
	if err != nil {
		t.Fatalf("querying notices: %v", err)
	}
	defer rows.Close()
	var out [][3]any
	for rows.Next() {
		var sid int64
		var changes string
		var actor sql.NullInt64
		if err := rows.Scan(&sid, &changes, &actor); err != nil {
			t.Fatalf("scanning notice: %v", err)
		}
		var a any
		if actor.Valid {
			a = actor.Int64
		}
		out = append(out, [3]any{sid, changes, a})
	}
	return out
}

// snNoticeFixture seeds a project, one task and two sessions that both hold it.
func snNoticeFixture(t *testing.T, db *sql.DB) {
	t.Helper()
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "working")
	snSession(t, db, 9002, 1, 500, "working")
	snSessionTask(t, db, 9001, 500)
	snSessionTask(t, db, 9002, 500)
}

// TestNoticeTrigger_NullActorNotifiesEveryHolder is the case the whole feature
// exists for: the user changes a task from a bare terminal, where there is no
// session to attribute the change to. A NULL actor must suppress nobody.
func TestNoticeTrigger_NullActorNotifiesEveryHolder(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	if _, err := db.Exec("UPDATE tasks SET status='revisit' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}

	got := snNotices(t, db)
	if len(got) != 2 {
		t.Fatalf("want a notice for each of the 2 holders, got %d: %v", len(got), got)
	}
	for _, row := range got {
		if row[2] != nil {
			t.Errorf("changed_by_session should be NULL, got %v", row[2])
		}
		if !strings.Contains(row[1].(string), `"status"`) {
			t.Errorf("changes should name status, got %s", row[1])
		}
	}
}

// TestNoticeTrigger_SuppressesActorsOwnChange pins the rule that decides whether
// this feature helps or becomes noise: a session is never told about a change it
// made itself. Without it, the line would be redundant on most turns, and noise
// is what trains an agent to skim the line that matters.
func TestNoticeTrigger_SuppressesActorsOwnChange(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	if _, err := db.Exec(
		"UPDATE tasks SET status='underway', changed_by_session=9001 WHERE id=500",
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	got := snNotices(t, db)
	if len(got) != 1 {
		t.Fatalf("want 1 notice (the non-acting session), got %d: %v", len(got), got)
	}
	if got[0][0].(int64) != 9002 {
		t.Errorf("notice should go to 9002, not the acting session; got %v", got[0][0])
	}
}

// TestNoticeTrigger_NoRowWhenNothingChanged is the "changed → notify; unchanged
// → don't" contract. Setting a field to the value it already holds is not an
// edit, and a trigger that fired anyway would manufacture notices out of every
// idempotent write in the codebase.
func TestNoticeTrigger_NoRowWhenNothingChanged(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	if _, err := db.Exec("UPDATE tasks SET status='ready' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := snNotices(t, db); len(got) != 0 {
		t.Errorf("a no-op update must write no notices, got %v", got)
	}

	// An unwatched field moving is also not a notice-worthy change.
	if _, err := db.Exec("UPDATE tasks SET title='renamed' WHERE id=500"); err != nil {
		t.Fatalf("update title: %v", err)
	}
	if got := snNotices(t, db); len(got) != 0 {
		t.Errorf("an unwatched field must write no notices, got %v", got)
	}
}

// TestNoticeTrigger_OneRowPerUpdateEvent pins the row granularity: a single
// `task update` that moves three fields is ONE edit, and must render as one
// line and flip `notified` once — not fan out into three rows the agent reads
// as three separate events.
func TestNoticeTrigger_OneRowPerUpdateEvent(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	if _, err := db.Exec(
		"UPDATE tasks SET status='underway', tier=2, description='hello' WHERE id=500",
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	got := snNotices(t, db)
	if len(got) != 2 {
		t.Fatalf("want 1 row per holder, got %d", len(got))
	}
	var changes map[string]any
	if err := json.Unmarshal([]byte(got[0][1].(string)), &changes); err != nil {
		t.Fatalf("changes is not JSON: %v", err)
	}
	for _, field := range []string{"status", "tier", "description"} {
		if _, ok := changes[field]; !ok {
			t.Errorf("changes should carry %q, got %v", field, changes)
		}
	}
	if len(changes) != 3 {
		t.Errorf("want exactly 3 changed fields, got %v", changes)
	}
}

// TestNoticeTrigger_FreeformNeverLeaksContent is the privacy/bloat guarantee:
// the notice says a freeform field CHANGED without reproducing it. A regression
// here would quietly start pasting whole descriptions and plans into every
// agent's context.
func TestNoticeTrigger_FreeformNeverLeaksContent(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	const secret = "the-actual-description-body"
	if _, err := db.Exec(
		"UPDATE tasks SET description=?, plan=?, analysis=?, notes=? WHERE id=500",
		secret, secret, secret, secret,
	); err != nil {
		t.Fatalf("update: %v", err)
	}

	got := snNotices(t, db)
	if len(got) == 0 {
		t.Fatal("expected notices")
	}
	for _, row := range got {
		if strings.Contains(row[1].(string), secret) {
			t.Errorf("notice leaked freeform content: %s", row[1])
		}
	}
}

// TestNoticeTrigger_FreeformTransitions pins the four transitions the elision
// sentinel preserves. Content is never carried, but whether it arrived, left,
// or merely changed still is — which is the whole reason a bare "changed" flag
// was not enough.
func TestNoticeTrigger_FreeformTransitions(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	steps := []struct {
		set  any
		want string
	}{
		{"content", "added"},    // NULL   → content
		{nil, "cleared"},        // content → NULL
		{"content", "added"},    // NULL   → content
		{"", "emptied"},         // content → ""
		{"content", "added"},    // ""     → content
		{"different", "edited"}, // content → content
	}
	for _, step := range steps {
		before := len(snNotices(t, db))
		if _, err := db.Exec("UPDATE tasks SET description=? WHERE id=500", step.set); err != nil {
			t.Fatalf("set description=%v: %v", step.set, err)
		}
		rows := snNotices(t, db)
		if len(rows) == before {
			t.Fatalf("set description=%v: expected a notice", step.set)
		}
		var changes map[string]noticeChange
		if err := json.Unmarshal([]byte(rows[before][1].(string)), &changes); err != nil {
			t.Fatalf("changes not JSON: %v", err)
		}
		if got := freeformVerb(changes["description"]); got != step.want {
			t.Errorf("set description=%v: want %q, got %q (%v)",
				step.set, step.want, got, changes["description"])
		}
	}
}

// TestPendingNoticesDeliverOnce pins one-shot delivery. Re-showing a notice on
// every turn is the per-turn bloat this design exists to avoid, and it is also
// what would train an agent to skim the line.
func TestPendingNoticesDeliverOnce(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	if _, err := db.Exec("UPDATE tasks SET status='revisit' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}

	first, err := PendingNotices(9001)
	if err != nil {
		t.Fatalf("PendingNotices: %v", err)
	}
	if len(first) != 1 {
		t.Fatalf("want 1 pending notice, got %d", len(first))
	}

	// The other holder's notice must be untouched by 9001 draining its own.
	if err := MarkNoticesDelivered([]int64{first[0].ID}); err != nil {
		t.Fatalf("MarkNoticesDelivered: %v", err)
	}
	again, err := PendingNotices(9001)
	if err != nil {
		t.Fatalf("PendingNotices after delivery: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("a delivered notice must not reappear, got %v", again)
	}
	other, err := PendingNotices(9002)
	if err != nil {
		t.Fatalf("PendingNotices(9002): %v", err)
	}
	if len(other) != 1 {
		t.Errorf("9001 delivering must not consume 9002's notice, got %d", len(other))
	}
}

func TestRenderNotice(t *testing.T) {
	tests := []struct {
		name    string
		changes string
		want    string
	}{
		{
			name:    "enumerated before and after",
			changes: `{"status":{"before":"ready","after":"revisit"}}`,
			want:    "FYI — E-500 status: ready → revisit",
		},
		{
			name:    "absent value renders as a dash",
			changes: `{"tier":{"before":null,"after":2}}`,
			want:    "FYI — E-500 tier: — → 2",
		},
		{
			name:    "freeform renders a verb, never content",
			changes: `{"description":{"before":"…","after":"…"}}`,
			want:    "FYI — E-500 description edited",
		},
		{
			name: "multi-field keeps a stable order",
			changes: `{"description":{"before":null,"after":"…"},` +
				`"tier":{"before":null,"after":3},` +
				`"status":{"before":"ready","after":"underway"}}`,
			want: "FYI — E-500 status: ready → underway; tier: — → 3; description added",
		},
		{
			// E-1000 renamed the trigger's key from `text` to `plan`. A notice
			// already sitting undelivered when the rename landed still carries
			// the old key, and delivery is one-shot: a row that rendered
			// nothing would be held back as unrenderable forever rather than
			// merely losing a field.
			name:    "a pre-rename notice's `text` key renders as the plan",
			changes: `{"text":{"before":null,"after":"…"}}`,
			want:    "FYI — E-500 plan added",
		},
		{
			name: "a pre-rename key alongside its successor does not double up",
			changes: `{"plan":{"before":"…","after":"…"},` +
				`"text":{"before":null,"after":"…"}}`,
			want: "FYI — E-500 plan edited",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RenderNotice(Notice{TaskID: 500, Changes: tc.changes})
			if !ok {
				t.Fatalf("RenderNotice returned not-ok for %s", tc.changes)
			}
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestRenderNoticeLanded covers the synthetic `landed` key the
// task_landings_notify_sessions trigger writes (E-2005). It is not a task
// field, so it renders as a sentence rather than a "before → after" pair, and
// it is answered before noticeFieldOrder is consulted.
func TestRenderNoticeLanded(t *testing.T) {
	tests := []struct {
		name    string
		changes string
		want    string
	}{
		{
			name:    "the ordinary land names the branch and the commit",
			changes: `{"landed":{"before":null,"after":"main@1dd0006"}}`,
			want:    "FYI — E-500 landed on main (1dd0006)",
		},
		{
			name:    "a base branch containing @ splits on the last one",
			changes: `{"landed":{"before":null,"after":"release@2@6671bca"}}`,
			want:    "FYI — E-500 landed on release@2 (6671bca)",
		},
		{
			name: "a record-only backfill has no base branch to name",
			// E-1719: the land it records predates anything being asked to
			// remember one, and "main" written there would be a guess.
			changes: `{"landed":{"before":null,"after":"6671bca"}}`,
			want:    "FYI — E-500 landed (6671bca)",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RenderNotice(Notice{TaskID: 500, Changes: tc.changes})
			if !ok {
				t.Fatalf("RenderNotice returned not-ok for %s", tc.changes)
			}
			if got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestRenderNoticeRejectsUnrenderable pins the fail-safe: a notice that cannot
// be rendered must report not-ok so the caller leaves it PENDING. Delivering a
// blank and marking it done would consume the only copy of a correction.
func TestRenderNoticeRejectsUnrenderable(t *testing.T) {
	for _, changes := range []string{"", "{}", "not json", `{"unknown_field":{"before":1,"after":2}}`} {
		if _, ok := RenderNotice(Notice{TaskID: 500, Changes: changes}); ok {
			t.Errorf("changes %q should be unrenderable", changes)
		}
	}
}

func TestTaskHeadlineRender(t *testing.T) {
	tests := []struct {
		name string
		h    TaskHeadline
		want string
	}{
		{
			name: "all facts present",
			h:    TaskHeadline{Title: "Do a thing", Status: "underway", Phase: "now", Tier: 2, HasTier: true},
			want: "Active task: E-500 (underway · tier 2 · now) — Do a thing.",
		},
		{
			name: "no tier set",
			h:    TaskHeadline{Title: "Do a thing", Status: "ready", Phase: "next"},
			want: "Active task: E-500 (ready · next) — Do a thing.",
		},
		{
			name: "nothing known degrades to the old line",
			h:    TaskHeadline{Title: "Do a thing"},
			want: "Active task: E-500 — Do a thing.",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.h.Render(500); got != tc.want {
				t.Errorf("want %q, got %q", tc.want, got)
			}
		})
	}
}

// TestGetTaskHeadlineReadsCurrentValues covers Arm 2's whole purpose: the line
// is re-derived from the tasks table every turn, so it cannot go stale the way
// a one-shot notice can after a context compaction.
func TestGetTaskHeadlineReadsCurrentValues(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	if _, err := db.Exec("UPDATE tasks SET status='underway', tier=2 WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}
	h, err := GetTaskHeadline(500)
	if err != nil {
		t.Fatalf("GetTaskHeadline: %v", err)
	}
	if h.Status != "underway" || !h.HasTier || h.Tier != 2 || h.Phase != "now" {
		t.Errorf("headline should reflect current row, got %+v", h)
	}
}

// TestNoticeTrigger_SkipsEndedSessions pins the write-side half of the E-1917
// fix. An ended session never takes another turn, so a notice written for it is
// undeliverable the moment it is created — 82% of the table was stranded this
// way before the fan-out filtered on state.
func TestNoticeTrigger_SkipsEndedSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "working")
	snSession(t, db, 9002, 1, 500, "ended")
	snSessionTask(t, db, 9001, 500)
	snSessionTask(t, db, 9002, 500)

	if _, err := db.Exec("UPDATE tasks SET status='revisit' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}

	got := snNotices(t, db)
	if len(got) != 1 {
		t.Fatalf("only the live session should get a notice, got %d: %v", len(got), got)
	}
	if got[0][0].(int64) != 9001 {
		t.Errorf("notice should go to the live session 9001, got %v", got[0][0])
	}
}

// TestNoticeTrigger_KeepsIdleAndNeedsInputSessions guards the other side of that
// filter. Only `ended` is excluded: an idle or waiting session can still come
// back, and a notice surviving until it does is the entire point of one-shot
// delivery. Widening the filter to "not working" would silently drop the
// notices most worth keeping.
func TestNoticeTrigger_KeepsIdleAndNeedsInputSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "idle")
	snSession(t, db, 9002, 1, 500, "needs_input")
	snSessionTask(t, db, 9001, 500)
	snSessionTask(t, db, 9002, 500)

	if _, err := db.Exec("UPDATE tasks SET status='revisit' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}

	if got := snNotices(t, db); len(got) != 2 {
		t.Errorf("idle and needs_input sessions must still be notified, got %d: %v",
			len(got), got)
	}
}

// TestReapNoticesForEndedSessions covers the case the write-side filter cannot:
// a session that ends AFTER its notice was written. Those rows are undeliverable
// forever, and without the reaper they accumulate without bound.
func TestReapNoticesForEndedSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "working")
	snSession(t, db, 9002, 1, 500, "idle")
	snSessionTask(t, db, 9001, 500)
	snSessionTask(t, db, 9002, 500)

	if _, err := db.Exec("UPDATE tasks SET status='revisit' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}
	if got := snNotices(t, db); len(got) != 2 {
		t.Fatalf("setup: want 2 notices, got %d", len(got))
	}

	// 9001 ends after its notice was written; 9002 stays idle.
	if _, err := db.Exec("UPDATE sessions SET state='ended' WHERE id=9001"); err != nil {
		t.Fatalf("ending session: %v", err)
	}
	if err := ReapNoticesForEndedSessions(); err != nil {
		t.Fatalf("ReapNoticesForEndedSessions: %v", err)
	}

	got := snNotices(t, db)
	if len(got) != 1 {
		t.Fatalf("want only the idle session's notice to survive, got %d: %v", len(got), got)
	}
	if got[0][0].(int64) != 9002 {
		t.Errorf("the surviving notice should belong to 9002, got %v", got[0][0])
	}
}

// TestReapNoticesLeavesDeliveredRows pins that the reaper only removes
// UNDELIVERED rows. A delivered notice records what an agent was actually shown
// — the same fact the delivery log carries — and deleting it would destroy
// evidence rather than reclaim waste.
func TestReapNoticesLeavesDeliveredRows(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	snTask(t, db, 500, 1, "ready", "now", "")
	snSession(t, db, 9001, 1, 500, "working")
	snSessionTask(t, db, 9001, 500)

	if _, err := db.Exec("UPDATE tasks SET status='revisit' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}
	notices, err := PendingNotices(9001)
	if err != nil || len(notices) != 1 {
		t.Fatalf("setup: PendingNotices got %d, err %v", len(notices), err)
	}
	if err := MarkNoticesDelivered([]int64{notices[0].ID}); err != nil {
		t.Fatalf("MarkNoticesDelivered: %v", err)
	}

	if _, err := db.Exec("UPDATE sessions SET state='ended' WHERE id=9001"); err != nil {
		t.Fatalf("ending session: %v", err)
	}
	if err := ReapNoticesForEndedSessions(); err != nil {
		t.Fatalf("ReapNoticesForEndedSessions: %v", err)
	}

	if got := snNotices(t, db); len(got) != 1 {
		t.Errorf("a delivered notice must survive the reaper, got %d: %v", len(got), got)
	}
}
