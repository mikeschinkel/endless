package monitor

import (
	"database/sql"
	"testing"
)

func sgDep(t *testing.T, db *sql.DB, from, to int64, depType string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO task_deps (source_type, source_id, target_type, target_id, dep_type)
		 VALUES ('task', ?, 'task', ?, ?)`, from, to, depType,
	); err != nil {
		t.Fatalf("dep %d %s %d: %v", from, depType, to, err)
	}
}

// E-2164: the node set is the seeds plus every OPEN blocker, walked
// transitively and regardless of phase or claim; a terminal blocker imposes no
// order and is not walked.
func TestSessionGraphData_AddsOpenBlockersTransitively(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")

	const seed, laterBlocker, farBlocker, doneBlocker, inFlightBlocker, unrelated = 10, 20, 30, 40, 50, 60
	snTask(t, db, seed, 1, "ready", "now", "p")
	snTask(t, db, laterBlocker, 1, "ready", "later", "p")
	snTask(t, db, farBlocker, 1, "unverified", "now", "p") // unverified still blocks
	snTask(t, db, doneBlocker, 1, "confirmed", "now", "p")
	snTask(t, db, inFlightBlocker, 1, "underway", "now", "p")
	snTask(t, db, unrelated, 1, "ready", "now", "p")
	snSession(t, db, 7, 1, inFlightBlocker, "working")

	snBlocks(t, db, laterBlocker, seed)
	snBlocks(t, db, farBlocker, laterBlocker)
	snBlocks(t, db, doneBlocker, seed)
	snBlocks(t, db, inFlightBlocker, seed)
	sgDep(t, db, seed, unrelated, DepPrecedes) // unrelated is not a seed: not drawn

	got, err := SessionGraphData([]int64{seed})
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	for _, id := range []int64{seed, laterBlocker, farBlocker, inFlightBlocker} {
		if _, ok := got.Nodes[id]; !ok {
			t.Errorf("E-%d missing from node set", id)
		}
	}
	for _, id := range []int64{doneBlocker, unrelated} {
		if _, ok := got.Nodes[id]; ok {
			t.Errorf("E-%d must not be in the node set", id)
		}
	}
	if !got.Nodes[inFlightBlocker].InFlight {
		t.Errorf("in-flight blocker not flagged in flight")
	}
	if got.Nodes[laterBlocker].InFlight {
		t.Errorf("idle blocker flagged in flight")
	}
	if len(got.Edges) != 3 {
		t.Fatalf("edges = %+v, want the three open blocks edges", got.Edges)
	}
	for _, e := range got.Edges {
		if e.Type != DepBlocks {
			t.Errorf("unexpected edge %+v", e)
		}
	}
}

func TestSessionGraphData_CarriesPrecedesAndConflictsAmongNodes(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	for _, id := range []int64{1, 2, 3} {
		snTask(t, db, id, 1, "ready", "now", "p")
	}
	snTask(t, db, 4, 1, "confirmed", "now", "p")
	sgDep(t, db, 1, 2, DepPrecedes)
	sgDep(t, db, 3, 2, DepConflictsWith)
	sgDep(t, db, 1, 3, "relates_to") // not an ordering relation

	got, err := SessionGraphData([]int64{1, 2, 3, 4})
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	if _, ok := got.Nodes[4]; ok {
		t.Errorf("a terminal seed has nothing left to order")
	}
	want := []GraphEdge{{1, 2, DepPrecedes}, {3, 2, DepConflictsWith}}
	if len(got.Edges) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got.Edges, want)
	}
	for i := range want {
		if got.Edges[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, got.Edges[i], want[i])
		}
	}
}

func TestSessionGraphData_NoSeedsNoDatabase(t *testing.T) {
	got, err := SessionGraphData(nil)
	if err != nil || len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Fatalf("SessionGraphData(nil) = %+v, %v; want empty and no error", got, err)
	}
}
