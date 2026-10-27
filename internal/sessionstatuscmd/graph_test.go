package sessionstatuscmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// TestMain defaults the graph's database seam to an empty graph, so every
// table test that stubs the row source stays database-free. Graph tests
// install their own data with stubGraph.
func TestMain(m *testing.M) {
	graphData = func([]int64, int64) (monitor.GraphData, error) {
		return monitor.GraphData{Nodes: map[int64]monitor.GraphNode{}}, nil
	}
	graphProjectRoot = func(int64) (string, error) { return "", nil }
	os.Exit(m.Run())
}

// gb builds graph input tersely: nodes are open, idle, on-list unless marked.
type gb struct {
	nodes     map[int64]graphNode
	edges     []graphEdge
	conflicts []graphConflict
}

func newGB(ids ...int64) *gb {
	b := &gb{nodes: map[int64]graphNode{}}
	for _, id := range ids {
		b.nodes[id] = graphNode{ID: id, Status: "ready", Phase: "now", OnList: true}
	}
	return b
}

func (b *gb) blocks(from, to int64) *gb {
	b.edges = append(b.edges, graphEdge{from, to, edgeBlocks})
	return b
}

func (b *gb) precedes(from, to int64) *gb {
	b.edges = append(b.edges, graphEdge{from, to, edgePrecedes})
	return b
}

func (b *gb) conflict(a, c int64) *gb {
	b.conflicts = append(b.conflicts, graphConflict{A: a, B: c, Declared: true})
	return b
}

func (b *gb) build() orderGraph { return buildOrderGraph(b.nodes, b.edges, b.conflicts) }

func assertLines(t *testing.T, g orderGraph, want ...string) {
	t.Helper()
	got := g.plainLines()
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lines:\n  got  %q\n  want %q", got, want)
	}
}

// The plan's own example: a task with two blockers shows both, one line each;
// the chain with the longest run leads; `|` groups share their left side.
func TestGraph_PlanExample(t *testing.T) {
	g := newGB(1813, 1814, 1815, 1992, 1993, 1994, 1995).
		blocks(1813, 1814).blocks(1813, 1994).blocks(1815, 1814).
		blocks(1992, 1993).blocks(1993, 1994).blocks(1994, 1995).build()
	assertLines(t, g,
		"E-1992 => E-1993 => E-1994 => E-1995",
		"E-1813 => E-1814 | E-1994",
		"E-1815 => E-1814",
	)
}

// `|` binds tighter than the arrows: a diamond is one line.
func TestGraph_DiamondIsOneLine(t *testing.T) {
	g := newGB(1, 2, 3, 4).blocks(1, 2).blocks(1, 3).blocks(2, 4).blocks(3, 4).build()
	assertLines(t, g, "E-1 => E-2 | E-3 => E-4")
}

// A group only continues where every member shares the right side too: E-3
// does not block E-4, so the grouped line stops and E-2's own successor gets a
// line of its own rather than `E-1 => E-2 | E-3 => E-4`, which would invent an
// ordering the data does not have.
func TestGraph_GroupNeverOverstates(t *testing.T) {
	g := newGB(1, 2, 3, 4).blocks(1, 2).blocks(1, 3).blocks(2, 4).build()
	assertLines(t, g, "E-1 => E-2 | E-3", "E-2 => E-4")
}

// Two blockers that stand in identical relations share one line as a leading
// group; two that differ get a line each, the shared task's repeat dimmed.
func TestGraph_TwinBlockersShareALine(t *testing.T) {
	g := newGB(1, 2, 3).blocks(1, 3).blocks(2, 3).build()
	assertLines(t, g, "E-1 | E-2 => E-3")
	g = newGB(1, 2, 3, 4).blocks(1, 3).blocks(2, 3).blocks(1, 4).build()
	assertLines(t, g, "E-1 => E-3 | E-4", "E-2 => E-3")
}

func TestGraph_PrecedesRendersAdvisoryArrowAndOrdersFirst(t *testing.T) {
	g := newGB(5, 9, 20, 30).precedes(20, 5).blocks(9, 30).build()
	// 20 must lead a line before 5 appears; ties on chain length go to the
	// lowest id, so 9 => 30 comes first and 20 -> 5 next.
	assertLines(t, g, "E-9 => E-30", "E-20 -> E-5")
}

func TestGraph_BlocksWinsOverPrecedesOnOnePair(t *testing.T) {
	g := newGB(1, 2).precedes(1, 2).blocks(1, 2).build()
	assertLines(t, g, "E-1 => E-2")
	if len(g.edges) != 1 || g.edges[0].Kind != edgeBlocks {
		t.Fatalf("edges = %+v", g.edges)
	}
}

