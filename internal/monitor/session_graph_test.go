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

	got, err := SessionGraphData([]int64{seed}, 0)
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

	got, err := SessionGraphData([]int64{1, 2, 3, 4}, 0)
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	if _, ok := got.Nodes[4]; !ok {
		t.Errorf("a terminal seed is kept: the caller alone decides the starting set (E-2345)")
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
	got, err := SessionGraphData(nil, 0)
	if err != nil || len(got.Nodes) != 0 || len(got.Edges) != 0 {
		t.Fatalf("SessionGraphData(nil) = %+v, %v; want empty and no error", got, err)
	}
}

func sgHas(t *testing.T, got GraphData, want, notWant []int64) {
	t.Helper()
	for _, id := range want {
		if _, ok := got.Nodes[id]; !ok {
			t.Errorf("E-%d missing from node set", id)
		}
	}
	for _, id := range notWant {
		if _, ok := got.Nodes[id]; ok {
			t.Errorf("E-%d must not be in the node set", id)
		}
	}
}

// E-2345 P2: a confirmed own task that has not landed is still a seed, and its
// open blockers are drawn.
func TestSessionGraphData_TerminalOwnTaskBlockersDrawn(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	const own, blocker = 10, 20
	snTask(t, db, own, 1, "confirmed", "now", "p")
	snTask(t, db, blocker, 1, "ready", "now", "p")
	snBlocks(t, db, blocker, own)

	got, err := SessionGraphData([]int64{own}, own)
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	sgHas(t, got, []int64{own, blocker}, nil)
	if len(got.Edges) != 1 || got.Edges[0] != (GraphEdge{blocker, own, DepBlocks}) {
		t.Fatalf("edges = %+v, want the one blocks edge", got.Edges)
	}
}

// E-2345 P3: the E-2206 => E-2208 => E-2333 shape. The own task's blocker is
// in another project, and that blocker's blocker is another live session's
// task; the whole chain is drawn.
func TestSessionGraphData_CrossProjectChainToOwnTask(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "endless", "/endless")
	seedProject(t, db, 2, "go-cfgstore", "/go-cfgstore")
	const own, mid, root = 2333, 2208, 2206
	snTask(t, db, own, 1, "underway", "now", "p")
	snTask(t, db, mid, 2, "ready", "now", "p")
	snTask(t, db, root, 2, "underway", "now", "p")
	snSession(t, db, 7, 1, own, "working")
	snSession(t, db, 8, 2, root, "working")
	snBlocks(t, db, mid, own)
	snBlocks(t, db, root, mid)

	got, err := SessionGraphData([]int64{own}, own)
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	sgHas(t, got, []int64{own, mid, root}, nil)
	if got.Nodes[mid].ProjectID != 2 || got.Nodes[root].ProjectID != 2 {
		t.Errorf("blockers lost their project: %+v", got.Nodes)
	}
	if !got.Nodes[root].InFlight {
		t.Errorf("E-%d is another live session's task: want in flight", root)
	}
	want := []GraphEdge{{root, mid, DepBlocks}, {mid, own, DepBlocks}}
	if len(got.Edges) != 2 || got.Edges[0] != want[0] || got.Edges[1] != want[1] {
		t.Fatalf("edges = %+v, want %+v", got.Edges, want)
	}
}

// E-2345 P4 and P8: the own task's open downstream tasks, by blocks and by
// precedes, are drawn one step only; a downstream task's blockers are walked
// to the end of their chain, three deep here. A terminal downstream task, and
// downstream of anything but the own task, are not added.
func TestSessionGraphData_DownstreamOneStepWithBlockerChains(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	const own, blocked, preceded, doneDown, grandchild = 10, 20, 21, 22, 30
	const b1, b2, b3 = 41, 42, 43
	const otherSeed, otherDown = 50, 51
	for _, id := range []int64{own, blocked, preceded, grandchild, b1, b2, b3, otherSeed, otherDown} {
		snTask(t, db, id, 1, "ready", "now", "p")
	}
	snTask(t, db, doneDown, 1, "confirmed", "now", "p")
	snBlocks(t, db, own, blocked)
	sgDep(t, db, own, preceded, DepPrecedes)
	snBlocks(t, db, own, doneDown)
	snBlocks(t, db, blocked, grandchild) // downstream of downstream: not added
	snBlocks(t, db, b1, blocked)
	snBlocks(t, db, b2, b1)
	snBlocks(t, db, b3, b2)
	snBlocks(t, db, otherSeed, otherDown) // downstream of a non-own seed: not added

	got, err := SessionGraphData([]int64{own, otherSeed}, own)
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	sgHas(t, got,
		[]int64{own, blocked, preceded, b1, b2, b3, otherSeed},
		[]int64{doneDown, grandchild, otherDown})

	// No own task: no downstream at all.
	got, err = SessionGraphData([]int64{own}, 0)
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	sgHas(t, got, []int64{own}, []int64{blocked, preceded})
}

// E-2345 P9: a blocks cycle ends the walk without repeating a task, and every
// edge of the cycle is returned for the layout to draw as its cycle line.
func TestSessionGraphData_BlocksCycleTerminates(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	for _, id := range []int64{1, 2, 3} {
		snTask(t, db, id, 1, "ready", "now", "p")
	}
	snBlocks(t, db, 2, 1)
	snBlocks(t, db, 3, 2)
	snBlocks(t, db, 1, 3)

	got, err := SessionGraphData([]int64{1}, 1)
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	if len(got.Nodes) != 3 {
		t.Fatalf("nodes = %+v, want exactly E-1, E-2, E-3", got.Nodes)
	}
	if len(got.Edges) != 3 {
		t.Fatalf("edges = %+v, want the three cycle edges", got.Edges)
	}
}

// E-2455: a task that should precede a graph node is walked exactly as a
// blocker is. The E-2444 -> E-2445 shape: the predecessor is another live
// session's task, so it is not a seed, yet it enters with its `precedes` edge;
// its own blocker and soft predecessor follow it in. A terminal predecessor
// imposes no order and is not walked.
func TestSessionGraphData_WalksPrecedesPredecessors(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "p1", "/p1")
	const seed, pred, predBlocker, predPred, donePred = 2445, 2444, 30, 31, 40
	for _, id := range []int64{seed, predBlocker, predPred} {
		snTask(t, db, id, 1, "ready", "now", "p")
	}
	snTask(t, db, pred, 1, "underway", "now", "p")
	snTask(t, db, donePred, 1, "confirmed", "now", "p")
	snSession(t, db, 8, 1, pred, "working")
	sgDep(t, db, pred, seed, DepPrecedes)
	snBlocks(t, db, predBlocker, pred)
	sgDep(t, db, predPred, pred, DepPrecedes)
	sgDep(t, db, donePred, seed, DepPrecedes)

	got, err := SessionGraphData([]int64{seed}, 0)
	if err != nil {
		t.Fatalf("SessionGraphData: %v", err)
	}
	sgHas(t, got, []int64{seed, pred, predBlocker, predPred}, []int64{donePred})
	if !got.Nodes[pred].InFlight {
		t.Errorf("E-%d is another live session's task: want in flight", pred)
	}
	want := []GraphEdge{{predBlocker, pred, DepBlocks}, {predPred, pred, DepPrecedes}, {pred, seed, DepPrecedes}}
	if len(got.Edges) != len(want) {
		t.Fatalf("edges = %+v, want %+v", got.Edges, want)
	}
	for i := range want {
		if got.Edges[i] != want[i] {
			t.Errorf("edge %d = %+v, want %+v", i, got.Edges[i], want[i])
		}
	}
}
