package monitor

import (
	"database/sql"
	"testing"
)

// seedGateSession inserts a minimal sessions row so session_gates rows whose
// FK references sessions(session_id) can be inserted. Schema enables
// PRAGMA foreign_keys=ON, so the FK is enforced in tests.
func seedGateSession(t *testing.T, db *sql.DB, sessionID string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO sessions (session_id, platform, state, last_activity)
		 VALUES (?, 'claude', 'working', '2026-05-29T00:00:00')`,
		sessionID,
	); err != nil {
		t.Fatalf("seed session %q: %v", sessionID, err)
	}
}

// openGateCount returns how many session_gates rows for the session have a
// NULL cleared_at (i.e. are still "open"). Used to assert the supersede branch
// of SetGatePending.
func openGateCount(t *testing.T, db *sql.DB, sessionID string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_gates WHERE session_id=? AND cleared_at IS NULL",
		sessionID,
	).Scan(&n); err != nil {
		t.Fatalf("count open gates: %v", err)
	}
	return n
}

// TestSetGatePending_InsertsOpenGate verifies that on a session with no prior
// gates, SetGatePending creates exactly one open row carrying the phrase.
func TestSetGatePending_InsertsOpenGate(t *testing.T) {
	db := withTestDB(t)
	seedGateSession(t, db, "sess-A")

	if err := SetGatePending("sess-A", "pivot-phrase"); err != nil {
		t.Fatalf("SetGatePending: %v", err)
	}
	pending, phrase := IsGatePending("sess-A")
	if !pending {
		t.Error("IsGatePending = false, want true")
	}
	if phrase != "pivot-phrase" {
		t.Errorf("phrase = %q, want pivot-phrase", phrase)
	}
	if got := openGateCount(t, db, "sess-A"); got != 1 {
		t.Errorf("open gate count = %d, want 1", got)
	}
}

// TestSetGatePending_SupersedesPrior verifies the two-row pivot ledger: a
// second SetGatePending on the same session marks the prior row
// cleared_by='superseded' and inserts a fresh open row, so the audit trail of
// trigger phrases is preserved but only one gate is open.
func TestSetGatePending_SupersedesPrior(t *testing.T) {
	db := withTestDB(t)
	seedGateSession(t, db, "sess-A")

	if err := SetGatePending("sess-A", "first"); err != nil {
		t.Fatalf("first SetGatePending: %v", err)
	}
	if err := SetGatePending("sess-A", "second"); err != nil {
		t.Fatalf("second SetGatePending: %v", err)
	}

	if got := openGateCount(t, db, "sess-A"); got != 1 {
		t.Errorf("open gate count = %d, want 1 (only newest open)", got)
	}
	// Total rows = 2: first row stays as superseded telemetry.
	var total int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_gates WHERE session_id=?", "sess-A",
	).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if total != 2 {
		t.Errorf("total gate rows = %d, want 2", total)
	}
	// The first row's cleared_by must be 'superseded'.
	var supersededClearedBy string
	if err := db.QueryRow(
		`SELECT COALESCE(cleared_by, '') FROM session_gates
		 WHERE session_id=? AND matcher_phrase=?`,
		"sess-A", "first",
	).Scan(&supersededClearedBy); err != nil {
		t.Fatalf("read first row cleared_by: %v", err)
	}
	if supersededClearedBy != "superseded" {
		t.Errorf("prior row cleared_by = %q, want superseded", supersededClearedBy)
	}
	// IsGatePending reports the newest phrase.
	pending, phrase := IsGatePending("sess-A")
	if !pending || phrase != "second" {
		t.Errorf("IsGatePending = (%v, %q), want (true, second)", pending, phrase)
	}
}

// TestClearGatePending_MarksCleared verifies the clear path: after a clear,
// IsGatePending reports false and the row carries the supplied cleared_by.
func TestClearGatePending_MarksCleared(t *testing.T) {
	db := withTestDB(t)
	seedGateSession(t, db, "sess-A")

	if err := SetGatePending("sess-A", "phrase"); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := ClearGatePending("sess-A", "task_claim"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if pending, _ := IsGatePending("sess-A"); pending {
		t.Error("IsGatePending = true after clear, want false")
	}
	var clearedBy string
	if err := db.QueryRow(
		"SELECT COALESCE(cleared_by, '') FROM session_gates WHERE session_id=?",
		"sess-A",
	).Scan(&clearedBy); err != nil {
		t.Fatalf("read cleared_by: %v", err)
	}
	if clearedBy != "task_claim" {
		t.Errorf("cleared_by = %q, want task_claim", clearedBy)
	}
}

// TestClearGatePending_NoOpWithoutOpenRow verifies the documented no-op: a
// clear against a session with no open gate completes without error and
// doesn't somehow create a row.
func TestClearGatePending_NoOpWithoutOpenRow(t *testing.T) {
	db := withTestDB(t)

	if err := ClearGatePending("sess-A", "task_claim"); err != nil {
		t.Fatalf("clear no-op: %v", err)
	}
	if openGateCount(t, db, "sess-A") != 0 {
		t.Error("clear without prior gate produced a row")
	}
	var total int
	if err := db.QueryRow(
		"SELECT count(*) FROM session_gates WHERE session_id=?", "sess-A",
	).Scan(&total); err != nil {
		t.Fatalf("count total: %v", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0 after no-op clear", total)
	}
}

// TestIsGatePending_FalseForUnknownSession verifies that an absent session
// reports (false, "") — the SQL ErrNoRows branch, not an error.
func TestIsGatePending_FalseForUnknownSession(t *testing.T) {
	withTestDB(t)
	pending, phrase := IsGatePending("never-touched")
	if pending {
		t.Error("IsGatePending on unknown session = true, want false")
	}
	if phrase != "" {
		t.Errorf("phrase = %q, want empty", phrase)
	}
}
