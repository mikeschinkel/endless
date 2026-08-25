// Package sessiontaskrelation defines the Relation Go enum, the source of truth
// for session_tasks.relation_id (per ED-1506: const-in-code is the source of
// truth, the session_task_relations SQL table mirrors it). The package lives
// outside internal/events and internal/monitor so both can depend on it without
// a cycle.
//
// Relation classifies HOW a task entered a session's scope, captured by the
// task-mutation executors (E-1462) and by the session-task verbs (E-1696):
//   - claimed:    the session's claimed task (task.claimed)
//   - surfaced:   created during the session (task.created / task.imported)
//   - queued:     explicitly promoted to session work (`session task add`)
//   - revisited:  a pre-existing task the session touched but did not claim
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
// "Goal:" cannot be reasoned about, "Claimed:" says what happened.
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it + give it a Rank. The VerifyIntegrity startup check fails closed
// on drift.
package sessiontaskrelation

import (
	"database/sql"
	"fmt"
)

// Relation is the closed enumeration of session-task scope-entry relations.
type Relation int

const (
	RelationClaimed   Relation = 1
	RelationSurfaced  Relation = 2
	RelationRevisited Relation = 3
	// RelationReferenced and RelationQueued are APPENDED (E-1696), never
	// renumbered: the ids are persisted in session_tasks.relation_id, so
	// inserting a value in the middle would silently reclassify live rows.
	// Display order and precedence are carried by Rank(), not by id.
	RelationReferenced Relation = 4
	RelationQueued     Relation = 5
)

// String returns the lowercase machine slug (matches session_task_relations.slug).
func (r Relation) String() string {
	switch r {
	case RelationClaimed:
		return "claimed"
	case RelationSurfaced:
		return "surfaced"
	case RelationRevisited:
		return "revisited"
	case RelationReferenced:
		return "referenced"
	case RelationQueued:
		return "queued"
	default:
		return fmt.Sprintf("Relation(%d)", int(r))
	}
}

// Label returns the human display string (matches session_task_relations.label).
func (r Relation) Label() string {
	switch r {
	case RelationClaimed:
		return "Claimed"
	case RelationSurfaced:
		return "Surfaced"
	case RelationRevisited:
		return "Revisited"
	case RelationReferenced:
		return "Referenced"
	case RelationQueued:
		return "Queued"
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
	case "surfaced":
		return RelationSurfaced, nil
	case "revisited":
		return RelationRevisited, nil
	case "referenced":
		return RelationReferenced, nil
	case "queued":
		return RelationQueued, nil
	default:
		return 0, fmt.Errorf(
			"sessiontaskrelation: invalid relation %q "+
				"(valid: claimed, surfaced, queued, revisited, referenced)", s,
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
		RelationClaimed, RelationSurfaced, RelationRevisited,
		RelationReferenced, RelationQueued,
	}
}

// Rank orders the relations from strongest claim on the session to weakest:
// claimed < queued < surfaced < revisited < referenced. LOWER rank = STRONGER.
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
//     left a session's claimed task reading `surfaced`.
//
//  2. DISPLAY TIER (E-1462's Extension). `session status` ranks equally
//     actionable rows by this order, so decided work (claimed/queued) sits above
//     incidental work (surfaced/revisited) and read-only relevance
//     (`referenced`) sinks to the bottom.
//
// An unknown value ranks last, so a relation from a newer binary degrades to
// "least prominent, never upgrades over anything" rather than silently
// outranking real work.
func (r Relation) Rank() int {
	switch r {
	case RelationClaimed:
		return 0
	case RelationQueued:
		return 1
	case RelationSurfaced:
		return 2
	case RelationRevisited:
		return 3
	case RelationReferenced:
		return 4
	default:
		return 5
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
