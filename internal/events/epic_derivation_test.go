// White-box tests for epic status auto-derivation (E-1541). These call
// recomputeEpicStatus directly against a fresh schema-applied SQLite DB, seeding
// an epic + children and asserting the derived status. recomputeEpicStatus is
// driven with a nil emitter for the rule cases (DB-only) and a capturing emitter
// where attribution matters.
package events

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
	"github.com/mikeschinkel/endless/internal/tasktype"
)

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// newDerivationDB stands up a fresh schema-applied SQLite DB with a seeded
// "test" project (id 1). It is independent of monitor.DB() — recomputeEpicStatus
// takes a dbQuerier, so tests pass the handle directly.
func newDerivationDB(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema.SQL); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		t.Fatalf("set fks: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO projects (id, name, path, status, created_at, updated_at)
		 VALUES (1, 'test', '/tmp/test', 'active', '2026-06-21T00:00:00', '2026-06-21T00:00:00')`,
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return db
}

// seedTask inserts one task. parentID may be nil. typeID is a task_types id
// (use int(tasktype.TaskTypeEpic) for epics).
func seedTask(t *testing.T, db *sql.DB, id int64, parentID *int64, typeID int, status string) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO tasks (id, project_id, phase, title, status, type_id, parent_id, created_at, updated_at)
		 VALUES (?, 1, 'now', ?, ?, ?, ?, '2026-06-21T00:00:00', '2026-06-21T00:00:00')`,
		id, fmt.Sprintf("task-%d", id), status, typeID, parentID,
	)
	if err != nil {
		t.Fatalf("seed task %d: %v", id, err)
	}
}

func ptr(v int64) *int64 { return &v }

// taskStatus reads a task's status and completed_at.
func taskStatus(t *testing.T, db *sql.DB, id int64) (string, sql.NullString) {
	t.Helper()
	var status string
	var completedAt sql.NullString
	if err := db.QueryRow(
		"SELECT status, completed_at FROM tasks WHERE id = ?", id,
	).Scan(&status, &completedAt); err != nil {
		t.Fatalf("read task %d: %v", id, err)
	}
	return status, completedAt
}

// TestDeriveRule_Table covers the §1 derivation rule. Each case seeds an epic
// (id 1, starting at needs_plan) with the listed children, recomputes from the
// epic, and asserts the resulting epic status.
func TestDeriveRule_Table(t *testing.T) {
	epic := int64(1)
	epicType := int(tasktype.TaskTypeEpic)
	taskType := int(tasktype.TaskTypeTask)

	cases := []struct {
		name     string
		children []string // child statuses
		want     string
	}{
		{"all needs_plan", []string{"needs_plan", "needs_plan"}, "needs_plan"},
		{"needs_plan + ready", []string{"needs_plan", "ready"}, "ready"},
		{"any in_progress", []string{"needs_plan", "ready", "in_progress"}, "in_progress"},
		{"all terminal", []string{"confirmed", "completed", "assumed"}, "completed"},
		{"ready + terminal", []string{"ready", "confirmed"}, "ready"},
		{"needs_plan + terminal", []string{"needs_plan", "obsolete"}, "needs_plan"},
		{"in_progress wins over ready", []string{"ready", "in_progress", "confirmed"}, "in_progress"},
		{"declined+obsolete are terminal", []string{"declined", "obsolete"}, "completed"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newDerivationDB(t)
			seedTask(t, db, epic, nil, epicType, "needs_plan")
			for i, st := range tc.children {
				seedTask(t, db, int64(100+i), ptr(epic), taskType, st)
			}
			if err := recomputeEpicStatus(db, nil, epic); err != nil {
				t.Fatalf("recompute: %v", err)
			}
			got, completedAt := taskStatus(t, db, epic)
			if got != tc.want {
				t.Fatalf("epic status = %q, want %q", got, tc.want)
			}
			// completed_at must be set iff completed.
			if tc.want == "completed" && !completedAt.Valid {
				t.Errorf("completed epic has NULL completed_at")
			}
			if tc.want != "completed" && completedAt.Valid {
				t.Errorf("non-completed epic has completed_at=%q", completedAt.String)
			}
		})
	}
}

// TestDerive_ZeroChildrenUnchanged: an epic with no children is left untouched.
func TestDerive_ZeroChildrenUnchanged(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "in_progress")
	if err := recomputeEpicStatus(db, nil, 1); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if got, _ := taskStatus(t, db, 1); got != "in_progress" {
		t.Fatalf("childless epic status = %q, want unchanged in_progress", got)
	}
}

// TestDerive_ReopenCase: adding a needs_plan child to a completed epic flips it
// back to needs_plan (E-1537 §4 reopen consequence falls out of the rule).
func TestDerive_ReopenCase(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "completed")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "confirmed")
	seedTask(t, db, 101, ptr(1), int(tasktype.TaskTypeTask), "needs_plan")

	if err := recomputeEpicStatus(db, nil, 1); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	got, completedAt := taskStatus(t, db, 1)
	if got != "needs_plan" {
		t.Fatalf("reopened epic status = %q, want needs_plan", got)
	}
	if completedAt.Valid {
		t.Errorf("reopened epic still has completed_at=%q", completedAt.String)
	}
}

