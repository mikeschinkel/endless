// Package gatekind defines the GateKind Go enum, the source of truth for
// session_gates.kind_id (per ED-1506: const-in-code is the source of truth, the
// gate_kinds SQL table mirrors it). The package lives outside internal/events
// and internal/monitor so both can depend on it without a cycle.
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it. The VerifyIntegrity startup check fails closed on drift.
package gatekind

import (
	"database/sql"
	"fmt"
)

// GateKind is the closed enumeration of session-gate kinds.
type GateKind int

const (
	GateKindRevisit GateKind = 1
)

// String returns the lowercase machine slug (matches gate_kinds.slug).
func (k GateKind) String() string {
	switch k {
	case GateKindRevisit:
		return "revisit"
	default:
		return fmt.Sprintf("GateKind(%d)", int(k))
	}
}

// Label returns the human display string (matches gate_kinds.label).
func (k GateKind) Label() string {
	switch k {
	case GateKindRevisit:
		return "Revisit"
	default:
		return ""
	}
}

// Parse converts a slug from CLI / external input to a GateKind. Returns an
// error for unknown slugs.
func Parse(s string) (GateKind, error) {
	switch s {
	case "revisit":
		return GateKindRevisit, nil
	default:
		return 0, fmt.Errorf("gatekind: invalid gate kind %q (valid: revisit)", s)
	}
}

// Validate returns an error if s is not a recognized slug.
func Validate(s string) error {
	_, err := Parse(s)
	return err
}

// All returns the canonical set in id order. Used by VerifyIntegrity and by
// callers that need to enumerate the enum.
func All() []GateKind {
	return []GateKind{GateKindRevisit}
}

// VerifyIntegrity asserts that the gate_kinds SQL table matches the Go enum.
// Runs once at startup (from monitor.DB() after schema.SQL applies). Returns
// an error on any drift: an enum constant with no matching row, a slug or
// label mismatch, or a gate_kinds row whose id does not match any constant.
// Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id    int
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM gate_kinds")
	if err != nil {
		return fmt.Errorf("gatekind: query gate_kinds: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("gatekind: scan gate_kinds row: %w", err)
		}
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("gatekind: iterate gate_kinds: %w", err)
	}

	for _, gk := range All() {
		r, ok := byID[int(gk)]
		if !ok {
			return fmt.Errorf("gatekind: enum constant %s (id=%d) missing from gate_kinds table",
				gk.String(), int(gk))
		}
		if r.slug != gk.String() {
			return fmt.Errorf("gatekind: id=%d slug mismatch: enum=%q, table=%q",
				int(gk), gk.String(), r.slug)
		}
		if r.label != gk.Label() {
			return fmt.Errorf("gatekind: id=%d label mismatch: enum=%q, table=%q",
				int(gk), gk.Label(), r.label)
		}
		delete(byID, int(gk))
	}

	for id, r := range byID {
		return fmt.Errorf("gatekind: gate_kinds row id=%d slug=%q has no matching enum constant",
			id, r.slug)
	}

	return nil
}