func TestGraph_NoEdgesRendersNothing(t *testing.T) {
	g := newGB(1, 2, 3).build()
	if !g.empty() {
		t.Fatalf("a graph with no edges must be empty: %q", g.plainLines())
	}
	var b bytes.Buffer
	renderGraph(&b, g, 80, true)
	if b.Len() != 0 {
		t.Fatalf("empty graph wrote %q, want nothing — no header, no blank", b.String())
	}
}

func TestGraph_TaskWithNoEdgeNeverAppears(t *testing.T) {
	g := newGB(1, 2, 3).blocks(1, 2).build()
	if _, ok := g.nodes[3]; ok {
		t.Fatalf("an edgeless task must not appear")
	}
}

// Conflicts qualify a task on their own; an ordering edge between the pair
// replaces the `<>`.
func TestGraph_ConflictOnlyTasksAppear(t *testing.T) {
	g := newGB(1, 2).conflict(2, 1).build()
	assertLines(t, g, "E-1 <> E-2")
	g = newGB(1, 2).conflict(1, 2).blocks(2, 1).build()
	assertLines(t, g, "E-2 => E-1")
	if len(g.conflicts) != 0 {
		t.Fatalf("an ordered pair must not also be a conflict: %+v", g.conflicts)
	}
}

func TestGraph_MutualConflictIsOneSetLine(t *testing.T) {
	g := newGB(2142, 2159, 2164).conflict(2142, 2159).conflict(2159, 2164).conflict(2142, 2164).build()
	assertLines(t, g, "<> E-2142 | E-2159 | E-2164")
}

// `<>` is not transitive, so a chain-shaped conflict graph is pairs, never a
// set and never a chain.
func TestGraph_ChainShapedConflictIsPairs(t *testing.T) {
	g := newGB(1, 2, 3).conflict(1, 2).conflict(2, 3).build()
	assertLines(t, g, "E-1 <> E-2", "E-2 <> E-3")
}

func TestGraph_SetsOrderBySizeThenLowestIDThenPairs(t *testing.T) {
	g := newGB(1, 2, 3, 4, 5, 6, 7, 8, 9).
		conflict(7, 8).conflict(8, 9).conflict(7, 9).
		conflict(1, 2).conflict(1, 3).conflict(1, 4).conflict(2, 3).conflict(2, 4).conflict(3, 4).
		conflict(5, 6).build()
	assertLines(t, g, "<> E-1 | E-2 | E-3 | E-4", "<> E-7 | E-8 | E-9", "E-5 <> E-6")
}

func TestGraph_CycleIsReportedNotTruncated(t *testing.T) {
	g := newGB(1, 2, 3, 4).blocks(1, 2).blocks(2, 3).blocks(3, 1).blocks(3, 4).build()
	assertLines(t, g, "E-3 => E-4", "cycle: E-1, E-2, E-3")
	if !reflect.DeepEqual(g.cycles, [][]int64{{1, 2, 3}}) {
		t.Fatalf("cycles = %v", g.cycles)
	}
}

// Every occurrence of an id after its first is dim. An in-flight id is not
// (E-2345): until it lands it still holds back what it blocks.
func TestGraph_RepeatsDimInFlightDoesNot(t *testing.T) {
	b := newGB(1, 2, 3, 4)
	n := b.nodes[4]
	n.InFlight, n.OnList = true, false
	b.nodes[4] = n
	g := b.blocks(1, 3).blocks(2, 3).blocks(4, 2).build()
	// 4 => 2 => 3 leads (longest chain); 1 => 3 repeats E-3.
	assertLines(t, g, "E-4 => E-2 => E-3", "E-1 => E-3")
	var out bytes.Buffer
	renderGraph(&out, g, 200, true)
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if !strings.HasPrefix(lines[0], ansiBold+"E-4"+ansiReset) {
		t.Errorf("in-flight E-4 should be normal, not dim: %q", lines[0])
	}
	if !strings.Contains(lines[0], ansiBold+"E-3") {
		t.Errorf("first E-3 should be bright: %q", lines[0])
	}
	if !strings.HasSuffix(lines[1], dim("E-3", true)) {
		t.Errorf("repeated E-3 should be dim: %q", lines[1])
	}
	if !strings.Contains(lines[1], dim(" => ", true)) {
		t.Errorf("punctuation should be dim: %q", lines[1])
	}
}

