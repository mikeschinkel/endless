// This file is the assertion list for E-2074's column drops, and NOTHING else.
//
// It is separate from transcript_test.go for one reason: .endless/tasks/
// e-2074-verify.sh sweeps the tree for any surviving mention of the dropped
// identifiers, and a guard that must NAME what it forbids would trip that sweep
// forever. The sweep exempts this file by name — the same exemption it grants
// the change file and its own assertion list — so the exemption stays scoped to
// code whose whole job is to prove absence.
package monitor

import "testing"

// TestSessionsHasNoneOfTheE2074Columns pins the four drops against the schema
// rather than against deleted accessors, so a column re-added in schema.sql or
// by a stray change file fails loudly here. Same shape as the E-1905 guard
// above, and the same reason: db.py's _migrate_v3 runs on every Python connect
// and has twice been the thing that resurrected a just-dropped column.
func TestSessionsHasNoneOfTheE2074Columns(t *testing.T) {
	db := withTestDB(t)
	for _, col := range []string{"summary", "short_id", "kind_id", "plan_file_path"} {
		var n int
		if err := db.QueryRow(
			"SELECT count(*) FROM pragma_table_info('sessions') WHERE name = ?", col,
		).Scan(&n); err != nil {
			t.Fatalf("introspect sessions.%s: %v", col, err)
		}
		if n != 0 {
			t.Errorf("sessions still declares dropped column %q (E-2074)", col)
		}
	}

	// The overreach guard: session_gates.kind_id (gatekind) and
	// processes.kind_id (processkind) are different columns on different
	// tables, and neither may have been swept up with sessions.kind_id.
	for _, tbl := range []string{"session_gates", "processes"} {
		var n int
		if err := db.QueryRow(
			"SELECT count(*) FROM pragma_table_info('" + tbl + "') WHERE name = 'kind_id'",
		).Scan(&n); err != nil {
			t.Fatalf("introspect %s.kind_id: %v", tbl, err)
		}
		if n != 1 {
			t.Errorf("%s.kind_id count = %d, want 1 — it is a different column", tbl, n)
		}
	}

	// And session_kinds itself is gone while process_kinds, the other ED-1506
	// enum mirror, is not.
	var kinds, procKinds int
	if err := db.QueryRow(
		"SELECT (SELECT count(*) FROM sqlite_master WHERE type='table' AND name='session_kinds'),"+
			" (SELECT count(*) FROM sqlite_master WHERE type='table' AND name='process_kinds')",
	).Scan(&kinds, &procKinds); err != nil {
		t.Fatalf("introspect enum mirror tables: %v", err)
	}
	if kinds != 0 {
		t.Error("the session_kinds table survives (E-2074 drops it)")
	}
	if procKinds != 1 {
		t.Error("the process_kinds table was swept up with session_kinds")
	}
}
