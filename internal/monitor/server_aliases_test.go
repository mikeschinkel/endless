package monitor

import (
	"database/sql"
	"testing"
	"time"
)

// E-2428: an id a running tmux server carried before a re-stamp is recorded as
// an alias of its new id, and every server-id comparison accepts it.

const (
	aliasSocket   = "/endless-test/sockets/endless-tasks"
	restampedID   = "a6d29860" // the id the server carried before the re-stamp
	currentID     = "0ae20eaa" // the id it answers with now
	earlierServer = "earlier-server-id"
)

// aliasServerStart is when the running server process started.
var aliasServerStart = time.Date(2026, 10, 17, 0, 12, 57, 0, time.UTC)

// seedTmuxServerSeen records that uuid lived on socket, first seen at seen.
func seedTmuxServerSeen(t *testing.T, db *sql.DB, uuid, socket, seen string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tmux_servers (server_uuid, socket_path, first_seen_at) VALUES (?, ?, ?)`,
		uuid, socket, seen,
	); err != nil {
		t.Fatalf("seed tmux server %q: %v", uuid, err)
	}
}

// seedAlias records alias as an earlier id of server.
func seedAlias(t *testing.T, db *sql.DB, alias, server string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO tmux_server_aliases (alias_uuid, server_uuid, recorded_at)
		 VALUES (?, ?, '2026-10-24T00:00:00')`, alias, server,
	); err != nil {
		t.Fatalf("seed alias %q -> %q: %v", alias, server, err)
	}
}

