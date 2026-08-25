package monitor

// Per-session task relation (E-1696). How a task entered ONE session's scope —
// claimed, queued, surfaced, revisited or referenced — read off that session's own
// session_tasks row and layered onto the viewer-agnostic row set, exactly as
// E-1914's hides are.
//
// Annotation rather than a query column, for a reason specific to this view: the
// focal path unions rows from EVERY session whose task_id is the focal
// task. A relation selected in that query would be whichever session's row the
// join happened to reach, reported to you as your own classification. Reading it
// for one named viewer is the only way the answer is well-defined.

import (
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// nonClaimedRelationIDs is the SQL id list for every relation EXCEPT claimed, built
// from the Go enum so a newly added relation is included automatically. Used by
// SessionStatusRowsForSession, the no-goal view: claimed rows are excluded there
// because a session that claimed a task resolves through the focal path instead.
//
// Derived rather than written as a literal because the previous literal — the
// `IN (2, 3)` this replaced — silently meant "surfaced and revisited ONLY", so
// adding `queued` would have left `session task add` promoting tasks into a view
// that refused to show them.
var nonClaimedRelationIDs = buildNonClaimedRelationIDs()

func buildNonClaimedRelationIDs() string {
	var ids []string
	for _, rel := range sessiontaskrelation.All() {
		if rel == sessiontaskrelation.RelationClaimed {
			continue
		}
		ids = append(ids, strconv.Itoa(int(rel)))
	}
	return strings.Join(ids, ", ")
}

// SessionTaskRelations returns the relations session `sessionID` has recorded,
// as task_id → Relation. Empty (never nil) for session 0 or a session with no
// rows, so callers can index it unconditionally.
//
// A row whose relation_id is NULL (pre-E-1462 history) is omitted rather than
// mapped to zero: absent and zero already mean the same thing to every caller,
// and omitting keeps the map to rows that actually classify something.
func SessionTaskRelations(sessionID int64) (map[int64]sessiontaskrelation.Relation, error) {
	rels := make(map[int64]sessiontaskrelation.Relation)
	if sessionID == 0 {
		return rels, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(
		`SELECT task_id, relation_id FROM session_tasks
		  WHERE session_id = ? AND relation_id IS NOT NULL`,
		sessionID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var taskID, relID int64
		if err := rows.Scan(&taskID, &relID); err != nil {
			return nil, err
		}
		rels[taskID] = sessiontaskrelation.Relation(relID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rels, nil
}

// AnnotateSessionStatusRelation fills each row's Relation from the VIEWING
// session's session_tasks rows, in place. Mirrors AnnotateSessionStatusHidden.
//
// viewer == 0 (no session resolved — e.g. outside tmux) leaves every row at zero
// relation, which sorts them all into the unclassified tier. That is the right
// failure mode: a listing that cannot identify its viewer must not claim rows
// entered ITS scope some particular way, and a uniform relation degrades the
// tier ordering to the pre-E-1696 ordering rather than to a wrong one.
func AnnotateSessionStatusRelation(rows []SessionStatusRow, viewer int64) error {
	if len(rows) == 0 || viewer == 0 {
		return nil
	}
	rels, err := SessionTaskRelations(viewer)
	if err != nil {
		return err
	}
	for i := range rows {
		if rel, ok := rels[rows[i].ID]; ok {
			rows[i].Relation = rel
		}
	}
	return nil
}
