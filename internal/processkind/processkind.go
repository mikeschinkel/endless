// Package processkind defines the ProcessKind Go enum, the source of truth for
// processes.kind_id (mirroring the SessionKind/TaskType pattern per ED-1506:
// const-in-code is the source of truth, the process_kinds SQL table mirrors it
// for FK enforcement and queryability). It lives outside internal/monitor so
// other packages can depend on it without a cycle.
//
// Adding a value = add an enum constant here + add a seed row in
// internal/schema/schema.sql + add a row in the per-ticket migration that
// introduces it. The VerifyIntegrity startup check fails closed on drift.
//
// # What a "process" is
//
// A `processes` row answers "WHERE does this session run", durably. The kind
// discriminates how to read the other two identity columns:
//
//   - tmux (1) — server_uuid is the tmux server's @server_uuid, address is the
//     pane id ("%414"). The PAIR is the identity: a tmux pane id alone is
//     reused by a later server, which is exactly the defect E-1898 exists to
//     remove.
//   - pid (2)  — server_uuid is NULL (a bare OS process has no multiplexer),
//     address is the pid rendered as text.
//
// The enum is deliberately multiplexer-agnostic. cmux and herdr join as new
// kinds with their own snapshot populator; nothing in the liveness JOIN, the
// sessions table, or any consumer changes to admit them.
package processkind

import (
	"database/sql"
	"fmt"
)

// ProcessKind is the closed enumeration of process kind values.
type ProcessKind int

const (
	ProcessKindTmux ProcessKind = 1
	ProcessKindPID  ProcessKind = 2
)

// String returns the lowercase machine slug (matches process_kinds.slug).
func (k ProcessKind) String() string {
	switch k {
	case ProcessKindTmux:
		return "tmux"
	case ProcessKindPID:
		return "pid"
	default:
		return fmt.Sprintf("ProcessKind(%d)", int(k))
	}
}

// Label returns the human display string (matches process_kinds.label).
func (k ProcessKind) Label() string {
	switch k {
	case ProcessKindTmux:
		return "Tmux pane"
	case ProcessKindPID:
		return "OS process"
	default:
		return ""
	}
}

// UsesServer reports whether this kind's identity includes a server_uuid.
// Callers use it to decide whether a NULL server_uuid is legitimate (pid) or a
// defect (tmux). Keeping the rule here rather than at each call site means a
// future multiplexer kind cannot be misclassified by an out-of-date caller.
func (k ProcessKind) UsesServer() bool {
	return k == ProcessKindTmux
}

// Parse converts a slug from CLI / external input to a ProcessKind. Returns an
// error for unknown slugs.
func Parse(s string) (ProcessKind, error) {
	switch s {
	case "tmux":
		return ProcessKindTmux, nil
	case "pid":
		return ProcessKindPID, nil
	default:
		return 0, fmt.Errorf("processkind: invalid process kind %q (valid: tmux, pid)", s)
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
func All() []ProcessKind {
	return []ProcessKind{ProcessKindTmux, ProcessKindPID}
}

// VerifyIntegrity asserts that the process_kinds SQL table matches the Go enum.
// Runs once at startup (from monitor.DB() after schema.SQL applies). Returns an
// error on any drift: an enum constant with no matching row, a slug or label
// mismatch, or a process_kinds row whose id matches no constant. Callers are
// expected to hard-fail the process.
func VerifyIntegrity(db *sql.DB) error {
	type row struct {
		id    int
		slug  string
		label string
	}
	rows, err := db.Query("SELECT id, slug, label FROM process_kinds")
	if err != nil {
		return fmt.Errorf("processkind: query process_kinds: %w", err)
	}
	defer rows.Close()

	byID := make(map[int]row)
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.slug, &r.label); err != nil {
			return fmt.Errorf("processkind: scan process_kinds row: %w", err)
		}
		byID[r.id] = r
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("processkind: iterate process_kinds: %w", err)
	}

	for _, k := range All() {
		r, ok := byID[int(k)]
		if !ok {
			return fmt.Errorf("processkind: enum constant %s (id=%d) missing from process_kinds table",
				k.String(), int(k))
		}
		if r.slug != k.String() {
			return fmt.Errorf("processkind: id=%d slug mismatch: enum=%q, table=%q",
				int(k), k.String(), r.slug)
		}
		if r.label != k.Label() {
			return fmt.Errorf("processkind: id=%d label mismatch: enum=%q, table=%q",
				int(k), k.Label(), r.label)
		}
		delete(byID, int(k))
	}

	for id, r := range byID {
		return fmt.Errorf("processkind: process_kinds row id=%d slug=%q has no matching enum constant",
			id, r.slug)
	}

	return nil
}

// KindForLegacyProcess classifies a pre-E-1898 `sessions.process` string by
// shape, for the migration backfill and for any remaining reader of the old
// column. "%N" is a tmux pane; "pid:N" is a bare OS process.
//
// ok is false for anything else (including the empty string), which the
// migration treats as "no binding" rather than guessing — a wrong guess would
// mint a processes row that can never match an observation.
func KindForLegacyProcess(process string) (kind ProcessKind, address string, ok bool) {
	switch {
	case len(process) > 1 && process[0] == '%':
		return ProcessKindTmux, process, true
	case len(process) > 4 && process[:4] == "pid:":
		return ProcessKindPID, process[4:], true
	default:
		return 0, "", false
	}
}
