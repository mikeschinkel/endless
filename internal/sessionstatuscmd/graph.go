package sessionstatuscmd

// E-2164 — the ordering graph: which of this session's tasks block or should
// precede which, and which must not run at the same time, drawn as chain lines
// under the task table.
//
//	E-1992 => E-1993 => E-1994 => E-1995
//	E-1813 => E-1814 | E-1994
//	E-2142 -> E-2159
//	<> E-2142 | E-2159 | E-2164
//	E-2020 <> E-2158
//
// `=>` blocks, `->` should precede (advisory), `|` groups tasks that stand in
// the same relation to everything on their left and right on that line (it
// binds tighter than either arrow), `<>` marks two tasks that must not run
// concurrently, and a line opening with `<>` is a set of which no two may.
// Every occurrence of an id after its first, and every id already in flight,
// is dim.
//
// Everything here is pure: the database half is monitor.SessionGraphData and
// the detected-conflict half is monitor.WorktreeChangedPaths, both reached
// through package-var seams so the layout is testable without either.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// graphEdgeKind is the ordering strength of an edge.
type graphEdgeKind int

const (
	edgeBlocks graphEdgeKind = iota
	edgePrecedes
)

// op is the arrow the kind renders as. The heavier arrow carries the harder
// constraint.
func (k graphEdgeKind) op() string {
	if k == edgeBlocks {
		return "=>"
	}
	return "->"
}

// slug is the stored relation type, which is also the JSON spelling.
func (k graphEdgeKind) slug() string {
	if k == edgeBlocks {
		return monitor.DepBlocks
	}
	return monitor.DepPrecedes
}

// graphNode is one task the graph may draw.
type graphNode struct {
	ID       int64
	Status   string
	Phase    string
	InFlight bool
	// OnList is true for a task seeded from the session's own rows, false for
	// a blocker the graph pulled in because a seeded task waits on it.
	OnList bool
}

type graphEdge struct {
	From, To int64
	Kind     graphEdgeKind
}

// graphConflict is an unordered pair that must not run concurrently. A is the
// lower id. Declared and Detected say which source produced it; both may.
type graphConflict struct {
	A, B     int64
	Declared bool
	Detected bool
	// Paths are the changed paths both worktrees share (detected only).
	Paths []string
}

func (c graphConflict) source() string {
	switch {
	case c.Declared && c.Detected:
		return "both"
	case c.Declared:
		return "declared"
	default:
		return "detected"
	}
}

type lineKind int

const (
	lineChain lineKind = iota
	lineCycle
	lineSet
	linePair
)

// graphLine is one rendered line. For a chain, groups are the `|` groups in
// order and ops[i] joins groups[i] to groups[i+1]. A set line is one group, a
// pair line two single-id groups, a cycle line one group naming the tasks.
type graphLine struct {
	kind   lineKind
	groups [][]int64
	ops    []graphEdgeKind
}

// orderGraph is the laid-out graph: what `session status` draws and `--json`
// carries.
type orderGraph struct {
	nodes     map[int64]graphNode
	edges     []graphEdge
	conflicts []graphConflict
	cycles    [][]int64
	lines     []graphLine
}

func (g orderGraph) empty() bool { return len(g.lines) == 0 }

// graphSeeds picks the rows that seed the graph: the session's own backlog,
// excluding the viewing session's task, the spawning task, the parent row, any
// task another session is already on (in flight, or owned elsewhere), hidden
// rows, phase `later`, and anything finished. What remains is the set of tasks
// someone reading this view might pick up next.
func graphSeeds(rows []monitor.SessionStatusRow) []int64 {
	var ids []int64
	for _, r := range rows {
		switch {
		case r.IsFocal, r.IsFrom, r.IsParent, r.InFlight, r.OwnedElsewhere, r.Hidden:
			continue
		case r.Phase == "later", isTerminal(r.Status):
			continue
		}
		ids = append(ids, r.ID)
	}
	return ids
}

// Seams: the database and the changed-path cache, replaced in tests.
var (
	graphData            = monitor.SessionGraphData
	graphProjectRoot     = monitor.ProjectPath
	graphWorktreeChanged = monitor.WorktreeChangedPaths
)

