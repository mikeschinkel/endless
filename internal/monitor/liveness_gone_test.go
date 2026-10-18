package monitor

import (
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// E-2300: a session bound to a tmux server that is gone reads `dead`, judged
// only by asking the socket that server recorded. Every test here stubs the
// prober, except TestProbeTmuxSocket_FileVerdicts, which uses socket files it
// made itself and never execs tmux.

const (
	goneSocket  = "/endless-test/sockets/endless-tasks"
	goneServer  = "server-that-exited"
	otherSocket = "/endless-test/sockets/elsewhere"
)

// seedTmuxServer records that uuid lived on socket.
func seedTmuxServer(t *testing.T, db *sql.DB, uuid, socket string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tmux_servers (server_uuid, socket_path, first_seen_at)
		 VALUES (?, ?, '2026-10-18T00:00:00')`, uuid, socket,
	); err != nil {
		t.Fatalf("seed tmux server %q: %v", uuid, err)
	}
}

// stubSocketProbes answers each probed socket from probes (a socket absent from
// it gets no verdict) and returns the number of probes made per socket. Call it
// AFTER SetTestTmux*, which resets the prober.
func stubSocketProbes(t *testing.T, probes map[string]socketProbe) map[string]int {
	t.Helper()
	calls := map[string]int{}
	prev := probeSocket
	probeSocket = func(socket string) socketProbe {
		calls[socket]++
		return probes[socket]
	}
	t.Cleanup(func() { probeSocket = prev })
	return calls
}

// goneFixture: one reachable server, stewServer, hosting %1..%9.
func goneFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	t.Cleanup(SetTestTmuxObservation(stewServer, map[string]string{"%1": "2.1.220"}))
	return db
}

// bindSession binds session id to pane on server.
func bindSession(t *testing.T, db *sql.DB, id int64, server, pane string) {
	t.Helper()
	pid := mustSeedPane(t, db, server, pane)
	if _, err := db.Exec(`UPDATE sessions SET process_id = ? WHERE id = ?`, pid, id); err != nil {
		t.Fatalf("bind session %d: %v", id, err)
	}
}

func TestLiveness_GoneServerTruthTable(t *testing.T) {
	tests := []struct {
		name     string
		recorded bool // a tmux_servers row for goneServer
		probe    socketProbe
		want     Liveness
		why      string
	}{
		{
			name: "another server answers the recorded socket", recorded: true,
			probe: socketProbe{answered: true, uuid: "a-newer-server"},
			want:  LivenessDead,
			why:   "a socket hosts one server; someone else there means ours is gone",
		},
		{
			name: "a server with no uuid answers", recorded: true,
			probe: socketProbe{answered: true},
			want:  LivenessDead,
			why:   "ours had an id in memory, and a running server does not lose it",
		},
		{
			name: "socket file exists, connection refused", recorded: true,
			probe: socketProbe{refused: true},
			want:  LivenessDead,
			why:   "nothing is listening where our server listened",
		},
		{
			name: "socket file missing, or the probe failed", recorded: true,
			probe: socketProbe{},
			want:  LivenessUnknown,
			why:   "a server can outlive its socket file (macOS /tmp cleanup)",
		},
		{
			name: "our own server answers, though not observed", recorded: true,
			probe: socketProbe{answered: true, uuid: goneServer},
			want:  LivenessUnknown,
			why:   "it is there; we just could not read its panes",
		},
		{
			name: "no recorded socket (bound before E-2300)", recorded: false,
			probe: socketProbe{refused: true},
			want:  LivenessUnknown,
			why:   "with no socket to ask, absence proves nothing",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := goneFixture(t)
			if tc.recorded {
				seedTmuxServer(t, db, goneServer, goneSocket)
			}
			stubSocketProbes(t, map[string]socketProbe{goneSocket: tc.probe})
			id := seedLivenessSession(t, db, "sess-gone", mustSeedPane(t, db, goneServer, "%1"))

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

// An observed server is judged by its panes, never probed — even with a
// recorded socket that a probe would say is someone else's.
func TestLiveness_ObservedServerIsNeverProbed(t *testing.T) {
	db := goneFixture(t)
	seedTmuxServer(t, db, stewServer, goneSocket)
	calls := stubSocketProbes(t, map[string]socketProbe{goneSocket: {refused: true}})
	live := seedLivenessSession(t, db, "sess-live", mustSeedPane(t, db, stewServer, "%1"))
	dead := seedLivenessSession(t, db, "sess-dead", mustSeedPane(t, db, stewServer, "%404"))

	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	for id, want := range map[int64]Liveness{live: LivenessLive, dead: LivenessDead} {
		if got, _ := SessionLiveness(id); got != want {
			t.Errorf("session %d: liveness = %q, want %q", id, got, want)
		}
	}
	if calls[goneSocket] != 0 {
		t.Errorf("probed an observed server's socket %d times, want 0", calls[goneSocket])
	}
}

// Every restart of the task-session server records the same fixed path: one
// probe there judges all of them.
func TestLiveness_ProbesEachSocketOnce(t *testing.T) {
	db := goneFixture(t)
	for _, uuid := range []string{"restart-1", "restart-2", "restart-3"} {
		seedTmuxServer(t, db, uuid, goneSocket)
		seedLivenessSession(t, db, "sess-"+uuid, mustSeedPane(t, db, uuid, "%1"))
	}
	calls := stubSocketProbes(t, map[string]socketProbe{
		goneSocket: {answered: true, uuid: "restart-4"},
	})
	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	if calls[goneSocket] != 1 {
		t.Errorf("probes of %s = %d, want 1", goneSocket, calls[goneSocket])
	}
	var dead int
	if err := db.QueryRow(
		`SELECT count(*) FROM session_liveness WHERE liveness = 'dead'`,
	).Scan(&dead); err != nil {
		t.Fatalf("count dead: %v", err)
	}
	if dead != 3 {
		t.Errorf("dead sessions = %d, want 3", dead)
	}
}

// A verdict is a read, never a write (E-1898's rule): judging a server gone
// leaves sessions exactly as it found them.
func TestLiveness_GoneVerdictWritesNothing(t *testing.T) {
	db := goneFixture(t)
	seedTmuxServer(t, db, goneServer, goneSocket)
	stubSocketProbes(t, map[string]socketProbe{goneSocket: {refused: true}})
	id := seedLivenessSession(t, db, "sess-gone", mustSeedPane(t, db, goneServer, "%1"))

	before := snapshotSessionsTable(t, db)
	if err := RefreshLiveness(); err != nil {
		t.Fatalf("RefreshLiveness: %v", err)
	}
	if got, _ := SessionLiveness(id); got != LivenessDead {
		t.Fatalf("liveness = %q, want dead (the fixture must actually judge)", got)
	}
	if after := snapshotSessionsTable(t, db); after != before {
		t.Errorf("sessions changed under a liveness verdict")
	}
}

// The prober's file-level verdicts, on socket files this test made. The
// "answered" verdict needs tmux and is covered through the stub above.
func TestProbeTmuxSocket_FileVerdicts(t *testing.T) {
	// A short directory: a unix socket path is limited to ~104 bytes, which
	// t.TempDir() can exceed on macOS.
	dir, err := os.MkdirTemp("", "e2300")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	if got := probeTmuxSocket(filepath.Join(dir, "missing")); got != (socketProbe{}) {
		t.Errorf("missing file: %+v, want no verdict", got)
	}

	plain := filepath.Join(dir, "plain")
	if err := os.WriteFile(plain, nil, 0o600); err != nil {
		t.Fatalf("write plain file: %v", err)
	}
	if got := probeTmuxSocket(plain); got != (socketProbe{}) {
		t.Errorf("not a socket: %+v, want no verdict", got)
	}

	stale := filepath.Join(dir, "stale")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	if got := probeTmuxSocket(stale); got != (socketProbe{refused: true}) {
		t.Errorf("socket file with no listener: %+v, want refused", got)
	}
}

func TestEnsureTmuxPaneProcess_RecordsSocketPath(t *testing.T) {
	db := withTestDB(t)
	asks := 0
	socket := "/endless-test/sockets/first"
	prev := tmuxSocketPathFn
	tmuxSocketPathFn = func() string { asks++; return socket }
	t.Cleanup(func() { tmuxSocketPathFn = prev })

	if _, err := EnsureTmuxPaneProcess("%7"); err != nil {
		t.Fatalf("EnsureTmuxPaneProcess: %v", err)
	}
	// A uuid names one server lifetime: a later answer never rewrites it, and a
	// recorded server is not asked again.
	socket = "/endless-test/sockets/second"
	if _, err := EnsureTmuxPaneProcess("%8"); err != nil {
		t.Fatalf("EnsureTmuxPaneProcess: %v", err)
	}

	var got string
	if err := db.QueryRow(
		`SELECT socket_path FROM tmux_servers WHERE server_uuid = ?`, TestServerUUID,
	).Scan(&got); err != nil {
		t.Fatalf("read tmux_servers: %v", err)
	}
	if got != "/endless-test/sockets/first" {
		t.Errorf("socket_path = %q, want the first recorded path", got)
	}
	if asks != 1 {
		t.Errorf("asked tmux for the socket %d times, want 1", asks)
	}
}

func TestEnsureTmuxPaneProcess_SkipsRelativeSocket(t *testing.T) {
	db := withTestDB(t)
	prev := tmuxSocketPathFn
	tmuxSocketPathFn = func() string { return "./s" }
	t.Cleanup(func() { tmuxSocketPathFn = prev })

	if _, err := EnsureTmuxPaneProcess("%7"); err != nil {
		t.Fatalf("EnsureTmuxPaneProcess: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM tmux_servers`).Scan(&n); err != nil {
		t.Fatalf("count tmux_servers: %v", err)
	}
	if n != 0 {
		t.Errorf("recorded %d servers for a relative socket, want 0", n)
	}
}

