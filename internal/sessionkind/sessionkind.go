// Package sessionkind defines the SessionKind Go enum, the source of truth
// for sessions.kind_id (mirroring the TaskType pattern per ED-1506:
// const-in-code is the source of truth, the session_kinds SQL table mirrors
// it for FK enforcement and queryability). The package lives outside
// internal/monitor so other packages can depend on it without a cycle.
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it. The VerifyIntegrity startup check fails closed on drift.
//
// The discriminator separates the two session shapes:
//   - tmux (1)       — a session bound to a live tmux pane (sessions.process
//                      holds the pane id). This is every session today.
//   - background (2) — a headless background agent with no tmux pane;
//                      sessions.process is legitimately NULL. Written by the
//                      --bg dispatch path (E-1568).
package sessionkind

import (
	"database/sql"
	"fmt"
)

// SessionKind is the closed enumeration of session kind values.
type SessionKind int

const (
	SessionKindTmux       SessionKind = 1
	SessionKindBackground SessionKind = 2
)

// String returns the lowercase machine slug (matches session_kinds.slug).
func (k SessionKind) String() string {
	switch k {
	case SessionKindTmux:
		return "tmux"
	case SessionKindBackground:
		return "background"
	default:
		return fmt.Sprintf("SessionKind(%d)", int(k))
	}
}

// Label returns the human display string (matches session_kinds.label).
func (k SessionKind) Label() string {
	switch k {
	case SessionKindTmux:
		return "Tmux"
	case SessionKindBackground:
		return "Background"
	default:
		return ""
	}
}

// Parse converts a slug from CLI / external input to a SessionKind. Returns an
// error for unknown slugs.
func Parse(s string) (SessionKind, error) {
	switch s {
	case "tmux":
		return SessionKindTmux, nil
	case "background":
		return SessionKindBackground, nil
	default:
		return 0, fmt.Errorf("sessionkind: invalid session kind %q (valid: tmux, background)", s)
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
func All() []SessionKind {
	return []SessionKind{SessionKindTmux, SessionKindBackground}
}

// VerifyIntegrity asserts that the session_kinds SQL table matches the Go enum.
// Runs once at startup (from monitor.DB() after schema.SQL applies). Returns an
// error on any drift: an enum constant with no matching row, a slug or label
// mismatch, or a session_kinds row whose id does not match any constant.
// Callers are expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id    int
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM session_kinds")
	if err != nil {
		return fmt.Errorf("sessionkind: query session_kinds: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("sessionkind: scan session_kinds row: %w", err)
		}
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("sessionkind: iterate session_kinds: %w", err)
	}

	for _, k := range All() {
		r, ok := byID[int(k)]
		if !ok {
			return fmt.Errorf("sessionkind: enum constant %s (id=%d) missing from session_kinds table",
				k.String(), int(k))
		}
		if r.slug != k.String() {
			return fmt.Errorf("sessionkind: id=%d slug mismatch: enum=%q, table=%q",
				int(k), k.String(), r.slug)
		}
		if r.label != k.Label() {
			return fmt.Errorf("sessionkind: id=%d label mismatch: enum=%q, table=%q",
				int(k), k.Label(), r.label)
		}
		delete(byID, int(k))
	}

	for id, r := range byID {
		return fmt.Errorf("sessionkind: session_kinds row id=%d slug=%q has no matching enum constant",
			id, r.slug)
	}

	return nil
}
