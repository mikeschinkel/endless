// Package sessiontaskrelation defines the Relation Go enum, the source of truth
// for session_tasks.relation_id (per ED-1506: const-in-code is the source of
// truth, the session_task_relations SQL table mirrors it). The package lives
// outside internal/events and internal/monitor so both can depend on it without
// a cycle.
//
// Relation classifies HOW a task entered a session's scope, captured by the
// task-mutation executors (E-1462), by the session-task verbs (E-1696) and by
// the ownership paths (E-2291):
//   - claimed:    the session's claimed task (task.claimed)
//   - bound:      the session owns the task but has not claimed it — `task bind`
//     (task.bound) or the SessionStart cwd auto-bind of a pre-work task. The
//     step before a claim: the session is working out the plan with its user.
//   - touched:    explicitly put in the session's scope (`touch`, and its alias
//     `session task add`)
//   - spawned:    a task this session handed to a new session it spawned
//     (session_tasks.spawned, E-2405). Recorded on the SPAWNING session so
//     its `session status` keeps the delegated task in view until it settles,
//     even when the task lives in another project.
//   - filed:      created during the session (task.created / task.imported)
//   - revisited:  a pre-existing task the session edited but did not claim
//   - referenced: read-only relevance — the session looked at the task without
//     editing it. Reserved by E-1696 for the auto-capture read gate; no emitter
//     produces it yet (that gate needs the gitignored machine-user ledger E-1673
//     routes to, so it ships separately). Defined here now so the precedence
//     ladder and the display tier land against a stable enum.
//
// RENAMING a value = edit the slug/label here and in the schema.sql seed, which
// upserts the change onto existing rows on connect (the E-1659 pattern). Ids are
// persisted, slugs are not, so a rename is a label change and never a data
// migration. E-1967 renamed id 1 from `goal`/`Goal` to `claimed`/`Claimed`:
// "Goal:" cannot be reasoned about, "Claimed:" says what happened. E-2291
// renamed id 2 from `surfaced` to `filed` (it says what the session did) and id
// 5 from `queued` to `touched` (the verb that writes it).
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it + give it a Rank. The VerifyIntegrity startup check fails closed
// on drift.
package sessiontaskrelation

import (
	"database/sql"
	"fmt"
	"strings"
)

// Relation is the closed enumeration of session-task scope-entry relations.
type Relation int

const (
	RelationClaimed   Relation = 1
	RelationFiled     Relation = 2
	RelationRevisited Relation = 3
	// RelationReferenced, RelationTouched and RelationBound are APPENDED
	// (E-1696, E-2291), never renumbered: the ids are persisted in
	// session_tasks.relation_id, so inserting a value in the middle would
	// silently reclassify live rows. Display order and precedence are carried
	// by Rank(), not by id.
	RelationReferenced Relation = 4
	RelationTouched    Relation = 5
	RelationBound      Relation = 6
	// RelationSpawned is APPENDED by E-2405 under the same never-renumber rule.
	RelationSpawned Relation = 7
)

// String returns the lowercase machine slug (matches session_task_relations.slug).
func (r Relation) String() string {
	switch r {
	case RelationClaimed:
		return "claimed"
	case RelationFiled:
		return "filed"
	case RelationRevisited:
		return "revisited"
	case RelationReferenced:
		return "referenced"
	case RelationTouched:
		return "touched"
	case RelationBound:
		return "bound"
	case RelationSpawned:
		return "spawned"
	default:
		return fmt.Sprintf("Relation(%d)", int(r))
	}
}

// Label returns the human display string (matches session_task_relations.label).
func (r Relation) Label() string {
	switch r {
	case RelationClaimed:
		return "Claimed"
	case RelationFiled:
		return "Filed"
	case RelationRevisited:
		return "Revisited"
	case RelationReferenced:
		return "Referenced"
	case RelationTouched:
		return "Touched"
	case RelationBound:
		return "Bound"
	case RelationSpawned:
		return "Spawned"
	default:
		return ""
	}
}

// Parse converts a slug from CLI / external input to a Relation. Returns an
// error for unknown slugs.
func Parse(s string) (Relation, error) {
	switch s {
	case "claimed":
		return RelationClaimed, nil
	case "filed":
		return RelationFiled, nil
	case "revisited":
		return RelationRevisited, nil
	case "referenced":
		return RelationReferenced, nil
	case "touched":
		return RelationTouched, nil
	case "bound":
		return RelationBound, nil
	case "spawned":
		return RelationSpawned, nil
	default:
		return 0, fmt.Errorf(
			"sessiontaskrelation: invalid relation %q "+
				"(valid: claimed, bound, spawned, touched, filed, revisited, referenced)", s,
		)
	}
}

// Validate returns an error if s is not a recognized slug.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}

