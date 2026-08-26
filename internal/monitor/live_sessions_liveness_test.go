package monitor

import (
	"testing"

)

// ListLiveSessions is where the dead-pane reaper's job went, and it is the read
// the spawn/claim ownership guard uses to decide whether a task is already held.
// These tests pin both directions of that decision, because the two failure
// modes are not symmetric: wrongly keeping an owner costs a retry, wrongly
// dropping one hands a live worktree to a second session.

// TestListLiveSessions_OmitsObservablyDead is the E-1807 guarantee, now
// achieved without writing anything. A session whose pane is gone from a server
// we DID reach must not be reported as live.
func TestListLiveSessions_OmitsObservablyDead(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	defer SetTestTmuxObservation("srv", map[string]string{"%1": "2.1.220"})()

	livePID := mustSeedPane(t, db, "srv", "%1")
	deadPID := mustSeedPane(t, db, "srv", "%404")
	seedLivenessSession(t, db, "sess-live", livePID)
	seedLivenessSession(t, db, "sess-dead", deadPID)

	before := snapshotSessionsTable(t, db)

	got, err := ListLiveSessions(1)
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d sessions, want 1: %+v", len(got), got)
	}
	if got[0].SessionID != "sess-live" {
		t.Errorf("listed %q, want sess-live", got[0].SessionID)
	}

	// The ghost dropped out of the READ. Nothing was written to make that true —
	// that is the entire difference from the reaper this replaced.
	if after := snapshotSessionsTable(t, db); after != before {
		t.Error("listing live sessions mutated the sessions table")
	}
	var state string
	if err := db.QueryRow(
		"SELECT state FROM sessions WHERE session_id = 'sess-dead'",
	).Scan(&state); err != nil {
		t.Fatalf("read ghost: %v", err)
	}
	if state != "working" {
		t.Errorf("ghost row state = %q, want working (unlisted, not rewritten)", state)
	}
}

// TestListLiveSessions_KeepsUnknownOwners is invariant I2 at the consumer that
// matters most. When the tmux server cannot be reached, its sessions are
// unprovable — not gone — and must keep holding their tasks.
func TestListLiveSessions_KeepsUnknownOwners(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	defer SetTestTmuxObservation("", nil)() // nothing reachable

	pid := mustSeedPane(t, db, "srv", "%1")
	seedLivenessSession(t, db, "sess-unprovable", pid)

	got, err := ListLiveSessions(1)
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d sessions, want 1 — an unreachable server must not free its tasks", len(got))
	}
	if got[0].Liveness != string(LivenessUnknown) {
		t.Errorf("liveness = %q, want %q", got[0].Liveness, LivenessUnknown)
	}
}

// TestListLiveSessions_KeepsPanelessSessions pins that a session with no pane
// binding is still listed as an owner. Named for background agents until
// E-2074 removed them; the population is now any row whose process_id is NULL
// — before its first hook lands one, or after an E-1898 backfill left it NULL.
func TestListLiveSessions_KeepsPanelessSessions(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	defer SetTestTmuxObservation("srv", map[string]string{"%1": "2.1.220"})()

	seedLivenessSession(t, db, "sess-unbound", 0)

	got, err := ListLiveSessions(1)
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d sessions, want 1 — a paneless row is still live", len(got))
	}
	if got[0].Liveness != string(LivenessUnbound) {
		t.Errorf("liveness = %q, want %q", got[0].Liveness, LivenessUnbound)
	}
}
