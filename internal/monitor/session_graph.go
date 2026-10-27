package monitor

import (
	"fmt"
	"sort"
)

// E-2164 — the data behind `session status`'s ordering graph: which tasks block
// or should precede which, and which were declared unsafe to run together.
//
// The renderer (internal/sessionstatuscmd/graph.go) decides which rows seed the
// graph and how it is drawn; this file only answers the database's half, so the
// layout stays testable without a database.

// Stored task_deps types the ordering graph reads. `blocks` is the hard
// constraint, `precedes` the order a start refuses to break
// without --out-of-order, `conflicts_with` the declared
// mutual exclusion — stored once and read from either end.
const (
	DepBlocks        = "blocks"
	DepPrecedes      = "precedes"
	DepConflictsWith = "conflicts_with"
)

// GraphNode is one task in the ordering graph's node set.
//
// InFlight is true when ANY live session has the task as its active task —
// including the viewing session's own. The row query's in_flight excludes the
// focal because a row is never "in flight" relative to itself; the graph asks a
// different question — is someone already handling this blocker? — and for the
// viewer's own task the answer is yes.
type GraphNode struct {
	ID        int64
	ProjectID int64
	Status    string
	Phase     string
	InFlight  bool
}

// GraphEdge is one stored relation between two graph nodes, in storage order:
// From blocks / precedes / conflicts_with To.
type GraphEdge struct {
	From int64
	To   int64
	Type string
}

// GraphData is the node set and the edges among it.
type GraphData struct {
	Nodes map[int64]GraphNode
	Edges []GraphEdge
}

// SessionGraphData returns the ordering graph's node set grown from seeds, and
// every blocks / precedes / conflicts_with edge between two of its nodes.
//
// The node set is seeds, plus every OPEN task that own (the session's own
// task, or 0 for none) directly blocks or should precede, plus every OPEN task
// that blocks or should precede any of those, walked transitively (E-2345,
// E-2455). A downstream task is what finishing the own task releases; one step
// only, so its own downstream tasks are not added, but its predecessors are,
// since they decide when it can start. A predecessor is the task you most need
// to see, and one hidden behind a `later` phase, another session's claim or
// another project is still the reason its dependent is not available. Blocks
// and precedes predecessors are walked by the same rule — spawn refuses a task
// while either is open — and differ only in the arrow drawn, since a soft
// order can be overridden with --out-of-order. Terminal predecessors are not
// walked — they impose no order, exactly as the row query's upchain treats them.
//
// Seeds are taken at any status: the caller alone decides the starting set, and
// it keeps the own task in it at a terminal status until the task lands.
// The walk's UNION never adds a task twice, so a cycle ends it.
func SessionGraphData(seeds []int64, own int64) (GraphData, error) {
	out := GraphData{Nodes: map[int64]GraphNode{}}
	if len(seeds) == 0 {
		return out, nil
	}
	db, err := DB()
	if err != nil {
		return out, err
	}
	// The live-session flag reads session_liveness (E-2300).
	if err = livenessReady(); err != nil {
		return out, err
	}

	ph, args := intPlaceholders(seeds)
	args = append(args, own)
	nodeQ := `
WITH RECURSIVE nodes(id) AS (
  SELECT id FROM live_tasks WHERE id IN (` + ph + `)
  UNION
  SELECT d.target_id
    FROM task_deps d
    JOIN live_tasks dn ON dn.id = d.target_id
   WHERE d.source_type = 'task' AND d.target_type = 'task'
     AND d.dep_type IN ('` + DepBlocks + `', '` + DepPrecedes + `')
     AND d.source_id = ? AND d.target_id <> d.source_id
     AND dn.status NOT IN (` + terminalStatusSet + `)
  UNION
  SELECT d.source_id
    FROM task_deps d
    JOIN nodes n ON d.target_id = n.id
    JOIN live_tasks blk ON blk.id = d.source_id
   WHERE d.source_type = 'task' AND d.target_type = 'task'
     AND d.dep_type IN ('` + DepBlocks + `', '` + DepPrecedes + `')
     AND blk.status NOT IN (` + terminalStatusSet + `)
)
SELECT t.id, t.project_id, t.status, t.phase,
       EXISTS(SELECT 1 FROM sessions s JOIN session_liveness sl ON sl.session_id = s.id
               WHERE ` + presentSession + ` AND s.task_id = t.id)
  FROM live_tasks t JOIN nodes n ON n.id = t.id`
	rows, err := db.Query(nodeQ, args...)
	if err != nil {
		return out, fmt.Errorf("graph nodes: %w", err)
	}
	for rows.Next() {
		var n GraphNode
		if err = rows.Scan(&n.ID, &n.ProjectID, &n.Status, &n.Phase, &n.InFlight); err != nil {
			rows.Close()
			return out, fmt.Errorf("graph nodes: %w", err)
		}
		out.Nodes[n.ID] = n
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, fmt.Errorf("graph nodes: %w", err)
	}
	if len(out.Nodes) == 0 {
		return out, nil
	}

	ids := make([]int64, 0, len(out.Nodes))
	for id := range out.Nodes {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	ph, args = intPlaceholders(ids)
	edgeQ := `
SELECT source_id, target_id, dep_type
  FROM task_deps
 WHERE source_type = 'task' AND target_type = 'task'
   AND dep_type IN ('` + DepBlocks + `', '` + DepPrecedes + `', '` + DepConflictsWith + `')
   AND source_id <> target_id
   AND source_id IN (` + ph + `)
   AND target_id IN (` + ph + `)
 ORDER BY source_id, target_id, dep_type`
	rows, err = db.Query(edgeQ, append(append([]any{}, args...), args...)...)
	if err != nil {
		return out, fmt.Errorf("graph edges: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e GraphEdge
		if err = rows.Scan(&e.From, &e.To, &e.Type); err != nil {
			return out, fmt.Errorf("graph edges: %w", err)
		}
		out.Edges = append(out.Edges, e)
	}
	if err = rows.Err(); err != nil {
		return out, fmt.Errorf("graph edges: %w", err)
	}
	return out, nil
}
