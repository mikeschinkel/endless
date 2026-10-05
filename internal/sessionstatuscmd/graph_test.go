package sessionstatuscmd

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestMain defaults the graph's database seam to an empty graph, so every
// table test that stubs the row source stays database-free. Graph tests
// install their own data with stubGraph.
func TestMain(m *testing.M) {
	graphData = func([]int64) (monitor.GraphData, error) {
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

// Every occurrence of an id after its first is dim, and so is an in-flight id.
func TestGraph_RepeatsAndInFlightRenderDim(t *testing.T) {
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
	if !strings.HasPrefix(lines[0], dim("E-4", true)) {
		t.Errorf("in-flight E-4 not dim: %q", lines[0])
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

// gatherGraph's seeds: the session's actionable backlog only; blockers join
// through the database seam.
func TestGraphSeeds_Exclusions(t *testing.T) {
	rows := []monitor.SessionStatusRow{
		{ID: 1, Status: "underway", Phase: "now", IsFocal: true},
		{ID: 2, Status: "ready", Phase: "now", IsFrom: true},
		{ID: 3, Status: "ready", Phase: "now", IsParent: true},
		{ID: 4, Status: "underway", Phase: "now", InFlight: true},
		{ID: 5, Status: "ready", Phase: "now", Hidden: true},
		{ID: 6, Status: "ready", Phase: "later"},
		{ID: 7, Status: "confirmed", Phase: "now"},
		{ID: 8, Status: "ready", Phase: "now", StewardedElsewhere: true},
		{ID: 9, Status: "ready", Phase: "now"},
		{ID: 10, Status: "unplanned", Phase: "next"},
	}
	if got := graphSeeds(rows); !reflect.DeepEqual(got, []int64{9, 10}) {
		t.Fatalf("seeds = %v, want [9 10]", got)
	}
}

// stubGraph installs database and cache answers for one test.
func stubGraph(t *testing.T, data monitor.GraphData, changed map[int64][]string) {
	t.Helper()
	prevData, prevRoot, prevChanged := graphData, graphProjectRoot, graphWorktreeChanged
	t.Cleanup(func() { graphData, graphProjectRoot, graphWorktreeChanged = prevData, prevRoot, prevChanged })
	graphData = func([]int64) (monitor.GraphData, error) { return data, nil }
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

// An off-list blocker renders; an in-flight one renders dim; --json carries
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
	renderFrame(&b, rows, 100, hintClaimBind, 200, false, hiddenOmit, g)
	out := b.String()
	footer := strings.Index(out, hiddenFooter(1))
	graph := strings.Index(out, "E-103 => E-102")
	if footer < 0 || graph < 0 || graph < footer {
		t.Fatalf("graph must follow the hidden footer:\n%s", out)
	}
}