// gatherGraph builds the ordering graph for a session's rows.
func gatherGraph(rows []monitor.SessionStatusRow) (orderGraph, error) {
	seeds := graphSeeds(rows)
	data, err := graphData(seeds)
	if err != nil {
		return orderGraph{}, err
	}
	onList := make(map[int64]bool, len(seeds))
	for _, id := range seeds {
		onList[id] = true
	}
	nodes := make(map[int64]graphNode, len(data.Nodes))
	for id, n := range data.Nodes {
		nodes[id] = graphNode{ID: id, Status: n.Status, Phase: n.Phase, InFlight: n.InFlight, OnList: onList[id]}
	}
	var edges []graphEdge
	var conflicts []graphConflict
	for _, e := range data.Edges {
		switch e.Type {
		case monitor.DepBlocks:
			edges = append(edges, graphEdge{From: e.From, To: e.To, Kind: edgeBlocks})
		case monitor.DepPrecedes:
			edges = append(edges, graphEdge{From: e.From, To: e.To, Kind: edgePrecedes})
		case monitor.DepConflictsWith:
			conflicts = append(conflicts, graphConflict{A: e.From, B: e.To, Declared: true})
		}
	}
	conflicts = append(conflicts, detectedConflicts(data.Nodes)...)
	return buildOrderGraph(nodes, edges, conflicts), nil
}

// detectedConflicts pairs up the graph's tasks whose worktrees' cached changed
// paths intersect. It reads only the cache (no git); a task with no current
// entry contributes nothing.
func detectedConflicts(nodes map[int64]monitor.GraphNode) []graphConflict {
	roots := map[int64]string{}
	paths := map[int64]map[string]bool{}
	var ids []int64
	for id, n := range nodes {
		root, ok := roots[n.ProjectID]
		if !ok {
			r, err := graphProjectRoot(n.ProjectID)
			if err != nil {
				r = ""
			}
			roots[n.ProjectID], root = r, r
		}
		if root == "" {
			continue
		}
		ps, ok := graphWorktreeChanged(root, id)
		if !ok || len(ps) == 0 {
			continue
		}
		set := make(map[string]bool, len(ps))
		for _, p := range ps {
			set[p] = true
		}
		paths[id] = set
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var out []graphConflict
	for i, a := range ids {
		for _, b := range ids[i+1:] {
			if nodes[a].ProjectID != nodes[b].ProjectID {
				continue
			}
			var shared []string
			for p := range paths[a] {
				if paths[b][p] {
					shared = append(shared, p)
				}
			}
			if len(shared) == 0 {
				continue
			}
			sort.Strings(shared)
			out = append(out, graphConflict{A: a, B: b, Detected: true, Paths: shared})
		}
	}
	return out
}

// buildOrderGraph lays out the graph. Pure and deterministic: equal input gives
// byte-identical lines.
func buildOrderGraph(nodes map[int64]graphNode, edges []graphEdge, conflicts []graphConflict) orderGraph {
	g := orderGraph{nodes: map[int64]graphNode{}}

	// One edge per ordered pair; `blocks` wins over `precedes` between the same
	// two tasks, since the harder constraint already says the softer one.
	byPair := map[[2]int64]graphEdgeKind{}
	for _, e := range edges {
		if _, ok := nodes[e.From]; !ok {
			continue
		}
		if _, ok := nodes[e.To]; !ok || e.From == e.To {
			continue
		}
		k := [2]int64{e.From, e.To}
		if old, ok := byPair[k]; !ok || e.Kind < old {
			byPair[k] = e.Kind
		}
	}
	for k, kind := range byPair {
		g.edges = append(g.edges, graphEdge{From: k[0], To: k[1], Kind: kind})
	}
	sort.Slice(g.edges, func(i, j int) bool {
		a, b := g.edges[i], g.edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	})
	ordered := func(a, b int64) bool {
		_, ab := byPair[[2]int64{a, b}]
		_, ba := byPair[[2]int64{b, a}]
		return ab || ba
	}

	// Conflicts: normalized, merged across sources, and dropped where an arrow
	// already relates the pair — the ordering line says it.
	merged := map[[2]int64]*graphConflict{}
	var keys [][2]int64
	for _, c := range conflicts {
		a, b := c.A, c.B
		if a > b {
			a, b = b, a
		}
		if a == b {
			continue
		}
		if _, ok := nodes[a]; !ok {
			continue
		}
		if _, ok := nodes[b]; !ok || ordered(a, b) {
			continue
		}
		k := [2]int64{a, b}
		m := merged[k]
		if m == nil {
			m = &graphConflict{A: a, B: b}
			merged[k] = m
			keys = append(keys, k)
		}
		m.Declared = m.Declared || c.Declared
		m.Detected = m.Detected || c.Detected
		if len(c.Paths) > 0 {
			m.Paths = c.Paths
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i][0] != keys[j][0] {
			return keys[i][0] < keys[j][0]
		}
		return keys[i][1] < keys[j][1]
	})
	for _, k := range keys {
		g.conflicts = append(g.conflicts, *merged[k])
	}

	// A task earns a place by having any edge the graph draws.
	for _, e := range g.edges {
		g.nodes[e.From], g.nodes[e.To] = nodes[e.From], nodes[e.To]
	}
	for _, c := range g.conflicts {
		g.nodes[c.A], g.nodes[c.B] = nodes[c.A], nodes[c.B]
	}

	layout := g.layoutEdges()
	g.lines = append(g.lines, chainLines(layout)...)
	for _, cyc := range g.cycles {
		g.lines = append(g.lines, graphLine{kind: lineCycle, groups: [][]int64{cyc}})
	}
	g.lines = append(g.lines, conflictLines(g.conflicts)...)
	return g
}

