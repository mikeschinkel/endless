// Package navvia defines the NavVia Go enum, the source of truth for
// session_navigations.via_id (mirroring the TaskType / SessionKind pattern per
// ED-1506: const-in-code is the source of truth, the nav_via_kinds SQL table
// mirrors it for FK enforcement and queryability). The package lives outside
// internal/monitor so other packages can depend on it without a cycle.
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it. The VerifyIntegrity startup check fails closed on drift.
//
// The discriminator records how a focus change was triggered (E-1682):
//   - manual (1) — a hand-driven tmux move (select-window / switch-client),
//                  caught by the global focus-change hook.
//   - goto (2)   — an `endless session goto`, which sets a one-shot
//                  @endless_nav_via=goto marker the recorder reads + clears.
package navvia

import (
	"database/sql"
	"fmt"
)

// NavVia is the closed enumeration of navigation-trigger values.
type NavVia int

const (
	NavViaManual NavVia = 1
	NavViaGoto   NavVia = 2
)

// String returns the lowercase machine slug (matches nav_via_kinds.slug).
func (v NavVia) String() string {
	switch v {
	case NavViaManual:
		return "manual"
	case NavViaGoto:
		return "goto"
	default:
		return fmt.Sprintf("NavVia(%d)", int(v))
	}
}

// Label returns the human display string (matches nav_via_kinds.label).
func (v NavVia) Label() string {
	switch v {
	case NavViaManual:
		return "Manual"
	case NavViaGoto:
		return "Goto"
	default:
		return ""
	}
}

// Parse converts a slug from CLI / external input to a NavVia. The empty
// string maps to NavViaManual: the recorder reads the @endless_nav_via marker,
// which is unset (empty) for every move that is not a `goto`. Returns an error
// for any other unknown slug.
func Parse(s string) (NavVia, error) {
	switch s {
	case "", "manual":
		return NavViaManual, nil
	case "goto":
		return NavViaGoto, nil
	default:
		return 0, fmt.Errorf("navvia: invalid nav via %q (valid: manual, goto)", s)
	}
}

// Validate returns an error if s is not a recognized slug. Used by write paths
// before the DB is touched.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}

// All returns the canonical set in id order. Used by VerifyIntegrity and by
// callers that need to enumerate the enum.
func All() []NavVia {
	return []NavVia{NavViaManual, NavViaGoto}
}

// VerifyIntegrity asserts that the nav_via_kinds SQL table matches the Go enum.
// Runs once at startup (from monitor.DB() after schema.SQL applies). Returns an
// error on any drift: an enum constant with no matching row, a slug or label
// mismatch, or a nav_via_kinds row whose id does not match any constant.
// Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id    int
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM nav_via_kinds")
	if err != nil {
		return fmt.Errorf("navvia: query nav_via_kinds: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("navvia: scan nav_via_kinds row: %w", err)
		}
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("navvia: iterate nav_via_kinds: %w", err)
	}

	for _, v := range All() {
		r, ok := byID[int(v)]
		if !ok {
			return fmt.Errorf("navvia: enum %s (id %d) has no nav_via_kinds row", v, int(v))
		}
		if r.slug != v.String() {
			return fmt.Errorf("navvia: id %d slug %q != enum slug %q", int(v), r.slug, v.String())
		}
		if r.label != v.Label() {
			return fmt.Errorf("navvia: id %d label %q != enum label %q", int(v), r.label, v.Label())
		}
		delete(byID, int(v))
	}
	for id, r := range byID {
		return fmt.Errorf("navvia: nav_via_kinds row id %d (%q) has no matching enum constant", id, r.slug)
	}
	return nil
}
