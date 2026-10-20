package sessionstatuscmd

// Display tiering by session-task relation (E-1696, implementing E-1462's
// Extension). `session status` is a dashboard of tasks related to this session's
// work, and not every relation earns the same prominence:
//
//	claimed / bound /           decided work — this session's business
//	  spawned / touched
//	filed / revisited           incidental work — edited, but not the point
//	referenced             read-only relevance — looked at, nothing more
//
// Two mechanisms, deliberately different in strength:
//
//  1. `referenced` rows SINK below everything, whatever their status, and render
//     dimmed. This is flood control, and it is the reason the tier exists: a
//     session reads many tasks (this feature's own implementing session read
//     nine before writing a line of code) and unranked reads would push real
//     work off the top of the pane. A hard sink is the only thing that holds
//     under that volume.
//
//  2. Everything else keeps today's ordering, with relation used ONLY to break
//     ties between EQUALLY actionable rows. Relation deliberately does not
//     outrank the action classification: a `touched` task parked in `later` must
//     not jump above the ⟳ row you are actually working, and promoting it would
//     make `touch` a way to derange the view rather than to fill it.
//
// The read gate that produces `referenced` at volume ships separately (it needs
// the gitignored machine-user ledger E-1673 routes to), so today the tier holds
// only rows a test or a future capture puts there. It lands now so the display
// is ready before the flood, not after it.

import (
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
)

// Relation glyphs, both single-width (asserted in TestRelationGlyphWidths) so
// the conditional column never breaks the fixed 13-col row prefix.
//
// Only the relations that need explaining get a mark. claimed, bound, filed
// and revisited are the ordinary majority (or the anchor row itself) and are already legible from the action
// glyph and the row's position; marking them would put a column on nearly every
// frame to say nothing.
const (
	// touchedGlyph marks a row put here on purpose by `touch` (or its alias
	// `session task add`) — ⊕ (U+2295 CIRCLED PLUS), "added". Same Mathematical Operators block as the
	// existing ⊘ hidden and ⊗ blocked marks, so it sits with them visually.
	touchedGlyph = "⊕"
	// spawnedGlyph marks a task this session handed to a session it spawned
	// (E-2405) — ↗ (U+2197 NORTH EAST ARROW), "sent off". An arrow, like the ↑
	// parent and ↩ from rows, because spawning is session lineage too: ↩ is the
	// spawner seen from the spawned session, ↗ the spawned task seen from the
	// spawner. It needs a mark because the spawner is NOT working the row — an
	// unmarked spawned task would read as this session's own open work.
	spawnedGlyph = "↗"
	// referencedGlyph marks read-only relevance — · (U+00B7 MIDDLE DOT). As quiet
	// as a visible mark gets, which is the point: it says "this is here because
	// you looked at it" without competing with work.
	referencedGlyph = "·"
)

// isReferenced reports whether the row is in the sunk-and-dimmed tier.
func isReferenced(r monitor.SessionStatusRow) bool {
	return r.Relation == sessiontaskrelation.RelationReferenced
}

// prominence ranks a row for the equal-action tiebreak: 0 for decided work
// (claimed, bound, spawned, touched), 1 for everything else. Spawned is decided
// work (E-2405): the session chose to start a session on it, which is a firmer
// decision than touching it, and the row stays in view until the task settles
// precisely so the spawner can follow what it delegated.
//
// "Everything else" deliberately includes rows with NO relation at all — the
// read-time children, dependents and upstream blockers, which have no
// session_tasks row by design. They rank alongside filed/revisited rather
// than below them, because they are real work on the focal task and only their
// bookkeeping differs. Ranking them last would reorder the view for a reason
// that has nothing to do with what a viewer should look at next.
//
// referenced never reaches this function: it is sunk by the primary sort key.
func prominence(r monitor.SessionStatusRow) int {
	switch r.Relation {
	case sessiontaskrelation.RelationClaimed, sessiontaskrelation.RelationBound,
		sessiontaskrelation.RelationSpawned, sessiontaskrelation.RelationTouched:
		return 0
	default:
		return 1
	}
}

// relationField renders the conditional relation column, on the same
// width-on-demand rule as the hidden and block columns: the slot exists only
// when a RENDERED row wears a mark, so a frame with no touched, spawned or
// referenced rows is byte-identical to before E-1696.
func relationField(r monitor.SessionStatusRow, rw int) string {
	if rw == 0 {
		return ""
	}
	switch r.Relation {
	case sessiontaskrelation.RelationTouched:
		return touchedGlyph + " "
	case sessiontaskrelation.RelationSpawned:
		return spawnedGlyph + " "
	case sessiontaskrelation.RelationReferenced:
		return referencedGlyph + " "
	default:
		return "  "
	}
}

// relationColWidth returns 2 when any row bears a relation mark, else 0.
func relationColWidth(rows []monitor.SessionStatusRow) int {
	for _, r := range rows {
		switch r.Relation {
		case sessiontaskrelation.RelationTouched, sessiontaskrelation.RelationSpawned,
			sessiontaskrelation.RelationReferenced:
			return 2
		}
	}
	return 0
}