func TestGraph_WrapsAtArrowUnderFirstGroup(t *testing.T) {
	g := newGB(1992, 1993, 1994, 1995).blocks(1992, 1993).blocks(1993, 1994).blocks(1994, 1995).build()
	var out bytes.Buffer
	renderGraph(&out, g, 20, false)
	want := "E-1992 => E-1993\n" +
		"       => E-1994\n" +
		"       => E-1995\n"
	if out.String() != want {
		t.Fatalf("wrapped:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestGraph_GroupIsNeverSplit(t *testing.T) {
	g := newGB(1, 2, 3, 4).blocks(1, 2).blocks(1, 3).blocks(1, 4).build()
	var out bytes.Buffer
	renderGraph(&out, g, 10, false)
	if out.String() != "E-1\n    => E-2 | E-3 | E-4\n" {
		t.Fatalf("got %q", out.String())
	}
}

func TestGraph_ByteStableAcrossRenders(t *testing.T) {
	build := func() string {
		g := newGB(1, 2, 3, 4, 5, 6, 7).
			blocks(1, 2).blocks(1, 3).blocks(3, 4).precedes(5, 4).
			conflict(6, 7).conflict(2, 6).build()
		var b bytes.Buffer
		renderGraph(&b, g, 80, true)
		return b.String()
	}
	first := build()
	for i := 0; i < 20; i++ {
		if got := build(); got != first {
			t.Fatalf("render %d differs:\n%q\n%q", i, first, got)
		}
	}
}

// E-2345: the starting set is every unlanded row that is the own task, at any
// status, or that the table draws bright and another session does not steward.
func TestGraphSeeds_BrightUnlandedRowsAndOwnTask(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1, Status: "underway", Phase: "now", IsFocal: true},                                  // own task: in
		{ID: 2, Status: "ready", Phase: "now", IsFrom: true},                                      // spawning task, bright: in
		{ID: 3, Status: "ready", Phase: "now", IsParent: true},                                    // parent dims: out
		{ID: 4, Status: "underway", Phase: "now", InFlight: true},                                 // spawned elsewhere dims: out
		{ID: 5, Status: "ready", Phase: "now", Hidden: true},                                      // hidden dims: out
		{ID: 6, Status: "ready", Phase: "later"},                                                  // later dims: out
		{ID: 7, Status: "confirmed", Phase: "now"},                                                // terminal dims: out
		{ID: 8, Status: "ready", Phase: "now", StewardedElsewhere: true},                          // suppressed: out
		{ID: 9, Status: "ready", Phase: "now"},                                                    // bright: in
		{ID: 10, Status: "unplanned", Phase: "urgent"},                                            // bold: in
		{ID: 11, Status: "ready", Phase: "now", Landed: true},                                     // landed: out
		{ID: 12, Status: "confirmed", Phase: "now", Unsettled: true},                              // unsettled vetoes dim: in
		{ID: 13, Status: "ready", Phase: "maybe"},                                                 // maybe dims: out
		{ID: 14, Status: "ready", Phase: "now", Relation: sessiontaskrelation.RelationReferenced}, // referenced dims: out
	}
	if got := graphSeeds(rows); !reflect.DeepEqual(got, []int64{1, 2, 9, 10, 12}) {
		t.Fatalf("seeds = %v, want [1 2 9 10 12]", got)
	}
}

// The own task stays in at a terminal status, and even when stewarded
// elsewhere, until it lands.
func TestGraphSeeds_OwnTaskUntilLanded(t *testing.T) {
	own := monitor.SessionStatusRow{ID: 1, Status: "confirmed", Phase: "later", IsFocal: true, StewardedElsewhere: true}
	if got := graphSeeds([]monitor.SessionStatusRow{own}); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("unlanded confirmed own task: seeds = %v, want [1]", got)
	}
	own.Landed = true
	if got := graphSeeds([]monitor.SessionStatusRow{own}); len(got) != 0 {
		t.Fatalf("landed own task: seeds = %v, want none", got)
	}
}

