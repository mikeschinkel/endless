package monitor

import (
	"strings"
	"testing"
)

// The readers' view of post-land items (E-2444): an item's newest post-land row
// is its current verdict, and which items hold a `landed` task.

func seedPostLandTask(t *testing.T) {
	t.Helper()
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/tmp/endless")
	seedTask(t, db, 7, 1, "t", "landed")
	for _, q := range []string{
		`INSERT INTO task_plan_items (task_id, seq, sort_order, kind, statement, origin, created_at, updated_at, post_land)
		 VALUES (7, 1, 1, 'deliverable', 'code', 'planned', 'x', 'x', 0),
		        (7, 2, 2, 'deliverable', 'row after install', 'planned', 'x', 'x', 1),
		        (7, 3, 3, 'deliverable', 'smoke run after install', 'planned', 'x', 'x', 1)`,
		`INSERT INTO task_reconciliations (id, task_id, seq, fingerprint, plan_digest, created_at)
		 VALUES (40, 7, 1, 'fp', 'd', 'x')`,
		`INSERT INTO task_reconciliation_entries (reconciliation_id, item_seq, verdict)
		 VALUES (40, 1, 'as_planned'), (40, 2, 'deferred'), (40, 3, 'deferred')`,
		`INSERT INTO task_questions (id, task_id, series, question, status, created_at, updated_at, kind)
		 VALUES (90, 7, 1, 'P2 (less)', 'answered', 'x', 'x', 'less')`,
		`INSERT INTO task_post_land_entries (reconciliation_id, attempt, item_seq, verdict, note, question_id, created_at)
		 VALUES (40, 1, 2, 'not_done', 'n', 90, 'x'),
		        (40, 2, 2, 'as_planned', NULL, NULL, 'x')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
}

func TestPostLand_NewestRowIsTheCurrentVerdict(t *testing.T) {
	seedPostLandTask(t)
	items, err := PlanItems(7, false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		v := "<none>"
		if it.Verdict != nil {
			v = *it.Verdict
		}
		if it.PostLandAttempt != nil {
			v += "@" + string(rune('0'+*it.PostLandAttempt))
		}
		got[it.Label] = v
	}
	want := map[string]string{"P1": "as_planned", "P2": "as_planned@2", "P3": "deferred"}
	for k, w := range want {
		if got[k] != w {
			t.Errorf("%s verdict = %q, want %q", k, got[k], w)
		}
	}
}

func TestPostLand_OpenItemsHoldTheTask(t *testing.T) {
	seedPostLandTask(t)
	st, err := ReadPostLandState(7)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Items) != 2 || len(st.Open) != 1 || st.Open[0].Label != "P3" {
		t.Fatalf("post-land state = %d items, open %+v; want 2 items, P3 open", len(st.Items), st.Open)
	}
	h, err := GetTaskHeadline(7)
	if err != nil {
		t.Fatal(err)
	}
	if line := h.Render(7); !strings.Contains(line, "Open post-land items: P3") {
		t.Errorf("headline %q does not name the open item", line)
	}
}