// aliasesOf returns alias -> server for every recorded alias.
func aliasesOf(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT alias_uuid, server_uuid FROM tmux_server_aliases`)
	if err != nil {
		t.Fatalf("read aliases: %v", err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var a, s string
		if err := rows.Scan(&a, &s); err != nil {
			t.Fatalf("scan alias: %v", err)
		}
		got[a] = s
	}
	return got
}

// stubServerStart fixes the running server's socket and start time for
// recordTmuxServer. Call it AFTER SetTestTmux*, which resets both.
func stubServerStart(t *testing.T, socket string, started time.Time) {
	t.Helper()
	prevSocket, prevStart := tmuxSocketPathFn, tmuxStartTimeFn
	tmuxSocketPathFn = func() string { return socket }
	tmuxStartTimeFn = func() time.Time { return started }
	t.Cleanup(func() { tmuxSocketPathFn, tmuxStartTimeFn = prevSocket, prevStart })
}

// TestRecordTmuxServer_AliasesIDsOfTheRunningServer covers P4 and P5: recording
// a new id makes every other id on its socket first seen at or after the
// server's start an alias of it, and leaves ids first seen before it alone.
func TestRecordTmuxServer_AliasesIDsOfTheRunningServer(t *testing.T) {
	db := withTestDB(t)
	stubServerStart(t, aliasSocket, aliasServerStart)

	seedTmuxServerSeen(t, db, earlierServer, aliasSocket, "2026-10-16T09:00:00")
	seedTmuxServerSeen(t, db, "first-of-this-server", aliasSocket, "2026-10-17T00:12:57")
	seedTmuxServerSeen(t, db, restampedID, aliasSocket, "2026-10-17T00:13:10")
	seedTmuxServerSeen(t, db, "on-another-socket", "/endless-test/sockets/elsewhere", "2026-10-18T00:00:00")
	// An alias recorded at an earlier re-stamp is re-pointed at the newest id.
	seedAlias(t, db, "first-of-this-server", restampedID)

	recordTmuxServer(currentID)

	got := aliasesOf(t, db)
	want := map[string]string{
		"first-of-this-server": currentID,
		restampedID:            currentID,
	}
	if len(got) != len(want) {
		t.Errorf("aliases = %v, want %v", got, want)
	}
	for a, s := range want {
		if got[a] != s {
			t.Errorf("alias %q -> %q, want %q", a, got[a], s)
		}
	}
	if _, ok := got[earlierServer]; ok {
		t.Errorf("an id first seen before the server started was aliased: %v", got)
	}
}

// TestRecordTmuxServer_NoStartTimeNoAliases: with no start time there is no
// evidence two ids share a server, so none is recorded.
func TestRecordTmuxServer_NoStartTimeNoAliases(t *testing.T) {
	db := withTestDB(t)
	stubServerStart(t, aliasSocket, time.Time{})
	seedTmuxServerSeen(t, db, restampedID, aliasSocket, "2026-10-17T00:13:10")

	recordTmuxServer(currentID)

	if got := aliasesOf(t, db); len(got) != 0 {
		t.Errorf("aliases = %v, want none", got)
	}
}

// aliasFixture: the running server answers currentID and hosts %22; a session
// is bound to %22 under restampedID, both ids recorded on one socket, and that
// socket answers currentID when probed.
func aliasFixture(t *testing.T, aliased bool) (db *sql.DB, session int64) {
	t.Helper()
	db = withTestDB(t)
	seedProject(t, db, 1, "acme", "/tmp/acme")
	t.Cleanup(SetTestTmuxObservation(currentID, map[string]string{"%22": "2.1.220"}))
	stubSocketProbes(t, map[string]socketProbe{aliasSocket: {answered: true, uuid: currentID}})
	seedTmuxServerSeen(t, db, restampedID, aliasSocket, "2026-10-17T00:13:10")
	seedTmuxServerSeen(t, db, currentID, aliasSocket, "2026-10-19T03:57:00")
	if aliased {
		seedAlias(t, db, restampedID, currentID)
	}
	session = seedLivenessSession(t, db, "sess-restamped", mustSeedPane(t, db, restampedID, "%22"))
	return db, session
}

// TestLiveness_SessionBoundUnderAliasReadsLive covers P7 for liveness, session
// status and session goto (both read ListLiveSessions): without the alias the
// session reads dead, which is the 2026-10-19 bug; with it, live.
func TestLiveness_SessionBoundUnderAliasReadsLive(t *testing.T) {
	for _, tc := range []struct {
		aliased bool
		want    Liveness
	}{
		{aliased: false, want: LivenessDead},
		{aliased: true, want: LivenessLive},
	} {
		t.Run(map[bool]string{false: "no_alias", true: "alias"}[tc.aliased], func(t *testing.T) {
			_, id := aliasFixture(t, tc.aliased)

			got, err := SessionLiveness(id)
			if err != nil {
				t.Fatalf("SessionLiveness: %v", err)
			}
			if got != tc.want {
				t.Errorf("liveness = %q, want %q", got, tc.want)
			}

			live, err := ListLiveSessions(1)
			if err != nil {
				t.Fatalf("ListLiveSessions: %v", err)
			}
			var found *LiveSession
			for i := range live {
				if live[i].EndlessSessionID == id {
					found = &live[i]
				}
			}
			if !tc.aliased {
				if found != nil {
					t.Errorf("session on a re-stamped server listed without an alias: %+v", *found)
				}
				return
			}
			if found == nil {
				t.Fatalf("session bound under an alias is not listed: %+v", live)
			}
			if found.PaneID == nil || *found.PaneID != "%22" {
				t.Errorf("PaneID = %v, want %%22 so session goto can reach it", found.PaneID)
			}
		})
	}
}

// TestProcessIDsForPanes_AcceptsAlias: a pane bound under an alias of the
// running server's id resolves to its process.
func TestProcessIDsForPanes_AcceptsAlias(t *testing.T) {
	db, _ := aliasFixture(t, true)
	want := mustSeedPane(t, db, restampedID, "%22")

	got, err := ProcessIDsForPanes([]string{"%22"})
	if err != nil {
		t.Fatalf("ProcessIDsForPanes: %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Errorf("ProcessIDsForPanes = %v, want [%d]", got, want)
	}
}

// TestLiveness_AliasOnProbedSocket covers P8: for a recorded server that was
// not observed, a socket answering with the id it is an alias of proves
// nothing, and one answering with any other id still proves it gone.
func TestLiveness_AliasOnProbedSocket(t *testing.T) {
	for _, tc := range []struct {
		name   string
		answer string
		want   Liveness
	}{
		{name: "answers with the id it is an alias of", answer: currentID, want: LivenessUnknown},
		{name: "answers with neither id", answer: "a-stranger", want: LivenessDead},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := goneFixture(t) // the observed server is stewServer, not ours
			stubSocketProbes(t, map[string]socketProbe{aliasSocket: {answered: true, uuid: tc.answer}})
			seedTmuxServerSeen(t, db, restampedID, aliasSocket, "2026-10-17T00:13:10")
			seedTmuxServerSeen(t, db, currentID, aliasSocket, "2026-10-19T03:57:00")
			seedAlias(t, db, restampedID, currentID)
			id := seedLivenessSession(t, db, "sess-restamped", mustSeedPane(t, db, restampedID, "%22"))

			if err := RefreshLiveness(); err != nil {
				t.Fatalf("RefreshLiveness: %v", err)
			}
			got, err := SessionLiveness(id)
			if err != nil {
				t.Fatalf("SessionLiveness: %v", err)
			}
			if got != tc.want {
				t.Errorf("liveness = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSameTmuxServer pins the one same-server test.
func TestSameTmuxServer(t *testing.T) {
	db := withTestDB(t)
	seedAlias(t, db, restampedID, currentID)
	for _, tc := range []struct {
		a, b string
		want bool
	}{
		{currentID, currentID, true},
		{restampedID, currentID, true},
		{currentID, restampedID, true},
		{"a-stranger", currentID, false},
		{earlierServer, restampedID, false},
	} {
		got, err := SameTmuxServer(db, tc.a, tc.b)
		if err != nil {
			t.Fatalf("SameTmuxServer: %v", err)
		}
		if got != tc.want {
			t.Errorf("SameTmuxServer(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