// All returns the canonical set in id order. Used by VerifyIntegrity and by
// callers that need to enumerate the enum.
func All() []Relation {
	return []Relation{
		RelationClaimed, RelationFiled, RelationRevisited,
		RelationReferenced, RelationTouched, RelationBound, RelationSpawned,
	}
}

// Anchors returns the relations that make a task the session's OWN task — the
// one sessions.task_id names — rather than one more row in its scope: claimed,
// and bound, its pre-work counterpart (E-2291). One named set so every reader
// that separates "the session's task" from "the session's other tasks" draws
// the line in the same place.
func Anchors() []Relation {
	return []Relation{RelationClaimed, RelationBound}
}

// IsAnchor reports whether r is one of Anchors().
func (r Relation) IsAnchor() bool {
	for _, a := range Anchors() {
		if r == a {
			return true
		}
	}
	return false
}

// Rank orders the relations from strongest claim on the session to weakest:
// claimed < bound < spawned < touched < filed < revisited < referenced. LOWER
// rank =
// STRONGER.
//
// One ladder serves two jobs, deliberately, because they are the same judgment:
//
//  1. CAPTURE PRECEDENCE (E-1696). upsertSessionTask upgrades a row's relation
//     when an incoming capture outranks the stored one, and never downgrades.
//     This replaced E-1462's set-once rule, which was correct only while
//     `referenced` did not exist: the documented happy path is `task show <id>`
//     THEN `task claim <id>`, so under set-once the read gate would stamp every
//     session's own claimed task `referenced` forever. Set-once also had the
//     inverse bug already — claim-then-edit was fine, but create-then-claim
//     left a session's claimed task reading `filed`. `bound` sits directly
//     below `claimed` (E-2291) so bind-then-claim upgrades to `claimed`, and
//     binding a touched task upgrades it to `bound`.
//
//  2. DISPLAY TIER (E-1462's Extension). `session status` ranks equally
//     actionable rows by this order, so decided work
//     (claimed/bound/spawned/touched)
//     sits above incidental work (filed/revisited) and read-only relevance
//     (`referenced`) sinks to the bottom.
//
// E-2405 slots `spawned` directly below `bound`: spawning a task is a firmer
// decision than touching it (the session did not just put it on the board, it
// started a session to work it), so a task the session filed, edited or touched
// and then spawned reads Spawned. It stays below `claimed` and `bound` because a
// session that owns a task is working it itself, which is the stronger claim.
//
// An unknown value ranks last, so a relation from a newer binary degrades to
// "least prominent, never upgrades over anything" rather than silently
// outranking real work.
func (r Relation) Rank() int {
	switch r {
	case RelationClaimed:
		return 0
	case RelationBound:
		return 1
	case RelationSpawned:
		return 2
	case RelationTouched:
		return 3
	case RelationFiled:
		return 4
	case RelationRevisited:
		return 5
	case RelationReferenced:
		return 6
	default:
		return 7
	}
}

// Outranks reports whether r is a strictly stronger claim than other — the
// upgrade test for upsertSessionTask. Equal relations do not outrank each other,
// so a repeat capture of the same kind is a no-op on relation_id.
func (r Relation) Outranks(other Relation) bool {
	return r.Rank() < other.Rank()
}

// VerifyIntegrity asserts that the session_task_relations SQL table matches the
// Go enum. Runs once at startup (from monitor.DB() after schema.SQL applies).
// Returns an error on any drift: an enum constant with no matching row, a slug
// or label mismatch, or a session_task_relations row whose id does not match any
// constant. Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id    int
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM session_task_relations")
	if err != nil {
		return fmt.Errorf("sessiontaskrelation: query session_task_relations: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("sessiontaskrelation: scan session_task_relations row: %w", err)
		}
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sessiontaskrelation: iterate session_task_relations: %w", err)
	}

	for _, rel := range All() {
		r, ok := byID[int(rel)]
		if !ok {
			return fmt.Errorf(
				"sessiontaskrelation: enum constant %s (id=%d) missing from session_task_relations table",
				rel.String(), int(rel))
		}
		if r.slug != rel.String() {
			return fmt.Errorf("sessiontaskrelation: id=%d slug mismatch: enum=%q, table=%q",
				int(rel), rel.String(), r.slug)
		}
		if r.label != rel.Label() {
			return fmt.Errorf("sessiontaskrelation: id=%d label mismatch: enum=%q, table=%q",
				int(rel), rel.Label(), r.label)
		}
		delete(byID, int(rel))
	}

	for id, r := range byID {
		return fmt.Errorf(
			"sessiontaskrelation: session_task_relations row id=%d slug=%q has no matching enum constant",
			id, r.slug)
	}

	return nil
}

