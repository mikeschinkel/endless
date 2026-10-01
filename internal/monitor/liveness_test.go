package monitor

import (
	"database/sql"
	"os/exec"
	"testing"

	"github.com/mikeschinkel/endless/internal/processkind"
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
func seedLivenessSession(t *testing.T, db *sql.DB, sessionID string, processID int64) int64 {
	t.Helper()
	var pid any
	if processID != 0 {
		pid = processID
	}
	res, err := db.Exec(
		`INSERT INTO sessions (session_id, project_id, platform, state, process_id, last_activity)
		 VALUES (?, 1, 'claude', 'working', ?, '2026-08-09T00:00:00')`,
		sessionID, pid,
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
			id := seedLivenessSession(t, db, "sess-"+tc.name, pid)

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

// TestLiveness_PanelessSessionIsUnboundNotDead pins the case D5 of the previous
// plan got wrong: a session with process_id NULL has no binding to judge, and a
// rule that reads "no binding" as "pane gone" condemns it on no evidence.
//
// The original name was BackgroundAgentIsUnboundNotDead — background agents had
// process_id NULL by design, so they were the population this protected. E-2074
// removed them, and the invariant outlived them: a row is paneless in the gap
// before its first hook records a process, and after E-1898 a migration-backfilled
// binding can be NULL too. "Unprovable" must never collapse to "dead".
func TestLiveness_PanelessSessionIsUnboundNotDead(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	defer SetTestTmuxObservation("srv", map[string]string{"%1": "2.1.220"})()

	id := seedLivenessSession(t, db, "sess-unbound", 0)

	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	got, err := SessionLiveness(id)
	if err != nil {
		t.Fatalf("SessionLiveness: %v", err)
	}
	if got != LivenessUnbound {
		t.Errorf("paneless session liveness = %q, want %q", got, LivenessUnbound)
	}
	if got.IsGone() {
		t.Error("paneless session read as gone; every unbound row would be treated as dead")
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
	id := seedLivenessSession(t, db, "sess-suspended", pid)

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
		ids = append(ids, seedLivenessSession(t, db, "sess-"+pane+string(rune('a'+i)), pid))
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
	id := seedLivenessSession(t, db, "sess-notmux", pid)

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

// ── duplicate observations (regression, 2026-08-13) ─────────────────────────

// TestLiveness_DuplicatePaneYieldsOneRow is the regression test for the defect
// that shipped with E-1898 and surfaced hours after land.
//
// `tmux list-panes -a` lists a LINKED window's panes once per tmux session the
// window is linked into — 442 rows for 245 distinct panes on the machine where
// it bit. live_processes had no uniqueness constraint, session_liveness
// LEFT JOINs it, so a pane recorded twice returned every one of its sessions
// twice: `list-live` gave 114 rows for 57 sessions and `esu` refused to resolve
// a sibling pane at all.
//
// It INSERTS the duplicate DIRECTLY rather than going through
// SetTestTmuxObservation, and that is the whole point. That seam takes a
// map[string]string of address -> command, which cannot hold the same address
// twice, so every existing fixture was structurally incapable of reproducing
// this. A test written through the seam would restate the blind spot instead of
// closing it.
func TestLiveness_DuplicatePaneYieldsOneRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if err := EnsureLivenessTables(db); err != nil {
		t.Fatalf("EnsureLivenessTables: %v", err)
	}

	pid := mustSeedPane(t, db, "srv", "%1")
	id := seedLivenessSession(t, db, "sess-linked", pid)

	if _, err := db.Exec(
		`INSERT OR IGNORE INTO observed_servers (kind_id, server_uuid) VALUES (?, ?)`,
		int(processkind.ProcessKindTmux), "srv",
	); err != nil {
		t.Fatalf("seed observed server: %v", err)
	}
	// The same pane, twice — what a linked window actually produces.
	for i := 0; i < 2; i++ {
		if _, err := db.Exec(
			`INSERT OR IGNORE INTO live_processes (kind_id, server_uuid, address, command)
			 VALUES (?, ?, ?, ?)`,
			int(processkind.ProcessKindTmux), "srv", "%1", "2.1.220",
		); err != nil {
			t.Fatalf("seed duplicate pane %d: %v", i, err)
		}
	}

	var observed int
	if err := db.QueryRow(
		`SELECT count(*) FROM live_processes WHERE address = '%1'`,
	).Scan(&observed); err != nil {
		t.Fatalf("count live_processes: %v", err)
	}
	if observed != 1 {
		t.Errorf("live_processes holds %d rows for one pane, want 1", observed)
	}

	var rows int
	if err := db.QueryRow(
		`SELECT count(*) FROM session_liveness WHERE session_id = ?`, id,
	).Scan(&rows); err != nil {
		t.Fatalf("count session_liveness: %v", err)
	}
	if rows != 1 {
		t.Errorf("session_liveness returned %d rows for one session, want 1 — "+
			"a duplicate observation multiplied the join", rows)
	}
}

// TestLiveness_DuplicateServerYieldsOneRow covers the sibling table. Only one
// socket answers @server_uuid today, so this guards a gap rather than closing an
// observed bug — which is exactly why it is here: observed_servers had the
// identical missing constraint and merely happened not to be exercised.
func TestLiveness_DuplicateServerYieldsOneRow(t *testing.T) {
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	if err := EnsureLivenessTables(db); err != nil {
		t.Fatalf("EnsureLivenessTables: %v", err)
	}

	pid := mustSeedPane(t, db, "srv", "%2")
	id := seedLivenessSession(t, db, "sess-dupsrv", pid)

	for i := 0; i < 3; i++ {
		if _, err := db.Exec(
			`INSERT OR IGNORE INTO observed_servers (kind_id, server_uuid) VALUES (?, ?)`,
			int(processkind.ProcessKindTmux), "srv",
		); err != nil {
			t.Fatalf("seed observed server %d: %v", i, err)
		}
	}
	if _, err := db.Exec(
		`INSERT OR IGNORE INTO live_processes (kind_id, server_uuid, address, command)
		 VALUES (?, ?, ?, ?)`,
		int(processkind.ProcessKindTmux), "srv", "%2", "2.1.220",
	); err != nil {
		t.Fatalf("seed pane: %v", err)
	}

	var rows int
	if err := db.QueryRow(
		`SELECT count(*) FROM session_liveness WHERE session_id = ?`, id,
	).Scan(&rows); err != nil {
		t.Fatalf("count session_liveness: %v", err)
	}
	if rows != 1 {
		t.Errorf("session_liveness returned %d rows, want 1", rows)
	}
}
