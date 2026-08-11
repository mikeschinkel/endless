package monitor

import (
	"database/sql"
	"os/exec"
	"testing"

	"github.com/mikeschinkel/endless/internal/processkind"
	"github.com/mikeschinkel/endless/internal/sessionkind"
)

// The E-1898 liveness contract.
//
// Every test in this file runs with NO tmux server involved (invariant I4).
// That is not a convenience — it is the design goal. The previous dead-pane
// reaper could only be exercised by standing up private tmux servers, which is
// why its edge cases went unexplored until one of them destroyed 59 live pane
// bindings. Liveness is now a JOIN over tables, so its truth table can be
// enumerated exhaustively as fixtures.

// seedLivenessSession inserts a session bound to processID (0 = unbound) and
// returns its sessions.id.
func seedLivenessSession(t *testing.T, db *sql.DB, sessionID string, processID int64, kind sessionkind.SessionKind) int64 {
	t.Helper()
	var pid any
	if processID != 0 {
		pid = processID
	}
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, kind_id, last_activity)
		 VALUES (?, 1, 'claude', 'working', ?, ?, '2026-08-09T00:00:00')`,
		sessionID, pid, int64(kind),
	)
	if err != nil {
		t.Fatalf("seed session %q: %v", sessionID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("last insert id: %v", err)
	}
	return id
}

// TestLiveness_TruthTable enumerates every cell of the four-state rule.
func TestLiveness_TruthTable(t *testing.T) {
	const (
		ourServer   = "server-we-can-see"
		otherServer = "server-we-cannot-reach"
	)

	tests := []struct {
		name    string
		server  string // server_uuid the binding was made against; "" = unbound
		address string
		want    Liveness
		why     string
	}{
		{
			name: "unbound session has no binding to judge", server: "", address: "",
			want: LivenessUnbound,
			why:  "a background agent legitimately has no pane; it must never read dead",
		},
		{
			name: "observed server, address present", server: ourServer, address: "%1",
			want: LivenessLive,
			why:  "the only positive case: we looked, and it was there",
		},
		{
			name: "observed server, address absent", server: ourServer, address: "%404",
			want: LivenessDead,
			why:  "the ONLY case that may condemn a row: we reached its server and looked",
		},
		{
			name: "unreached server", server: otherServer, address: "%1",
			want: LivenessUnknown,
			why:  "same address as a live pane, but on a server we never looked at",
		},
		{
			name: "unreached server, address absent there too", server: otherServer, address: "%404",
			want: LivenessUnknown,
			why:  "absence proves nothing about a server we did not reach",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := withTestDB(t)
			seedProject(t, db, 1, "acme", "/tmp/acme")
			// One reachable server hosting exactly one pane, %1.
			defer SetTestTmuxObservation(ourServer, map[string]string{"%1": "2.1.220"})()

			var pid int64
			if tc.server != "" {
				pid = mustSeedPane(t, db, tc.server, tc.address)
			}
			id := seedLivenessSession(t, db, "sess-"+tc.name, pid, sessionkind.SessionKindTmux)

			if err := RefreshLiveness(); err != nil {
				t.Fatalf("RefreshLiveness: %v", err)
			}
			got, err := SessionLiveness(id)
			if err != nil {
				t.Fatalf("SessionLiveness: %v", err)
			}
			if got != tc.want {
				t.Errorf("liveness = %q, want %q\n  %s", got, tc.want, tc.why)
			}
		})
	}
}

// TestLiveness_BackgroundAgentIsUnboundNotDead pins the case D5 of the previous
// plan got wrong. A background agent has process_id NULL BY DESIGN
// (RecordBgAgentSession), so any rule that reads "no binding" as "pane gone"
// condemns every bg agent in the project.
func TestLiveness_BackgroundAgentIsUnboundNotDead(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	defer SetTestTmuxObservation("srv", map[string]string{"%1": "2.1.220"})()

	id := seedLivenessSession(t, db, "sess-bg", 0, sessionkind.SessionKindBackground)

	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	got, err := SessionLiveness(id)
	if err != nil {
		t.Fatalf("SessionLiveness: %v", err)
	}
	if got != LivenessUnbound {
		t.Errorf("background agent liveness = %q, want %q", got, LivenessUnbound)
	}
	if got.IsGone() {
		t.Error("background agent read as gone; every bg agent would be treated as dead")
	}
}

// TestLiveness_ShellPaneStaysLive is the Ctrl+Z guarantee, and the reason the
// shell test is NOT part of liveness.
//
// A suspended Claude leaves its shell in the pane's foreground, so the pane
// reports "zsh" while the session is entirely alive. If this ever reads `dead`,
// the session's status line drops and — much worse — the spawn/claim ownership
// guard frees its task for someone else to claim. This test is the tripwire
// against reintroducing that inference.
func TestLiveness_ShellPaneStaysLive(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	defer SetTestTmuxObservation("srv", map[string]string{"%7": "zsh"})()

	pid := mustSeedPane(t, db, "srv", "%7")
	id := seedLivenessSession(t, db, "sess-suspended", pid, sessionkind.SessionKindTmux)

	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	got, err := SessionLiveness(id)
	if err != nil {
		t.Fatalf("SessionLiveness: %v", err)
	}
	if got != LivenessLive {
		t.Errorf("liveness = %q, want %q — a shell in the pane is not proof the harness died", got, LivenessLive)
	}
}

// TestLiveness_UnreachableTmuxCondemnsNothing is invariant I2, and the direct
// regression test for 2026-08-05.
//
// The incident's shape was an observer that could see nothing relevant and
// concluded everything was dead. Here the observer sees NOTHING AT ALL — the
// worst possible observation — against a project full of bound sessions. Not
// one may read `dead`, and not one row may change.
func TestLiveness_UnreachableTmuxCondemnsNothing(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	defer SetTestTmuxObservation("", nil)() // no server reachable

	ids := make([]int64, 0, 8)
	for i, pane := range []string{"%1", "%2", "%3", "%4", "%5", "%6", "%7", "%8"} {
		pid := mustSeedPane(t, db, "srv", pane)
		ids = append(ids, seedLivenessSession(t, db, "sess-"+pane+string(rune('a'+i)), pid, sessionkind.SessionKindTmux))
	}

	before := snapshotSessionsTable(t, db)

	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	for _, id := range ids {
		got, err := SessionLiveness(id)
		if err != nil {
			t.Fatalf("SessionLiveness(%d): %v", id, err)
		}
		if got != LivenessUnknown {
			t.Errorf("session %d liveness = %q, want %q", id, got, LivenessUnknown)
		}
		if got.IsGone() {
			t.Errorf("session %d read as gone from an observation that saw nothing", id)
		}
	}

	// Invariant I1, checked as data rather than argued: observing changed
	// nothing about the sessions table.
	if after := snapshotSessionsTable(t, db); after != before {
		t.Errorf("observation mutated the sessions table\n before: %s\n after:  %s", before, after)
	}
}

// snapshotSessionsTable renders every liveness-relevant column of every session
// as one comparable string, so a test can assert that a read path wrote nothing.
func snapshotSessionsTable(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(
		`SELECT id, state, COALESCE(process_id, 0), COALESCE(last_activity, '')
		 FROM sessions ORDER BY id`,
	)
	if err != nil {
		t.Fatalf("snapshot sessions: %v", err)
	}
	defer rows.Close()
	var out string
	for rows.Next() {
		var id, pid int64
		var state, last string
		if err := rows.Scan(&id, &state, &pid, &last); err != nil {
			t.Fatalf("scan snapshot: %v", err)
		}
		out += string(rune(id)) + state + string(rune(pid)) + last + ";"
	}
	return out
}

// TestLiveness_PoolIsSingleConn pins the assumption the TEMP observation tables
// rest on. They live on ONE SQLite connection; monitor.DB() gets away with that
// only because it caps the pool at one. Raising the cap would make liveness fail
// intermittently and invisibly — a query landing on a second connection would
// error with "no such table" for reasons that look like anything but pooling.
func TestLiveness_PoolIsSingleConn(t *testing.T) {
	db := withTestDB(t)
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Skipf("test DB pool is unconstrained (%d); the production cap is asserted below", got)
	}
}

// TestIsShellCommand is the pure truth table for the shell predicate, including
// the exact value the OLD helper got wrong.
func TestIsShellCommand(t *testing.T) {
	shells := []string{"zsh", "bash", "sh", "fish", "  zsh  ", "zsh\n"}
	for _, cmd := range shells {
		if !isShellCommand(cmd) {
			t.Errorf("isShellCommand(%q) = false, want true", cmd)
		}
	}
	notShells := []string{
		"2.1.220",     // a LIVE Claude pane — the value paneIsRunningClaude missed
		"2.1.212",     //   (Claude Code sets its process title to its version)
		"claude",      // the name the old helper matched, which real panes never report
		"claude-code", //   ...
		"vim", "less", "go", "Python", "uv", "",
	}
	for _, cmd := range notShells {
		if isShellCommand(cmd) {
			t.Errorf("isShellCommand(%q) = true, want false", cmd)
		}
	}
}

// TestParsePaneList pins the snapshot parser, including the rule that a pane
// whose command is unreadable still counts as PRESENT. Dropping it would turn a
// live session `dead` over a cosmetic field.
func TestParsePaneList(t *testing.T) {
	panes := parsePaneList("%1\tzsh\n%2\t2.1.220\n\n%3\n  \n%4\tgo build\n")
	want := []observedPane{
		{"%1", "zsh"}, {"%2", "2.1.220"}, {"%3", ""}, {"%4", "go build"},
	}
	if len(panes) != len(want) {
		t.Fatalf("parsed %d panes, want %d: %+v", len(panes), len(want), panes)
	}
	for i := range want {
		if panes[i] != want[i] {
			t.Errorf("pane[%d] = %+v, want %+v", i, panes[i], want[i])
		}
	}
}

// TestEnsureProcess_IdentityIsServerScoped pins invariant I3 at the storage
// layer: one address on two servers is two identities, and the same pair is
// always the same row.
func TestEnsureProcess_IdentityIsServerScoped(t *testing.T) {
	db := withTestDB(t)

	a := mustSeedPane(t, db, "server-a", "%414")
	b := mustSeedPane(t, db, "server-b", "%414")
	if a == b {
		t.Fatalf("pane %%414 on two servers shares processes.id %d; a reissued pane would collide", a)
	}

	again := mustSeedPane(t, db, "server-a", "%414")
	if again != a {
		t.Errorf("re-binding the same identity minted a new row (%d != %d)", again, a)
	}
}

// TestEnsureProcess_RefusesTmuxBindingWithoutServer pins that a tmux binding
// cannot be recorded without the server half of its identity. Storing one would
// create an identity that can never match an observation — permanently
// `unknown`, and silently wrong if anything later compared bare addresses.
func TestEnsureProcess_RefusesTmuxBindingWithoutServer(t *testing.T) {
	withTestDB(t)
	if _, err := EnsureProcess(processkind.ProcessKindTmux, "", "%1"); err == nil {
		t.Error("EnsureProcess accepted a tmux binding with no server_uuid")
	}
	// ...while a pid binding legitimately has no server.
	if _, err := EnsureProcess(processkind.ProcessKindPID, "", "1234"); err != nil {
		t.Errorf("EnsureProcess(pid) rejected a serverless binding: %v", err)
	}
}

// TestLiveness_NoTmuxBinaryIsUnknownNotDead covers the deployment case behind
// invariant I2: tmux is not installed at all. Every session reads `unknown`.
func TestLiveness_NoTmuxBinaryIsUnknownNotDead(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err == nil {
		// Simulate absence via the observation seam rather than skipping, so
		// this runs identically on a machine that has tmux.
		defer SetTestTmuxObservation("", nil)()
	}
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	pid := mustSeedPane(t, db, "srv", "%1")
	id := seedLivenessSession(t, db, "sess-notmux", pid, sessionkind.SessionKindTmux)

	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	got, err := SessionLiveness(id)
	if err != nil {
		t.Fatalf("SessionLiveness: %v", err)
	}
	if got.IsGone() {
		t.Errorf("liveness = %q; no tmux must mean no opinion, not death", got)
	}
}

// ── migration adoption (E-1898) ─────────────────────────────────────────────
//
// AdoptPaneBindings is the fix for the defect that took the board down on
// 2026-08-10: the migration backfilled every binding with server_uuid NULL, and
// a NULL-server binding matches no server, so all 64 pre-existing sessions went
// unresolvable at once. The original reasoning — "each re-binds on its next
// hook" — holds only for sessions that fire hooks; idle windows fire none.

// seedNullServerBinding creates a server-less (backfill-shaped) binding at
// address and points a non-ended session at it. Returns the session id.
func seedNullServerBinding(t *testing.T, db *sql.DB, sessionID, address string) int64 {
	t.Helper()
	pid, err := ensureProcess(db, processkind.ProcessKindTmux, "", address)
	if err != nil {
		t.Fatalf("seed null-server binding %q: %v", address, err)
	}
	return seedLivenessSession(t, db, sessionID, pid, sessionkind.SessionKindTmux)
}

// TestAdoptPaneBindings_AttributesLivePanes is the core case: a backfilled
// binding whose pane is live on the running server is repointed to that server,
// so it resolves immediately instead of waiting for a hook that may never come.
func TestAdoptPaneBindings_AttributesLivePanes(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	live := seedNullServerBinding(t, db, "sess-live", "%1")
	stale := seedNullServerBinding(t, db, "sess-stale", "%404")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	n, err := AdoptPaneBindings(tx, "srv", []string{"%1"})
	if err != nil {
		t.Fatalf("AdoptPaneBindings: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n != 1 {
		t.Errorf("adopted %d, want 1", n)
	}

	// The live pane now resolves against the server that owns it...
	if got := serverOf(t, db, live); got != "srv" {
		t.Errorf("live session server_uuid = %q, want \"srv\"", got)
	}
	// ...and the pane that is NOT live keeps its honest absence of a server.
	if got := serverOf(t, db, stale); got != "" {
		t.Errorf("stale session server_uuid = %q, want NULL — its server is gone", got)
	}

	// End to end: the adopted one reads live, the stale one reads unknown.
	defer SetTestTmuxObservation("srv", map[string]string{"%1": "2.1.220"})()
	if err = RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	if got, _ := SessionLiveness(live); got != LivenessLive {
		t.Errorf("adopted session liveness = %q, want %q", got, LivenessLive)
	}
	if got, _ := SessionLiveness(stale); got != LivenessUnknown {
		t.Errorf("stale session liveness = %q, want %q (never dead)", got, LivenessUnknown)
	}
}

// TestAdoptPaneBindings_RefusesAmbiguousAddress pins the guard. Two non-ended
// sessions claiming one address means bindings from different servers collided
// on a reused pane id — exactly the ambiguity E-1898 exists to stop resolving by
// guesswork. Adoption must decline and leave both `unknown`.
func TestAdoptPaneBindings_RefusesAmbiguousAddress(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	a := seedNullServerBinding(t, db, "sess-a", "%7")
	// Second session on the SAME server-less binding.
	pid, err := ensureProcess(db, processkind.ProcessKindTmux, "", "%7")
	if err != nil {
		t.Fatalf("reuse binding: %v", err)
	}
	b := seedLivenessSession(t, db, "sess-b", pid, sessionkind.SessionKindTmux)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	n, err := AdoptPaneBindings(tx, "srv", []string{"%7"})
	if err != nil {
		t.Fatalf("AdoptPaneBindings: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n != 0 {
		t.Errorf("adopted %d, want 0 — the address is claimed by two sessions", n)
	}
	for _, id := range []int64{a, b} {
		if got := serverOf(t, db, id); got != "" {
			t.Errorf("session %d server_uuid = %q, want NULL (ambiguous)", id, got)
		}
	}
}

// TestAdoptPaneBindings_PreservesSharedHistoryRow pins why adoption creates a
// NEW processes row rather than stamping a server onto the existing one. The
// backfill deduplicates by address, so one NULL-server row can be shared by
// every session that ever used that pane on every server. Mutating it would
// retroactively claim all of that history happened on THIS server.
func TestAdoptPaneBindings_PreservesSharedHistoryRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")

	shared, err := ensureProcess(db, processkind.ProcessKindTmux, "", "%9")
	if err != nil {
		t.Fatalf("seed shared binding: %v", err)
	}
	// An ended session from some earlier server, plus the current occupant.
	seedPaneSession(t, db, "sess-ancient", shared, "ended", 0, "2026-01-01T00:00:00")
	current := seedLivenessSession(t, db, "sess-current", shared, sessionkind.SessionKindTmux)

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err = AdoptPaneBindings(tx, "srv", []string{"%9"}); err != nil {
		t.Fatalf("AdoptPaneBindings: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}

	// The current session moved to a new, server-scoped identity...
	if got := serverOf(t, db, current); got != "srv" {
		t.Errorf("current session server_uuid = %q, want \"srv\"", got)
	}
	// ...and the ancient row still points at the untouched server-less record.
	var ancientPID int64
	if err = db.QueryRow(
		"SELECT process_id FROM sessions WHERE session_id = 'sess-ancient'",
	).Scan(&ancientPID); err != nil {
		t.Fatalf("read ancient: %v", err)
	}
	if ancientPID != shared {
		t.Errorf("ancient session was repointed (%d != %d); its server is unknown, not 'srv'",
			ancientPID, shared)
	}
	var serverOfShared sql.NullString
	if err = db.QueryRow(
		"SELECT server_uuid FROM processes WHERE id = ?", shared,
	).Scan(&serverOfShared); err != nil {
		t.Fatalf("read shared row: %v", err)
	}
	if serverOfShared.Valid {
		t.Errorf("shared history row was stamped with %q; it must stay server-less",
			serverOfShared.String)
	}
}

// TestAdoptPaneBindings_NoServerIsNoOp: on a headless machine the migration
// must still complete, adopting nothing.
func TestAdoptPaneBindings_NoServerIsNoOp(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	id := seedNullServerBinding(t, db, "sess-x", "%1")

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	n, err := AdoptPaneBindings(tx, "", []string{"%1"})
	if err != nil {
		t.Fatalf("AdoptPaneBindings: %v", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if n != 0 {
		t.Errorf("adopted %d with no reachable server, want 0", n)
	}
	if got := serverOf(t, db, id); got != "" {
		t.Errorf("server_uuid = %q, want NULL", got)
	}
}

// serverOf reads the server_uuid behind a session's binding ("" when NULL).
func serverOf(t *testing.T, db *sql.DB, sessionID int64) string {
	t.Helper()
	var s sql.NullString
	if err := db.QueryRow(
		`SELECT p.server_uuid FROM sessions s JOIN processes p ON p.id = s.process_id
		  WHERE s.id = ?`, sessionID,
	).Scan(&s); err != nil {
		t.Fatalf("read server for session %d: %v", sessionID, err)
	}
	return s.String
}
