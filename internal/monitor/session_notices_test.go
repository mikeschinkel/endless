package monitor

import (
	"database/sql"
	"testing"
)

// snNotices returns every notice row, oldest first, as (session_id, changes,
// changed_by_session) triples. Reads the table directly rather than through
// PendingNotices so the stored rows are observable independently of the
// delivery layer built on top of them.
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

// snNotifyHolders queues a status notice for every session holding the task, as
// the executor's fan-out would. Seeded directly: these tests are about the
// delivery and reaping built on the table, and the fan-out that writes it lives
// in internal/events (E-2422), which this package cannot import.
func snNotifyHolders(t *testing.T, db *sql.DB, taskID int64) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO session_notices (session_id, task_id, changes)
		 SELECT session_id, task_id, '{"status":{"before":"ready","after":"revisit"}}'
		   FROM session_tasks WHERE task_id = ?`, taskID,
	); err != nil {
		t.Fatalf("seed notices: %v", err)
	}
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

// TestPendingNoticesDeliverOnce pins one-shot delivery. Re-showing a notice on
// every turn is the per-turn bloat this design exists to avoid, and it is also
// what would train an agent to skim the line.
func TestPendingNoticesDeliverOnce(t *testing.T) {
	db := withTestDB(t)
	snNoticeFixture(t, db)

	snNotifyHolders(t, db, 500)

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
			changes: `{"risk":{"before":null,"after":"high"}}`,
			want:    "FYI — E-500 risk: — → high",
		},
		{
			name:    "freeform renders a verb, never content",
			changes: `{"description":{"before":"…","after":"…"}}`,
			want:    "FYI — E-500 description edited",
		},
		{
			name: "multi-field keeps a stable order",
			changes: `{"description":{"before":null,"after":"…"},` +
				`"risk":{"before":"low","after":"medium"},` +
				`"complexity":{"before":null,"after":"low"},` +
				`"status":{"before":"ready","after":"underway"}}`,
			want: "FYI — E-500 status: ready → underway; complexity: — → low; risk: low → medium; description added",
		},
		{
			// E-1813 dropped tasks.tier. A tier notice still undelivered when it
			// landed must render rather than be held back forever.
			name:    "a pre-E-1813 tier notice still renders",
			changes: `{"tier":{"before":null,"after":2}}`,
			want:    "FYI — E-500 tier: — → 2",
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
// executor writes for a landing (E-2005). It is not a task
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

	if _, err := db.Exec("UPDATE tasks SET status='underway' WHERE id=500"); err != nil {
		t.Fatalf("update: %v", err)
	}
	h, err := GetTaskHeadline(500)
	if err != nil {
		t.Fatalf("GetTaskHeadline: %v", err)
	}
	if h.Status != "underway" || h.Phase != "now" {
		t.Errorf("headline should reflect current row, got %+v", h)
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

	snNotifyHolders(t, db, 500)
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

	snNotifyHolders(t, db, 500)
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
