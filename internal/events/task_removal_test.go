// Tests for E-2161: removal retains the tree edge, and reads re-root through
// the task_tree view.
//
// Two halves, and they are separable on purpose. The first is about what
// removal WRITES — that `parent_id` survives it, on both the single-task path
// and the bulk-clear path, because once a child's edge is nulled nothing in the
// live database can put it back. The second is about what the schema READS — the
// effective_parent_id contract every converted reader now depends on, pinned
// here as the view's own behaviour rather than re-derived at each call site.
//
// White-box, like epic_derivation_test.go beside it: these call removeTaskTree
// and removeTasksBySourceFile directly against a fresh schema-applied DB,
// reusing newDerivationDB / seedTask / ptr from that file.
package events

import (
	"database/sql"
	"testing"

	"github.com/mikeschinkel/endless/internal/tasktype"
)

// taskParentRow reads a task's LITERAL parent_id — the column, not the view — so
// these tests can tell "the edge survived" apart from "the view papered over it".
func taskParentRow(t *testing.T, db *sql.DB, id int64) (int64, bool) {
	t.Helper()
	var pid sql.NullInt64
	if err := db.QueryRow("SELECT parent_id FROM tasks WHERE id = ?", id).Scan(&pid); err != nil {
		t.Fatalf("read parent_id of task %d: %v", id, err)
	}
	return pid.Int64, pid.Valid
}

// effectiveParent reads what task_tree says a task renders under. found is false
// when the task is not in the view at all (it is removed).
func effectiveParent(t *testing.T, db *sql.DB, id int64) (parent int64, hasParent, found bool) {
	t.Helper()
	var pid sql.NullInt64
	err := db.QueryRow("SELECT effective_parent_id FROM task_tree WHERE id = ?", id).Scan(&pid)
	if err == sql.ErrNoRows {
		return 0, false, false
	}
	if err != nil {
		t.Fatalf("read effective_parent_id of task %d: %v", id, err)
	}
	return pid.Int64, pid.Valid, true
}

func seedRemovedTask(t *testing.T, db *sql.DB, id int64, parentID *int64) {
	t.Helper()
	seedTask(t, db, id, parentID, int(tasktype.TaskTypeTask), "ready")
	if _, err := db.Exec("UPDATE tasks SET removed = 1 WHERE id = ?", id); err != nil {
		t.Fatalf("mark task %d removed: %v", id, err)
	}
}

// TestRemoveTaskTree_KeepsChildParentID is the core of E-2161. Non-cascade
// removal used to null every child's parent_id, mirroring the hard-delete
// path's ON DELETE SET NULL. The child here is itself already removed — the
// case the `task remove` guard leaves reachable, since it refuses a non-cascade
// removal whose children are live — so the null protected no render and only
// destroyed the association, unrecoverably: there is no restore verb.
func TestRemoveTaskTree_KeepsChildParentID(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "ready")
	seedRemovedTask(t, db, 2, ptr(1))

	if _, err := removeTaskTree(db, 1, false); err != nil {
		t.Fatalf("removeTaskTree: %v", err)
	}

	parent, ok := taskParentRow(t, db, 2)
	if !ok || parent != 1 {
		t.Errorf("child parent_id = (%d, %v), want (1, true) — removal must not null the edge", parent, ok)
	}
}

// TestRemoveTaskTree_LiveChildRendersUnderGrandparent is the read-time half of
// the same trade. Nulling was load-bearing for a LIVE child: every tree read
// joins live_tasks to live_tasks, so a child pointing at a hidden row would hang
// off nothing and vanish from every render. Retaining the edge is only safe
// because task_tree adopts the child upward instead.
func TestRemoveTaskTree_LiveChildRendersUnderGrandparent(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "ready")
	seedTask(t, db, 2, ptr(1), int(tasktype.TaskTypeEpic), "ready")
	seedTask(t, db, 3, ptr(2), int(tasktype.TaskTypeTask), "ready")

	if _, err := removeTaskTree(db, 2, false); err != nil {
		t.Fatalf("removeTaskTree: %v", err)
	}

	if parent, ok := taskParentRow(t, db, 3); !ok || parent != 2 {
		t.Errorf("literal parent_id = (%d, %v), want (2, true)", parent, ok)
	}
	parent, hasParent, found := effectiveParent(t, db, 3)
	if !found {
		t.Fatal("the live child fell out of task_tree entirely")
	}
	if !hasParent || parent != 1 {
		t.Errorf("effective_parent_id = (%d, %v), want (1, true) — the child must render under its grandparent",
			parent, hasParent)
	}
}

// TestRemoveTasksBySourceFile_KeepsChildParentID covers the bulk half
// (`task import --replace`). Its clear was WIDER than the single-task one: it
// nulled every child of every task the source file owned, including children
// owned by a different source file that were never part of the import.
func TestRemoveTasksBySourceFile_KeepsChildParentID(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "ready")
	if _, err := db.Exec("UPDATE tasks SET source_file = 'plan.md' WHERE id = 1"); err != nil {
		t.Fatalf("set source_file: %v", err)
	}
	// A child that is NOT part of the import, and never was.
	seedTask(t, db, 2, ptr(1), int(tasktype.TaskTypeTask), "ready")

	if _, err := removeTasksBySourceFile(db, 1, "plan.md"); err != nil {
		t.Fatalf("removeTasksBySourceFile: %v", err)
	}

	if parent, ok := taskParentRow(t, db, 2); !ok || parent != 1 {
		t.Errorf("child parent_id = (%d, %v), want (1, true) — a re-import must not orphan it", parent, ok)
	}
	if _, hasParent, found := effectiveParent(t, db, 2); !found || hasParent {
		t.Errorf("child in view = %v with a parent = %v; want present and rendering at the root",
			found, hasParent)
	}
}