// TestDerive_StickyOverrideBlocks: an epic in any sticky-override status is left
// alone regardless of its children.
func TestDerive_StickyOverrideBlocks(t *testing.T) {
	for _, sticky := range []string{"revisit", "declined", "obsolete", "blocked"} {
		t.Run(sticky, func(t *testing.T) {
			db := newDerivationDB(t)
			seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), sticky)
			seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "in_progress")
			if err := recomputeEpicStatus(db, nil, 1); err != nil {
				t.Fatalf("recompute: %v", err)
			}
			if got, _ := taskStatus(t, db, 1); got != sticky {
				t.Fatalf("sticky epic status = %q, want unchanged %q", got, sticky)
			}
		})
	}
}

// TestDerive_NestedPropagation: a grand-child status flip propagates up two epic
// levels. The change is triggered from the grand-child's parent (as the executor
// would), and both epics update.
func TestDerive_NestedPropagation(t *testing.T) {
	db := newDerivationDB(t)
	// grandparent epic (1) -> parent epic (2) -> child leaf (100)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "completed")
	seedTask(t, db, 2, ptr(1), int(tasktype.TaskTypeEpic), "completed")
	seedTask(t, db, 100, ptr(2), int(tasktype.TaskTypeTask), "confirmed")

	// Child flips to in_progress; recompute from its parent (2), as
	// execTaskStatusChanged does.
	if _, err := db.Exec("UPDATE tasks SET status = 'in_progress' WHERE id = 100"); err != nil {
		t.Fatalf("flip child: %v", err)
	}
	if err := recomputeEpicStatus(db, nil, 2); err != nil {
		t.Fatalf("recompute: %v", err)
	}

	if got, _ := taskStatus(t, db, 2); got != "in_progress" {
		t.Fatalf("parent epic status = %q, want in_progress", got)
	}
	if got, _ := taskStatus(t, db, 1); got != "in_progress" {
		t.Fatalf("grandparent epic status = %q, want in_progress (propagated)", got)
	}
}

// TestDerive_CycleHitsDepthCapWithoutPanic: a synthetic parent_id loop between
// two epics must not loop forever — the depth cap + seen set terminate it.
func TestDerive_CycleHitsDepthCapWithoutPanic(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "needs_plan")
	seedTask(t, db, 2, ptr(1), int(tasktype.TaskTypeEpic), "needs_plan")
	// Close the loop: 1's parent becomes 2.
	if _, err := db.Exec("UPDATE tasks SET parent_id = 2 WHERE id = 1"); err != nil {
		t.Fatalf("create cycle: %v", err)
	}
	// Must return (not hang / panic). State is unspecified but must be valid.
	if err := recomputeEpicStatus(db, nil, 1); err != nil {
		t.Fatalf("recompute over cycle: %v", err)
	}
}

// TestDerive_Idempotent: a second recompute with no underlying change makes no
// further mutation and invokes the emitter zero times.
func TestDerive_Idempotent(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "needs_plan")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "in_progress")

	var emits int
	emit := func(epicID int64, oldStatus, newStatus string) error { emits++; return nil }

	if err := recomputeEpicStatus(db, emit, 1); err != nil {
		t.Fatalf("recompute 1: %v", err)
	}
	if emits != 1 {
		t.Fatalf("first recompute emits = %d, want 1", emits)
	}
	if got, _ := taskStatus(t, db, 1); got != "in_progress" {
		t.Fatalf("epic status = %q, want in_progress", got)
	}

	if err := recomputeEpicStatus(db, emit, 1); err != nil {
		t.Fatalf("recompute 2: %v", err)
	}
	if emits != 1 {
		t.Fatalf("second recompute emits = %d, want still 1 (no change)", emits)
	}
}

// TestDerive_EmitterAttribution: the emitter is invoked once per actual change
// with the epic id and the old/new status.
func TestDerive_EmitterAttribution(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "needs_plan")
	seedTask(t, db, 100, ptr(1), int(tasktype.TaskTypeTask), "confirmed")

	type call struct {
		epicID     int64
		oldS, newS string
	}
	var calls []call
	emit := func(epicID int64, oldStatus, newStatus string) error {
		calls = append(calls, call{epicID, oldStatus, newStatus})
		return nil
	}

	if err := recomputeEpicStatus(db, emit, 1); err != nil {
		t.Fatalf("recompute: %v", err)
	}
	if len(calls) != 1 {
		t.Fatalf("emitter calls = %d, want 1", len(calls))
	}
	got := calls[0]
	if got.epicID != 1 || got.oldS != "needs_plan" || got.newS != "completed" {
		t.Fatalf("emit call = %+v, want {1 needs_plan completed}", got)
	}
}

// TestReplayEpicStatusDerived applies a recorded epic.status_derived event and
// asserts the projector reproduces the epic's status + completed_at without any
// derivation logic.
func TestReplayEpicStatusDerived(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "in_progress")

	evt := &Event{
		V:       Version,
		TS:      "5WYM00000010",
		Kind:    KindEpicStatusDerived,
		Project: "test",
		Entity:  EntityRef{Type: EntityTask, ID: "1"},
		Actor:   Actor{Kind: ActorSystem, ID: "epic-derivation"},
		Payload: mustJSON(t, EpicStatusDerivedPayload{TaskID: 1, OldStatus: "in_progress", NewStatus: "completed"}),
	}
	res := &ProjectResult{}
	if err := replayEpicStatusDerived(db, evt, res); err != nil {
		t.Fatalf("replay: %v", err)
	}
	got, completedAt := taskStatus(t, db, 1)
	if got != "completed" {
		t.Fatalf("replayed epic status = %q, want completed", got)
	}
	if !completedAt.Valid {
		t.Errorf("replayed completed epic has NULL completed_at")
	}
}
