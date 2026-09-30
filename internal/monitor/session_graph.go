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
// constraint, `precedes` the advisory order, `conflicts_with` the declared
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
// The node set is seeds plus every OPEN task that blocks one of them, walked
// transitively: a blocker is the task you most need to see, and one hidden
// behind a `later` phase or another session's claim is still the reason its
// dependent is not available (E-2164's plan: "then add every task that blocks
// one of the survivors"). Terminal blockers are not walked — they impose no
// order, exactly as the row query's upchain treats them.
//
// Terminal SEEDS are dropped for the same reason: a finished task has nothing
// left to order. The caller normally never passes one; --all can.
func SessionGraphData(seeds []int64) (GraphData, error) {
	out := GraphData{Nodes: map[int64]GraphNode{}}
	if len(seeds) == 0 {
		return out, nil
	}
	db, err := DB()
	if err != nil {
		return out, err
	}

	ph, args := intPlaceholders(seeds)
	nodeQ := `
WITH RECURSIVE nodes(id) AS (
  SELECT id FROM live_tasks
   WHERE id IN (` + ph + `) AND status NOT IN (` + terminalStatusSet + `)
  UNION
  SELECT d.source_id
    FROM task_deps d
    JOIN nodes n ON d.target_id = n.id
    JOIN live_tasks blk ON blk.id = d.source_id
   WHERE d.source_type = 'task' AND d.target_type = 'task'
     AND d.dep_type = 'blocks'
     AND blk.status NOT IN (` + terminalStatusSet + `)
)
SELECT t.id, t.project_id, t.status, t.phase,
       EXISTS(SELECT 1 FROM sessions s
               WHERE s.state IN (` + liveSessionStates + `) AND s.task_id = t.id)
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