// TestTaskTree_EffectiveParentContract pins the view itself, case by case. Every
// converted reader rests on exactly these answers, and a reader cannot restate
// them — that is the reason the rule is a view and not an expression repeated at
// each site.
func TestTaskTree_EffectiveParentContract(t *testing.T) {
	db := newDerivationDB(t)

	// 1 root (live) → 2 removed → 3 live: 3 is adopted by 1.
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "ready")
	seedRemovedTask(t, db, 2, ptr(1))
	seedTask(t, db, 3, ptr(2), int(tasktype.TaskTypeTask), "ready")
	// 4 under 3: an ordinary edge, untouched by any of this.
	seedTask(t, db, 4, ptr(3), int(tasktype.TaskTypeTask), "ready")
	// 5 removed root → 6 live: no live ancestor at all, so 6 renders at the root.
	seedRemovedTask(t, db, 5, nil)
	seedTask(t, db, 6, ptr(5), int(tasktype.TaskTypeTask), "ready")
	// 7 removed → 8 removed → 9 live: the walk steps over BOTH.
	seedRemovedTask(t, db, 7, ptr(1))
	seedRemovedTask(t, db, 8, ptr(7))
	seedTask(t, db, 9, ptr(8), int(tasktype.TaskTypeTask), "ready")

	tests := []struct {
		name      string
		id        int64
		want      int64
		wantValid bool
	}{
		{"a true root has no effective parent", 1, 0, false},
		{"a live child of a removed parent is adopted by the grandparent", 3, 1, true},
		{"an ordinary edge is unchanged", 4, 3, true},
		{"no live ancestor at all renders at the root", 6, 0, false},
		{"the walk crosses more than one removed ancestor", 9, 1, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parent, hasParent, found := effectiveParent(t, db, tc.id)
			if !found {
				t.Fatalf("task %d is not in task_tree", tc.id)
			}
			if hasParent != tc.wantValid || (hasParent && parent != tc.want) {
				t.Errorf("effective_parent_id = (%d, %v), want (%d, %v)",
					parent, hasParent, tc.want, tc.wantValid)
			}
		})
	}

	// The view is a superset of live_tasks in COLUMNS, not in rows: every
	// converted reader swapped one for the other and must not have gained a
	// removed row by doing so.
	var live, tree int
	if err := db.QueryRow("SELECT count(*) FROM live_tasks").Scan(&live); err != nil {
		t.Fatalf("count live_tasks: %v", err)
	}
	if err := db.QueryRow("SELECT count(*) FROM task_tree").Scan(&tree); err != nil {
		t.Fatalf("count task_tree: %v", err)
	}
	if live != tree {
		t.Errorf("task_tree holds %d rows, live_tasks %d — they must hold the same rows", tree, live)
	}
}

// TestTaskTree_DanglingAndCyclicChainsYieldNull covers the two malformed shapes
// the walk has to survive rather than spin on. Both answer "renders at the root",
// which is the only answer that keeps a broken row visible.
func TestTaskTree_DanglingAndCyclicChainsYieldNull(t *testing.T) {
	db := newDerivationDB(t)
	// FKs are on in newDerivationDB, so a dangling parent_id has to be written
	// with them off — which is also the only way the real one arose, under a
	// connection that predates the enforcement.
	if _, err := db.Exec("PRAGMA foreign_keys=OFF"); err != nil {
		t.Fatalf("disable fks: %v", err)
	}
	seedTask(t, db, 1, ptr(9999), int(tasktype.TaskTypeTask), "ready")
	seedRemovedTask(t, db, 2, ptr(3))
	seedRemovedTask(t, db, 3, ptr(2))
	seedTask(t, db, 4, ptr(2), int(tasktype.TaskTypeTask), "ready")

	for _, id := range []int64{1, 4} {
		parent, hasParent, found := effectiveParent(t, db, id)
		if !found {
			t.Errorf("task %d fell out of task_tree", id)
			continue
		}
		if hasParent {
			t.Errorf("task %d: effective_parent_id = %d, want NULL", id, parent)
		}
	}
}

// TestEpicDerivation_CountsAnAdoptedGrandchild is the pairing the roll-up cannot
// do without. deriveTargetStatus counts children by effective_parent_id, so the
// walk that decides WHICH epics to recompute has to use the same notion of
// parent — otherwise a change to the adopted grandchild never reaches the epic
// that counts it, and the derived status it just contributed to goes stale.
func TestEpicDerivation_CountsAnAdoptedGrandchild(t *testing.T) {
	db := newDerivationDB(t)
	seedTask(t, db, 1, nil, int(tasktype.TaskTypeEpic), "ready")
	seedRemovedTask(t, db, 2, ptr(1))
	seedTask(t, db, 3, ptr(2), int(tasktype.TaskTypeTask), "underway")

	parent, ok, err := taskEffectiveParentID(db, 3)
	if err != nil {
		t.Fatalf("taskEffectiveParentID: %v", err)
	}
	if !ok || parent != 1 {
		t.Fatalf("taskEffectiveParentID(3) = (%d, %v), want (1, true)", parent, ok)
	}

	if err := recomputeEpicStatus(db, nil, parent); err != nil {
		t.Fatalf("recomputeEpicStatus: %v", err)
	}
	if status, _ := taskStatus(t, db, 1); status != "underway" {
		t.Errorf("epic status = %q, want %q — the adopted grandchild must roll up", status, "underway")
	}
}