// ── one presence test everywhere (P6, P7) ───────────────────────────────────

// bindOwner seeds a working session on taskID (0 = none), bound to a server
// proven gone when gone is set, else to one that is merely unreachable.
func bindOwner(t *testing.T, db *sql.DB, sessionID, taskID int64, gone bool) {
	t.Helper()
	server := "unreachable-server"
	if gone {
		server = goneServer
	}
	snSession(t, db, sessionID, 1, taskID, "working")
	bindSession(t, db, sessionID, server, "%1")
}

// readersFixture: goneServer's recorded socket refuses, so it is gone;
// unreachable-server's socket gives no verdict, so it stays unknown.
func goneName(gone bool) string {
	if gone {
		return "gone server"
	}
	return "unreachable server"
}

func readersFixture(t *testing.T) *sql.DB {
	t.Helper()
	db := goneFixture(t)
	seedTmuxServer(t, db, goneServer, goneSocket)
	seedTmuxServer(t, db, "unreachable-server", otherSocket)
	stubSocketProbes(t, map[string]socketProbe{goneSocket: {refused: true}})
	return db
}

func TestWorktreeInUse_IgnoresSessionOnGoneServer(t *testing.T) {
	for _, gone := range []bool{true, false} {
		t.Run(goneName(gone), func(t *testing.T) {
			db := readersFixture(t)
			installLiveStub(t, false, nil)
			snTask(t, db, 50, 1, "underway", "now", "")
			bindOwner(t, db, 9, 50, gone)

			inUse, reason, err := WorktreeInUse(db, t.TempDir(), 50)
			if err != nil {
				t.Fatalf("WorktreeInUse: %v", err)
			}
			if inUse == gone {
				t.Errorf("gone=%v: inUse=%v (%q), want %v", gone, inUse, reason, !gone)
			}
		})
	}
}