// layoutEdges finds the cycles (strongly connected components of more than one
// task), records them on g, and returns the edges with every intra-cycle edge
// removed, which is a DAG. A cycle is data the renderer must report, never
// something it silently truncates; its tasks still chain normally to and from
// the rest of the graph.
func (g *orderGraph) layoutEdges() []graphEdge {
	succ := map[int64][]int64{}
	var ids []int64
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, e := range g.edges {
		succ[e.From] = append(succ[e.From], e.To)
	}

	// Tarjan's SCC, iterative over sorted ids for determinism.
	index, low := map[int64]int{}, map[int64]int{}
	onStack := map[int64]bool{}
	var stack []int64
	next := 0
	comp := map[int64]int{}
	ncomp := 0
	var strong func(v int64)
	strong = func(v int64) {
		index[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range succ[v] {
			if _, seen := index[w]; !seen {
				strong(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], index[w])
			}
		}
		if low[v] == index[v] {
			var members []int64
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp[w] = ncomp
				members = append(members, w)
				if w == v {
					break
				}
			}
			if len(members) > 1 {
				sort.Slice(members, func(i, j int) bool { return members[i] < members[j] })
				g.cycles = append(g.cycles, members)
			}
			ncomp++
		}
	}
	for _, id := range ids {
		if _, seen := index[id]; !seen {
			strong(id)
		}
	}
	sort.Slice(g.cycles, func(i, j int) bool { return g.cycles[i][0] < g.cycles[j][0] })

	var dag []graphEdge
	for _, e := range g.edges {
		if comp[e.From] == comp[e.To] {
			continue
		}
		dag = append(dag, e)
	}
	return dag
}