// The E-2333 shape: every row but the own task is excluded, yet the own task
// seeds the graph and is handed over as the task whose downstream to add.
func TestGatherGraph_OwnTaskSeedsAndIsPassedAsOwn(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 2333, Status: "underway", Phase: "now", IsFocal: true},
		{ID: 2240, Status: "underway", Phase: "now", InFlight: true},
		{ID: 2206, Status: "underway", Phase: "now", StewardedElsewhere: true},
		{ID: 2208, Status: "ready", Phase: "now", StewardedElsewhere: true},
	}
	var gotSeeds []int64
	var gotOwn int64
	prev := graphData
	t.Cleanup(func() { graphData = prev })
	graphData = func(seeds []int64, own int64) (monitor.GraphData, error) {
		gotSeeds, gotOwn = seeds, own
		return monitor.GraphData{
			Nodes: map[int64]monitor.GraphNode{2333: gNode(2333, true), 2208: gNode(2208, false), 2206: gNode(2206, true)},
			Edges: []monitor.GraphEdge{
				{From: 2206, To: 2208, Type: monitor.DepBlocks},
				{From: 2208, To: 2333, Type: monitor.DepBlocks},
			},
		}, nil
	}
	g, err := gatherGraph(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotSeeds, []int64{2333}) || gotOwn != 2333 {
		t.Fatalf("graphData(%v, %d), want ([2333], 2333)", gotSeeds, gotOwn)
	}
	assertLines(t, g, "E-2206 => E-2208 => E-2333")

	// Landed own task: not seeded, so no downstream either.
	rows[0].Landed = true
	if _, err = gatherGraph(rows); err != nil {
		t.Fatal(err)
	}
	if len(gotSeeds) != 0 || gotOwn != 0 {
		t.Fatalf("landed own task: graphData(%v, %d), want ([], 0)", gotSeeds, gotOwn)
	}
}

// The E-2444 -> E-2445 shape (E-2455): the predecessor's row is stewarded by
// another session, so it does not seed the graph, but the walk brings it in
// and the soft edge is drawn as `->` with the predecessor at the head.
func TestGatherGraph_StewardedElsewherePrecedesPredecessorDrawn(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 2444, Status: "underway", Phase: "now", StewardedElsewhere: true},
		{ID: 2445, Status: "ready", Phase: "now"},
	}
	var gotSeeds []int64
	prev := graphData
	t.Cleanup(func() { graphData = prev })
	graphData = func(seeds []int64, own int64) (monitor.GraphData, error) {
		gotSeeds = seeds
		return monitor.GraphData{
			Nodes: map[int64]monitor.GraphNode{2444: gNode(2444, true), 2445: gNode(2445, false)},
			Edges: []monitor.GraphEdge{{From: 2444, To: 2445, Type: monitor.DepPrecedes}},
		}, nil
	}
	g, err := gatherGraph(rows)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotSeeds, []int64{2445}) {
		t.Fatalf("seeds = %v, want [2445]: a stewarded row does not seed the graph", gotSeeds)
	}
	assertLines(t, g, "E-2444 -> E-2445")
	if g.nodes[2444].OnList {
		t.Errorf("E-2444 entered through the walk: want off-list")
	}
}

// stubGraph installs database and cache answers for one test.
func stubGraph(t *testing.T, data monitor.GraphData, changed map[int64][]string) {
	t.Helper()
	prevData, prevRoot, prevChanged := graphData, graphProjectRoot, graphWorktreeChanged
	t.Cleanup(func() { graphData, graphProjectRoot, graphWorktreeChanged = prevData, prevRoot, prevChanged })
	graphData = func([]int64, int64) (monitor.GraphData, error) { return data, nil }
	graphProjectRoot = func(int64) (string, error) { return "/proj", nil }
	graphWorktreeChanged = func(root string, id int64) ([]string, bool) {
		p, ok := changed[id]
		return p, ok
	}
}

func gNode(id int64, inFlight bool) monitor.GraphNode {
	return monitor.GraphNode{ID: id, ProjectID: 1, Status: "ready", Phase: "now", InFlight: inFlight}
}