// MovesFocus reports whether a capture of this relation repoints the session's
// focused task (sessions.focus_task_id, E-2188): claimed, bound, filed and
// revisited do — the session owns the task or wrote to it, so its conversation
// is on it now. touched, spawned and referenced do not: adding a task to the
// board, handing it to a spawned session (E-2405 — the spawned session works
// it, not the spawner) or merely reading a neighbour must not steal focus from
// the task being worked.
//
// A per-capture judgment, independent of Rank: focus follows the LATEST write,
// while the stored relation keeps the STRONGEST one.
func (r Relation) MovesFocus() bool {
	switch r {
	case RelationClaimed, RelationBound, RelationFiled, RelationRevisited:
		return true
	default:
		return false
	}
}

// Execer is the one method Record needs — satisfied by *sql.DB, *sql.Tx and
// the events package's in-flight transaction alike.
type Execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Record is the single write path for session_tasks (E-2291 moved it here from
// internal/events so the SessionStart auto-bind in internal/monitor, which
// cannot import events, writes through the same ladder as every executor). It
// inserts the (session, task) row or bumps updated_at on conflict, UPGRADING
// relation_id when rel outranks the stored relation and never downgrading it,
// then repoints the session's focus when rel MovesFocus.
//
// now is the caller's timestamp ("2006-01-02T15:04:05", UTC), so a row written
// inside an event carries the same instant as the rest of that event's writes.
//
// The comparison runs inside the upsert (see RankCaseSQL) rather than as a
// read-then-write, so two concurrent captures cannot interleave into a
// downgrade. NULL is reserved for pre-E-1462 historical rows and ranks below
// every real relation, so the first capture after it fills it in.
func Record(ex Execer, sessionID, taskID int64, rel Relation, now string) error {
	_, err := ex.Exec(
		`INSERT INTO session_tasks (session_id, task_id, relation_id, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(session_id, task_id) DO UPDATE SET
		     updated_at  = excluded.updated_at,
		     relation_id = CASE
		         WHEN `+RankCaseSQL("excluded.relation_id")+`
		            < `+RankCaseSQL("session_tasks.relation_id")+`
		         THEN excluded.relation_id
		         ELSE session_tasks.relation_id
		     END`,
		sessionID, taskID, int(rel), now, now,
	)
	if err != nil {
		return fmt.Errorf("upsert session_tasks (%d, %d): %w", sessionID, taskID, err)
	}
	return setFocus(ex, sessionID, taskID, rel)
}

// setFocus repoints the session's focused task (E-2188) when the capture is one
// that moves focus (Relation.MovesFocus). It runs on EVERY such touch, first or
// repeat — unlike relation_id, which is upgraded once and then sticks — so
// returning to a task already on the board moves focus back to it.
//
// Lives beside Record because Record is the single write path for
// session_tasks: every capture that could move focus already passes through it,
// and a second call site would be a second place to forget.
func setFocus(ex Execer, sessionID, taskID int64, rel Relation) error {
	if !rel.MovesFocus() {
		return nil
	}
	// The EXISTS guard keeps a capture for a task id with no tasks row — which
	// session_tasks tolerates by design, having no FK on task_id — from failing
	// the focus column's foreign key and, with it, the task mutation that carried
	// the capture. Focus is a display aid; it is never worth an event.
	if _, err := ex.Exec(
		`UPDATE sessions SET focus_task_id = ?
		  WHERE id = ? AND EXISTS (SELECT 1 FROM tasks WHERE id = ?)`,
		taskID, sessionID, taskID,
	); err != nil {
		return fmt.Errorf("set focus for session %d to task %d: %w", sessionID, taskID, err)
	}
	return nil
}

// RankCaseSQL builds a SQL expression mapping a session_tasks.relation_id
// column to its Rank(), so the upgrade comparison in Record runs inside the
// statement instead of as a read-then-write.
//
// Generated from All() rather than hand-written so the Go enum stays the single
// source of truth (ED-1506): adding a relation with a Rank automatically ranks
// it here, and no SQL literal can drift from the ladder it mirrors.
//
// The ELSE arm catches both NULL (pre-E-1462 historical rows) and any id this
// binary does not know, giving each the unknown rank — below every real
// relation. That is the safe direction in both cases: a NULL row is filled in by
// the next capture, and a value from a newer binary is never treated as
// outranking real work.
func RankCaseSQL(col string) string {
	var b strings.Builder
	b.WriteString("CASE " + col)
	for _, rel := range All() {
		fmt.Fprintf(&b, " WHEN %d THEN %d", int(rel), rel.Rank())
	}
	// Relation(0) is not a member of All(); its Rank() is the documented
	// ranks-last default, which is exactly what NULL and unknown ids need.
	fmt.Fprintf(&b, " ELSE %d END", Relation(0).Rank())
	return b.String()
}
