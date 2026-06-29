package sessionnextcmd

import (
	"fmt"
	"io"
	"sort"
	"strconv"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// Tree connector glyphs for the "single root list" layout (decided with Mike,
// E-1684): depth-0 tasks render flush-left as roots; each blocked task nests
// under its latest blocker with box-drawing connectors.
const (
	treeBranch    = "├── " // a child that has following siblings
	treeLastChild = "└── " // the final child in a sibling group
	treePipe      = "│   " // continuation under a branch with more siblings
	treeBlank     = "    " // continuation under the last child
)

// treeNode is one task in the rendered forest. Children are the tasks that nest
// directly beneath it (its direct dependents in implementation order).
type treeNode struct {
	id       int64
	children []*treeNode
}

// renderTree writes the IDs-only implementation-order tree for the do/plan rows
// to w. Structure is derived from the blocked-by DAG (monitor.SessionNextBlockerEdges)
// unless a per-session order (monitor.SessionNextDoOrder) is present, which
// overrides it. No legend, titles, or icons — IDs only (E-1684). focal==0 or no
// do/plan rows prints a short hint.
func renderTree(w io.Writer, rows []monitor.SessionNextRow, focal int64) error {
	ids := doPlanIDs(rows)
	if focal == 0 || len(ids) == 0 {
		fmt.Fprintln(w, "  (no do/plan tasks for this window)")
		return nil
	}

	doOrder, err := monitor.SessionNextDoOrder(focal, ids)
	if err != nil {
		return err
	}
	edges, err := monitor.SessionNextBlockerEdges(ids)
	if err != nil {
		return err
	}

	roots := buildForest(ids, edges, doOrder)
	renderForest(w, roots)
	return nil
}

// doPlanIDs extracts the task ids classified as do (ready) or plan
// (unplanned/needs_plan/revisit) — the actionable backlog the tree visualizes.
// Focal, parent, in-flight, verify, and terminal rows are excluded.
func doPlanIDs(rows []monitor.SessionNextRow) []int64 {
	var ids []int64
	for _, r := range rows {
		switch classify(r) {
		case actDo, actPlan:
			ids = append(ids, r.ID)
		}
	}
	return ids
}

// buildForest computes each task's parent and returns the sorted root nodes.
//
//   - DAG mode (default): depth(t)=0 when t has no in-set open blocker, else
//     1+max(depth(blockers)); parent(t) is its max-depth blocker (tie → lowest
//     id). Depth-0 tasks are roots.
//   - Override mode (any do_order present): tasks are layered by do_order asc;
//     tasks lacking a value form a trailing layer ordered by id. A task's parent
//     is the lowest-id task of the previous non-empty layer; the first layer is
//     the roots.
//
// Equal depth / equal layer with no parent-child edge ⇒ siblings (parallelizable).
func buildForest(ids []int64, edges map[int64][]int64, doOrder map[int64]int64) []*treeNode {
	parent := map[int64]int64{} // child → parent; absent ⇒ root
	if len(doOrder) > 0 {
		assignByLayer(ids, doOrder, parent)
	} else {
		assignByDAG(ids, edges, parent)
	}
	return assemble(ids, parent)
}

// assignByDAG fills parent[] from the blocked-by DAG via memoized depth.
func assignByDAG(ids []int64, edges map[int64][]int64, parent map[int64]int64) {
	inSet := make(map[int64]bool, len(ids))
	for _, id := range ids {
		inSet[id] = true
	}

	depth := map[int64]int{}
	var resolve func(id int64, seen map[int64]bool) int
	resolve = func(id int64, seen map[int64]bool) int {
		if d, ok := depth[id]; ok {
			return d
		}
		if seen[id] { // cycle guard — treat as a root to stay terminating
			return 0
		}
		seen[id] = true
		d := 0
		for _, b := range edges[id] {
			if !inSet[b] {
				continue
			}
			if bd := resolve(b, seen); bd+1 > d {
				d = bd + 1
			}
		}
		delete(seen, id)
		depth[id] = d
		return d
	}

	for _, id := range ids {
		_ = resolve(id, map[int64]bool{})
	}

	for _, id := range ids {
		// Parent = the in-set open blocker with the greatest depth (so the task
		// renders after all its prerequisites); ties broken by lowest id.
		best := int64(0)
		bestDepth := -1
		for _, b := range edges[id] {
			if !inSet[b] {
				continue
			}
			bd := depth[b]
			if bd > bestDepth || (bd == bestDepth && b < best) {
				best, bestDepth = b, bd
			}
		}
		if bestDepth >= 0 {
			parent[id] = best
		}
	}
}

// assignByLayer fills parent[] from explicit do_order layers. Only tasks WITH a
// do_order value participate in the chain; tasks lacking one stay parentless and
// render as independent roots (they have no declared order to place them in).
func assignByLayer(ids []int64, doOrder map[int64]int64, parent map[int64]int64) {
	byLayer := map[int64][]int64{}
	for _, id := range ids {
		if key, ok := doOrder[id]; ok {
			byLayer[key] = append(byLayer[key], id)
		}
	}
	keys := make([]int64, 0, len(byLayer))
	for k := range byLayer {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })

	var prevLowest int64
	havePrev := false
	for _, k := range keys {
		layer := byLayer[k]
		sort.Slice(layer, func(i, j int) bool { return layer[i] < layer[j] })
		if havePrev {
			for _, id := range layer {
				parent[id] = prevLowest
			}
		}
		prevLowest = layer[0] // lowest id (layer is id-sorted)
		havePrev = true
	}
}

// assemble wires parent[] into a node forest, sorting roots and every sibling
// group by id for deterministic output.
func assemble(ids []int64, parent map[int64]int64) []*treeNode {
	nodes := make(map[int64]*treeNode, len(ids))
	for _, id := range ids {
		nodes[id] = &treeNode{id: id}
	}
	var roots []*treeNode
	for _, id := range ids {
		if p, ok := parent[id]; ok {
			if pn := nodes[p]; pn != nil {
				pn.children = append(pn.children, nodes[id])
				continue
			}
		}
		roots = append(roots, nodes[id])
	}
	var sortChildren func(n *treeNode)
	sortChildren = func(n *treeNode) {
		sort.Slice(n.children, func(i, j int) bool { return n.children[i].id < n.children[j].id })
		for _, c := range n.children {
			sortChildren(c)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].id < roots[j].id })
	for _, r := range roots {
		sortChildren(r)
	}
	return roots
}

// renderForest prints the "single root list" layout: each root flush-left, its
// subtree drawn with box-drawing connectors.
func renderForest(w io.Writer, roots []*treeNode) {
	for _, r := range roots {
		fmt.Fprintln(w, taskLabel(r.id))
		renderChildren(w, r.children, "")
	}
}

func renderChildren(w io.Writer, children []*treeNode, prefix string) {
	for i, c := range children {
		last := i == len(children)-1
		connector, cont := treeBranch, treePipe
		if last {
			connector, cont = treeLastChild, treeBlank
		}
		fmt.Fprintln(w, prefix+connector+taskLabel(c.id))
		renderChildren(w, c.children, prefix+cont)
	}
}

func taskLabel(id int64) string { return "E-" + strconv.FormatInt(id, 10) }