// chainLines covers every edge of the DAG with chain lines.
//
// Order is derived, not authored: a topological order over both arrow kinds,
// ties broken by the longest chain leading on from a task, then lowest id. Each
// line starts at the first task in that order that still has an undrawn edge,
// so the first line leads with the task to start on.
//
// A line extends while the tasks at its right end share a common successor
// over an undrawn edge of one kind (`=>` preferred): the next `|` group is
// exactly those successors, so every member of every group stands in the
// line's relation to everything beside it. Where members diverge the line
// stops, and their remaining edges get lines of their own.
func chainLines(dag []graphEdge) []graphLine {
	if len(dag) == 0 {
		return nil
	}
	type key struct {
		from, to int64
		kind     graphEdgeKind
	}
	succ := map[graphEdgeKind]map[int64]map[int64]bool{edgeBlocks: {}, edgePrecedes: {}}
	out := map[int64][]int64{}
	indeg := map[int64]int{}
	nodeSet := map[int64]bool{}
	uncovered := map[key]bool{}
	for _, e := range dag {
		if succ[e.Kind][e.From] == nil {
			succ[e.Kind][e.From] = map[int64]bool{}
		}
		succ[e.Kind][e.From][e.To] = true
		out[e.From] = append(out[e.From], e.To)
		indeg[e.To]++
		nodeSet[e.From], nodeSet[e.To] = true, true
		uncovered[key{e.From, e.To, e.Kind}] = true
	}

	// Longest chain leading on from each task.
	depth := map[int64]int{}
	var longest func(v int64) int
	longest = func(v int64) int {
		if d, ok := depth[v]; ok {
			return d
		}
		d := 0
		for _, w := range out[v] {
			d = max(d, longest(w)+1)
		}
		depth[v] = d
		return d
	}
	var ready []int64
	for v := range nodeSet {
		longest(v)
		if indeg[v] == 0 {
			ready = append(ready, v)
		}
	}
	before := func(a, b int64) bool {
		if depth[a] != depth[b] {
			return depth[a] > depth[b]
		}
		return a < b
	}
	var topo []int64
	for len(ready) > 0 {
		sort.Slice(ready, func(i, j int) bool { return before(ready[i], ready[j]) })
		v := ready[0]
		ready = ready[1:]
		topo = append(topo, v)
		for _, w := range out[v] {
			indeg[w]--
			if indeg[w] == 0 {
				ready = append(ready, w)
			}
		}
	}
	pos := make(map[int64]int, len(topo))
	for i, v := range topo {
		pos[v] = i
	}

	hasUncoveredOut := func(v int64) bool {
		for _, k := range []graphEdgeKind{edgeBlocks, edgePrecedes} {
			for w := range succ[k][v] {
				if uncovered[key{v, w, k}] {
					return true
				}
			}
		}
		return false
	}
	sortIDs := func(ids []int64) { sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] }) }

	var lines []graphLine
	for {
		start := int64(0)
		found := false
		for _, v := range topo {
			if hasUncoveredOut(v) {
				start, found = v, true
				break
			}
		}
		if !found {
			break
		}
		// A line always starts from one task. Two tasks blocking the same one
		// are two chains, and are drawn as two lines with the shared task's
		// repeat dimmed, rather than folded into a leading `|` group.
		cur := []int64{start}
		line := graphLine{kind: lineChain, groups: [][]int64{cur}}
		for {
			var nextGroup []int64
			var nextKind graphEdgeKind
			for _, k := range []graphEdgeKind{edgeBlocks, edgePrecedes} {
				var cand []int64
				for w := range succ[k][cur[0]] {
					common, fresh := true, false
					for _, c := range cur {
						if !succ[k][c][w] {
							common = false
							break
						}
						if uncovered[key{c, w, k}] {
							fresh = true
						}
					}
					if common && fresh {
						cand = append(cand, w)
					}
				}
				if len(cand) > 0 {
					nextGroup, nextKind = cand, k
					break
				}
			}
			if len(nextGroup) == 0 {
				break
			}
			sortIDs(nextGroup)
			for _, c := range cur {
				for _, w := range nextGroup {
					delete(uncovered, key{c, w, nextKind})
				}
			}
			line.groups = append(line.groups, nextGroup)
			line.ops = append(line.ops, nextKind)
			cur = nextGroup
		}
		lines = append(lines, line)
	}
	return lines
}