func TestSessionStatusRows_InFlightIgnoresSessionOnGoneServer(t *testing.T) {
	for _, gone := range []bool{true, false} {
		t.Run(goneName(gone), func(t *testing.T) {
			db := readersFixture(t)
			const focal, blocker, emitter = 60, 61, 963
			snTask(t, db, focal, 1, "ready", "now", "")
			snTask(t, db, blocker, 1, "underway", "now", "")
			snBlocks(t, db, blocker, focal)
			bindOwner(t, db, 9, blocker, gone)
			snSession(t, db, emitter, 1, 0, "working")
			snSessionTaskRel(t, db, emitter, blocker, 3)

			rows, err := SessionStatusRows(focal, 0, false)
			if err != nil {
				t.Fatalf("SessionStatusRows: %v", err)
			}
			if r, ok := snRowByID(rows, blocker); !ok || r.InFlight == gone {
				t.Errorf("focal view, gone=%v: blocker row %+v (listed %v), want InFlight=%v", gone, r, ok, !gone)
			}

			rows, err = SessionStatusRowsForSession(emitter, false)
			if err != nil {
				t.Fatalf("SessionStatusRowsForSession: %v", err)
			}
			if r, ok := snRowByID(rows, blocker); !ok || r.InFlight == gone {
				t.Errorf("session view, gone=%v: row %+v (listed %v), want InFlight=%v", gone, r, ok, !gone)
			}
		})
	}
}