// Detected and declared conflicts render the same `<>`; --json says which.
// With the cache empty the graph still renders its ordering, and no `<>`.
func TestGatherGraph_DetectedAndDeclaredConflicts(t *testing.T) {
	data := monitor.GraphData{
		Nodes: map[int64]monitor.GraphNode{
			10: gNode(10, false), 11: gNode(11, false), 12: gNode(12, false), 13: gNode(13, false),
		},
		Edges: []monitor.GraphEdge{
			{From: 10, To: 11, Type: monitor.DepConflictsWith},
			{From: 12, To: 13, Type: monitor.DepConflictsWith},
		},
	}
	rows := []monitor.SessionStatusRow{
		{ID: 10, Status: "ready", Phase: "now"}, {ID: 11, Status: "ready", Phase: "now"},
		{ID: 12, Status: "ready", Phase: "now"}, {ID: 13, Status: "ready", Phase: "now"},
	}

	stubGraph(t, data, map[int64][]string{10: {"a.go", "b.go"}, 11: {"b.go"}, 13: {"c.go"}, 14: {"c.go"}})
	g, err := gatherGraph(rows)
	if err != nil {
		t.Fatal(err)
	}
	assertLines(t, g, "E-10 <> E-11", "E-12 <> E-13")
	js := g.toJSON()
	if js.Conflicts[0].Source != "both" || !reflect.DeepEqual(js.Conflicts[0].Paths, []string{"b.go"}) {
		t.Errorf("10<>11 = %+v, want both with shared path b.go", js.Conflicts[0])
	}
	if js.Conflicts[1].Source != "declared" {
		t.Errorf("12<>13 = %+v, want declared", js.Conflicts[1])
	}

	// Detected only: no declared rows, overlapping worktrees.
	stubGraph(t, monitor.GraphData{Nodes: data.Nodes}, map[int64][]string{12: {"x.go"}, 13: {"x.go"}})
	g, _ = gatherGraph(rows)
	assertLines(t, g, "E-12 <> E-13")
	if g.toJSON().Conflicts[0].Source != "detected" {
		t.Errorf("want detected: %+v", g.toJSON().Conflicts)
	}

	// Cache empty: no `<>` at all, and nothing else breaks.
	stubGraph(t, monitor.GraphData{Nodes: data.Nodes}, nil)
	g, _ = gatherGraph(rows)
	if !g.empty() {
		t.Fatalf("empty cache and no declared rows must draw nothing: %q", g.plainLines())
	}
}

// An off-list blocker renders; --json carries
// the same lines the text draws, from one fixture.
func TestGatherGraph_OffListBlockerAndJSONParity(t *testing.T) {
	data := monitor.GraphData{
		Nodes: map[int64]monitor.GraphNode{
			20: gNode(20, false), 21: gNode(21, true), 30: gNode(30, false), 31: gNode(31, false),
		},
		Edges: []monitor.GraphEdge{
			{From: 21, To: 20, Type: monitor.DepBlocks},
			{From: 30, To: 31, Type: monitor.DepPrecedes},
		},
	}
	rows := []monitor.SessionStatusRow{
		{ID: 20, Status: "ready", Phase: "now"}, {ID: 30, Status: "ready", Phase: "now"},
		{ID: 31, Status: "ready", Phase: "now"},
	}
	stubGraph(t, data, nil)
	g, err := gatherGraph(rows)
	if err != nil {
		t.Fatal(err)
	}
	var text bytes.Buffer
	renderGraph(&text, g, 200, false)
	js := g.toJSON()
	if got := strings.Split(strings.TrimRight(text.String(), "\n"), "\n"); !reflect.DeepEqual(got, js.Lines) {
		t.Fatalf("text %q != json lines %q", got, js.Lines)
	}
	assertLines(t, g, "E-21 => E-20", "E-30 -> E-31")
	var blocker jsonGraphNode
	for _, n := range js.Nodes {
		if n.ID == "E-21" {
			blocker = n
		}
	}
	if blocker.OnList || !blocker.InFlight {
		t.Fatalf("off-list in-flight blocker = %+v", blocker)
	}
	raw, err := json.Marshal(js)
	if err != nil {
		t.Fatal(err)
	}
	var back jsonGraph
	if err = json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Lines, []string{"E-21 => E-20", "E-30 -> E-31"}) {
		t.Errorf("json lines = %q", back.Lines)
	}
	wantEdges := []jsonGraphEdge{{"E-21", "E-20", "blocks"}, {"E-30", "E-31", "precedes"}}
	if !reflect.DeepEqual(back.Edges, wantEdges) {
		t.Errorf("json edges = %+v, want %+v", back.Edges, wantEdges)
	}
}

// The inline graph sits after the hidden footer and before the fault row.
func TestRenderFrame_GraphAfterHiddenFooter(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 100, Status: "underway", Phase: "now", IsFocal: true, Title: "focal"},
		{ID: 101, Status: "ready", Phase: "now", Title: "hidden one", Hidden: true},
		{ID: 102, Status: "ready", Phase: "now", Title: "visible"},
	}
	g := newGB(102, 103).blocks(103, 102).build()
	var b bytes.Buffer
	renderFrame(context.Background(), &b, rows, 100, hintClaimBind, 200, false, hiddenOmit, g)
	out := b.String()
	footer := strings.Index(out, hiddenFooter(1))
	graph := strings.Index(out, "E-103 => E-102")
	if footer < 0 || graph < 0 || graph < footer {
		t.Fatalf("graph must follow the hidden footer:\n%s", out)
	}
}