// conflictLines renders the conflict pairs: each maximal clique of three or
// more as one set line (`<> a | b | c` — no two may run concurrently), ordered
// by size descending then lowest member id; every pair no set line covers as a
// pairwise `a <> b`. `<>` is symmetric but not transitive, so it is never
// chained: a chain-shaped conflict graph renders as separate pairs, which
// correctly look different from a set.
func conflictLines(conflicts []graphConflict) []graphLine {
	if len(conflicts) == 0 {
		return nil
	}
	adj := map[int64]map[int64]bool{}
	var ids []int64
	add := func(a, b int64) {
		if adj[a] == nil {
			adj[a] = map[int64]bool{}
			ids = append(ids, a)
		}
		adj[a][b] = true
	}
	for _, c := range conflicts {
		add(c.A, c.B)
		add(c.B, c.A)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	// Bron–Kerbosch with pivoting over sorted sets, so the output is stable.
	var cliques [][]int64
	var bk func(r, p, x []int64)
	bk = func(r, p, x []int64) {
		if len(p) == 0 && len(x) == 0 {
			if len(r) >= 3 {
				c := append([]int64(nil), r...)
				sort.Slice(c, func(i, j int) bool { return c[i] < c[j] })
				cliques = append(cliques, c)
			}
			return
		}
		pivot := int64(0)
		best := -1
		for _, u := range append(append([]int64(nil), p...), x...) {
			n := 0
			for _, v := range p {
				if adj[u][v] {
					n++
				}
			}
			if n > best {
				pivot, best = u, n
			}
		}
		for _, v := range append([]int64(nil), p...) {
			if adj[pivot][v] {
				continue
			}
			var np, nx []int64
			for _, w := range p {
				if adj[v][w] {
					np = append(np, w)
				}
			}
			for _, w := range x {
				if adj[v][w] {
					nx = append(nx, w)
				}
			}
			bk(append(append([]int64(nil), r...), v), np, nx)
			for i, w := range p {
				if w == v {
					p = append(append([]int64(nil), p[:i]...), p[i+1:]...)
					break
				}
			}
			x = append(x, v)
		}
	}
	bk(nil, ids, nil)
	sort.Slice(cliques, func(i, j int) bool {
		a, b := cliques[i], cliques[j]
		if len(a) != len(b) {
			return len(a) > len(b)
		}
		for k := range a {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		return false
	})

	var lines []graphLine
	covered := map[[2]int64]bool{}
	for _, c := range cliques {
		lines = append(lines, graphLine{kind: lineSet, groups: [][]int64{c}})
		for i, a := range c {
			for _, b := range c[i+1:] {
				covered[[2]int64{a, b}] = true
			}
		}
	}
	for _, c := range conflicts {
		if covered[[2]int64{c.A, c.B}] {
			continue
		}
		lines = append(lines, graphLine{kind: linePair, groups: [][]int64{{c.A}, {c.B}}})
	}
	return lines
}

// graphStyle styles one piece of a line: ids bright unless dim, punctuation
// always dim.
type graphStyle struct {
	color bool
}

func (s graphStyle) punct(t string) string { return dim(t, s.color) }

func (s graphStyle) id(t string, dimmed bool) string {
	if dimmed {
		return dim(t, s.color)
	}
	if s.color {
		return ansiBold + t + ansiReset
	}
	return t
}

// segment is one unsplittable piece of a rendered line: its plain text (for
// width and for --json) and its styled text.
type segment struct {
	plain, styled string
}

// lineSegments renders each line into segments. A chain yields one segment per
// `|` group, each after the first led by its arrow; any other line is a single
// segment, because a set or pair is never split. seen tracks ids already drawn,
// so every later occurrence dims.
func (g orderGraph) lineSegments(color bool) [][]segment {
	st := graphStyle{color: color}
	seen := map[int64]bool{}
	idText := func(id int64) (string, string) {
		label := taskLabel(id)
		dimmed := seen[id] || g.nodes[id].InFlight
		seen[id] = true
		return label, st.id(label, dimmed)
	}
	group := func(ids []int64, sep string) segment {
		var p, s strings.Builder
		for i, id := range ids {
			if i > 0 {
				p.WriteString(sep)
				s.WriteString(st.punct(sep))
			}
			lp, ls := idText(id)
			p.WriteString(lp)
			s.WriteString(ls)
		}
		return segment{p.String(), s.String()}
	}

	var out [][]segment
	for _, ln := range g.lines {
		switch ln.kind {
		case lineChain:
			var segs []segment
			for i, grp := range ln.groups {
				sg := group(grp, " | ")
				if i > 0 {
					op := " " + ln.ops[i-1].op() + " "
					sg = segment{op + sg.plain, st.punct(op) + sg.styled}
				}
				segs = append(segs, sg)
			}
			out = append(out, segs)
		case lineCycle:
			sg := group(ln.groups[0], ", ")
			out = append(out, []segment{{"cycle: " + sg.plain, st.punct("cycle: ") + sg.styled}})
		case lineSet:
			sg := group(ln.groups[0], " | ")
			out = append(out, []segment{{"<> " + sg.plain, st.punct("<> ") + sg.styled}})
		case linePair:
			a := group(ln.groups[0], "")
			b := group(ln.groups[1], "")
			out = append(out, []segment{{a.plain + " <> " + b.plain, a.styled + st.punct(" <> ") + b.styled}})
		}
	}
	return out
}

// plainLines is the graph as unwrapped, unstyled lines — what --json carries,
// and what the text rendering is at any width wide enough not to wrap.
func (g orderGraph) plainLines() []string {
	var out []string
	for _, segs := range g.lineSegments(false) {
		var b strings.Builder
		for _, s := range segs {
			b.WriteString(s.plain)
		}
		out = append(out, b.String())
	}
	return out
}

// renderGraph writes the graph to w, wrapping each chain to cols at an arrow
// boundary with the continuation indented so its arrow sits just past the
// line's first group. A `|` group, a set line and a pair are never split. An
// empty graph writes nothing at all — no header, no blank line.
func renderGraph(w io.Writer, g orderGraph, cols int, color bool) {
	for _, segs := range g.lineSegments(color) {
		indent := strings.Repeat(" ", runewidth.StringWidth(segs[0].plain))
		var line strings.Builder
		width := 0
		for i, s := range segs {
			sw := runewidth.StringWidth(s.plain)
			if i > 0 && width+sw > cols {
				fmt.Fprintln(w, line.String())
				line.Reset()
				line.WriteString(indent)
				width = len(indent)
			}
			line.WriteString(s.styled)
			width += sw
		}
		fmt.Fprintln(w, line.String())
	}
}

// jsonGraph is the graph as data: the nodes it draws, the ordering edges, the
// conflicts with their source, any cycles, and the lines exactly as the text
// draws them unwrapped — so an agent reads structure rather than parsing ASCII.
type jsonGraph struct {
	Nodes     []jsonGraphNode     `json:"nodes"`
	Edges     []jsonGraphEdge     `json:"edges"`
	Conflicts []jsonGraphConflict `json:"conflicts"`
	Cycles    [][]string          `json:"cycles"`
	Lines     []string            `json:"lines"`
}

type jsonGraphNode struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Phase    string `json:"phase"`
	InFlight bool   `json:"in_flight"`
	// OnList is false for a blocker pulled in from outside the session's rows.
	OnList bool `json:"on_list"`
}

type jsonGraphEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

type jsonGraphConflict struct {
	Tasks  []string `json:"tasks"`
	Source string   `json:"source"`
	Paths  []string `json:"paths"`
}

func (g orderGraph) toJSON() jsonGraph {
	out := jsonGraph{
		Nodes:     []jsonGraphNode{},
		Edges:     []jsonGraphEdge{},
		Conflicts: []jsonGraphConflict{},
		Cycles:    [][]string{},
		Lines:     g.plainLines(),
	}
	if out.Lines == nil {
		out.Lines = []string{}
	}
	var ids []int64
	for id := range g.nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		n := g.nodes[id]
		out.Nodes = append(out.Nodes, jsonGraphNode{
			ID: taskLabel(id), Status: n.Status, Phase: n.Phase, InFlight: n.InFlight, OnList: n.OnList,
		})
	}
	for _, e := range g.edges {
		out.Edges = append(out.Edges, jsonGraphEdge{From: taskLabel(e.From), To: taskLabel(e.To), Type: e.Kind.slug()})
	}
	for _, c := range g.conflicts {
		paths := c.Paths
		if paths == nil {
			paths = []string{}
		}
		out.Conflicts = append(out.Conflicts, jsonGraphConflict{
			Tasks: []string{taskLabel(c.A), taskLabel(c.B)}, Source: c.source(), Paths: paths,
		})
	}
	for _, cyc := range g.cycles {
		var s []string
		for _, id := range cyc {
			s = append(s, taskLabel(id))
		}
		out.Cycles = append(out.Cycles, s)
	}
	return out
}