func TestSessionGraphData_InFlightIgnoresSessionOnGoneServer(t *testing.T) {
	for _, gone := range []bool{true, false} {
		t.Run(goneName(gone), func(t *testing.T) {
			db := readersFixture(t)
			snTask(t, db, 70, 1, "underway", "now", "")
			bindOwner(t, db, 9, 70, gone)

			got, err := SessionGraphData([]int64{70}, 0)
			if err != nil {
				t.Fatalf("SessionGraphData: %v", err)
			}
			if got.Nodes[70].InFlight == gone {
				t.Errorf("gone=%v: InFlight=%v, want %v", gone, got.Nodes[70].InFlight, !gone)
			}
		})
	}
}

func TestListLiveSessions_OmitsSessionOnGoneServer(t *testing.T) {
	db := readersFixture(t)
	gone := seedLivenessSession(t, db, "sess-gone", mustSeedPane(t, db, goneServer, "%1"))
	unreachable := seedLivenessSession(t, db, "sess-unreachable", mustSeedPane(t, db, "unreachable-server", "%1"))

	got, err := ListLiveSessions(1)
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	ids := map[int64]string{}
	for _, s := range got {
		ids[s.EndlessSessionID] = s.Liveness
	}
	if _, ok := ids[gone]; ok {
		t.Errorf("session on a gone server is listed as live: %v", ids)
	}
	if ids[unreachable] != string(LivenessUnknown) {
		t.Errorf("session on an unreachable server: %v, want listed as unknown", ids)
	}
}

// An `unknown` session on a recorded socket says whether that is Endless's own
// server and down, so the claim refusal can name `endless start`. A session
// with no recorded socket never asks.
func TestListLiveSessions_ReportsEndlessServerDown(t *testing.T) {
	db := readersFixture(t)
	recorded := seedLivenessSession(t, db, "sess-recorded", mustSeedPane(t, db, "unreachable-server", "%1"))
	unrecorded := seedLivenessSession(t, db, "sess-unrecorded", mustSeedPane(t, db, "never-recorded", "%1"))
	var asked []string
	prev := endlessServerDown
	endlessServerDown = func(socket string) bool { asked = append(asked, socket); return true }
	t.Cleanup(func() { endlessServerDown = prev })

	got, err := ListLiveSessions(1)
	if err != nil {
		t.Fatalf("ListLiveSessions: %v", err)
	}
	down := map[int64]bool{}
	for _, s := range got {
		down[s.EndlessSessionID] = s.EndlessServerDown
	}
	if !down[recorded] || down[unrecorded] {
		t.Errorf("endless_server_down = %v, want true for %d only", down, recorded)
	}
	if len(asked) != 1 || asked[0] != otherSocket {
		t.Errorf("asked about %v, want only %s", asked, otherSocket)
	}
}

func TestSameSocketPath(t *testing.T) {
	if !sameSocketPath("/tmp/endless-501/endless-tasks", "/tmp/endless-501//endless-tasks") {
		t.Errorf("an uncleaned spelling should match")
	}
	if sameSocketPath("/tmp/endless-501/endless-tasks", "/tmp/endless-501/endless-console") {
		t.Errorf("different sockets should not match")
	}
}
