package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mikeschinkel/endless/internal/processkind"
	"github.com/mikeschinkel/endless/internal/tmuxservers"
)

// liveness.go derives whether a session is running, WITHOUT storing the answer.
//
// # Why this file exists
//
// Until E-1898, liveness was a stored column maintained by a sweep: a "reaper"
// looked at tmux, concluded some sessions were dead, and wrote that conclusion
// into `sessions`. On 2026-08-05 it looked at the wrong tmux server — one it
// inferred from an ambient $TMUX it never validated — decided all 61 sessions
// were dead, and nulled 59 live pane bindings in a single UPDATE. The rows then
// read alive with no pane, permanently.
//
// The defect was not the sweep's judgment. It was that a judgment about volatile
// external state was WRITTEN AT ALL. So liveness is now computed at read time
// and never persisted:
//
//	observation (this file, per invocation)  JOIN  identity (processes table)
//
// A wrong observation now produces a briefly-wrong READ that self-corrects on
// the next refresh. It cannot destroy a binding, because no code path writes
// one in response to an observation.
//
// # The four states
//
//	live     — the address was present on a server we actually reached
//	dead     — we reached its server, and the address was absent; or its
//	           server is proven gone (see "Gone servers" below)
//	unknown  — we could NOT reach its server; we have no opinion
//	unbound  — the session has no process binding at all (background agents,
//	           and any session started outside a multiplexer)
//
// `unknown` is the load-bearing one. An unreachable server (a socket outside the
// enumerated directory, a relocated TMUX_TMPDIR, tmux not installed) yields
// `unknown` for its sessions, NOT `dead`. That is what bounds the blast radius
// of a failed observation to zero rows: no consumer may treat `unknown` as
// `dead`. The site that matters most is the spawn/claim ownership guard, which
// must read `unknown` as STILL LIVE and refuse — a wrong refusal is an
// annoyance, the inverse hands someone else a live worktree.
//
// `unbound` exists so a background agent, whose `process_id` is legitimately
// NULL, is never mistaken for a session whose pane vanished.
//
// # Gone servers (E-2300)
//
// `unknown` used to be forever for a session whose server had exited: nothing
// told "unreachable right now" from "gone for good", so a ghost from before a
// tmux restart counted as live in every guard that reads `unknown` as live.
// Enumerating sockets cannot settle it — a uuid absent from every socket we
// listed is exactly the "not found where we looked" judgment of 2026-08-05.
//
// So each binding's server records its socket path (tmux_servers, written by
// EnsureTmuxPaneProcess), and for a recorded server we did not observe, we ask
// THAT socket directly:
//
//	a different @server_uuid answers, or none   → gone (`dead`)
//	the socket file exists, nothing listening   → gone (`dead`)
//	the socket file is missing                  → no verdict (`unknown`)
//	anything else (timeout, permission)         → no verdict (`unknown`)
//
// A missing file is no verdict because a server outlives its socket file when
// macOS cleans /tmp; judging it gone would hand a running session's worktree
// to someone else. A server with no tmux_servers row — every binding from
// before E-2300 — is never probed, so never judged gone. Gone servers are
// observation like the rest: a TEMP table, rebuilt per refresh, never written
// to sessions.
//
// # What is deliberately NOT here
//
// There is no "the pane is running a shell, so the harness must have died" test.
// It reads plausible and it is wrong about live sessions: Ctrl+Z puts the shell
// back in the foreground, so a suspended-but-alive Claude reports `zsh` and
// would be declared dead — dropping its status line and freeing its task to be
// claimed out from under it. `live_processes.command` IS collected, but only the
// cosmetic "pane runs Claude but has no session row" hint may read it. An
// inference is acceptable for a hint that owns nothing; it is not acceptable for
// liveness. Liveness asks one question with an observable answer: is this
// address present on a server we reached?
//
// # Connection scoping
//
// The observation tables are TEMP, so they live on one SQLite connection.
// monitor.DB() sets SetMaxOpenConns(1) (db.go:495), so the pool IS that one
// connection and every later query sees them. TestLiveness_PoolIsSingleConn
// pins that dependency: raising MaxOpenConns would otherwise break liveness
// intermittently and invisibly.

// Liveness classifies a session's observed run state. String values match the
// `liveness` column of the session_liveness view so callers can compare against
// what SQL returns without a translation layer.
type Liveness string

const (
	// LivenessLive — address present on a server we reached.
	LivenessLive Liveness = "live"
	// LivenessDead — we reached the server and the address was absent, or the
	// server's recorded socket proves it gone (E-2300). The ONLY state that may
	// be read as "this session is over".
	LivenessDead Liveness = "dead"
	// LivenessUnknown — the server was not reached. No opinion. Never `dead`.
	LivenessUnknown Liveness = "unknown"
	// LivenessUnbound — no process binding (background agent, non-multiplexer).
	LivenessUnbound Liveness = "unbound"
)

// IsGone reports whether this state may be treated as "the session is over".
// ONLY `dead` qualifies. Every consumer that has to decide something
// consequential — freeing a task, treating its owner as gone — calls this rather than
// comparing strings, so the `unknown != dead` rule is enforced in one place
// instead of re-derived (correctly or not) at each site.
func (l Liveness) IsGone() bool {
	return l == LivenessDead
}

const (
	// createLiveProcesses is the per-invocation observation: every address we
	// saw, on every server we reached. `command` is for the status-line hint
	// ONLY — see the file header on why it must not reach liveness.
	createLiveProcesses = `CREATE TEMP TABLE IF NOT EXISTS live_processes (
		kind_id     INTEGER NOT NULL,
		server_uuid TEXT,
		address     TEXT NOT NULL,
		command     TEXT
	)`

	// An observation table states the SAME identity as the durable one, so it
	// carries the same constraint. Omitting it was a real defect: `tmux
	// list-panes -a` lists a LINKED window's panes once per tmux session the
	// window is linked into (measured 2026-08-13: 442 rows for 245 distinct
	// panes, every pane twice), and session_liveness LEFT JOINs this table, so a
	// pane recorded N times multiplied its session's row N times. `list-live`
	// returned 114 rows for 57 sessions and `esu` refused to resolve a sibling
	// pane at all.
	//
	// ifnull() for the same reason processes_identity uses it: SQLite treats
	// every NULL as distinct, so a plain UNIQUE would not bind for the
	// NULL-server rows. A pane seen twice in one snapshot is one pane.
	createLiveProcessesIdentity = `CREATE UNIQUE INDEX IF NOT EXISTS live_processes_identity
		ON live_processes (kind_id, ifnull(server_uuid, ''), address)`

	// createObservedServers records WHICH servers the snapshot actually reached.
	// Without it, "no live_processes row" is ambiguous between "the pane is gone"
	// and "we never looked", and collapsing those two is exactly the 2026-08-05
	// incident. This table is what makes `unknown` expressible.
	createObservedServers = `CREATE TEMP TABLE IF NOT EXISTS observed_servers (
		kind_id     INTEGER NOT NULL,
		server_uuid TEXT
	)`

	// Same rule, same reason. observed_servers is joined by session_liveness too,
	// so a server recorded twice would multiply rows exactly as a duplicate pane
	// did. Only one socket answers @server_uuid today, so this is prevention
	// rather than a fix — which is the point: the sibling table had the identical
	// gap and only one of them happened to be exercised.
	createObservedServersIdentity = `CREATE UNIQUE INDEX IF NOT EXISTS observed_servers_identity
		ON observed_servers (kind_id, ifnull(server_uuid, ''))`

	// createGoneServers records which servers this snapshot PROVED gone by
	// probing their recorded socket (E-2300). Never inferred from absence: a
	// server lands here only when its own socket answered as someone else, or
	// refused. server_uuid is NOT NULL because only a recorded server can be
	// probed, and a NULL binding must never match one.
	createGoneServers = `CREATE TEMP TABLE IF NOT EXISTS gone_servers (
		kind_id     INTEGER NOT NULL,
		server_uuid TEXT NOT NULL
	)`

	// The same row-multiplication guard as observed_servers_identity.
	createGoneServersIdentity = `CREATE UNIQUE INDEX IF NOT EXISTS gone_servers_identity
		ON gone_servers (kind_id, server_uuid)`
)

// createLivenessView is the single definition of liveness. Consumers SELECT
// from it; none re-derive the CASE. Its server comparisons go through
// SameTmuxServerSQL, which also carries the NULL handling.
var createLivenessView = `CREATE TEMP VIEW IF NOT EXISTS session_liveness AS
	SELECT
		s.id           AS session_id,
		s.process_id   AS process_id,
		p.kind_id      AS kind_id,
		p.server_uuid  AS server_uuid,
		p.address      AS address,
		l.command      AS command,
		CASE
			WHEN p.id IS NULL          THEN 'unbound'
			WHEN o.kind_id IS NOT NULL
			 AND l.address IS NULL     THEN 'dead'
			WHEN o.kind_id IS NOT NULL THEN 'live'
			WHEN g.kind_id IS NOT NULL THEN 'dead'
			ELSE                            'unknown'
		END AS liveness
	FROM sessions s
	LEFT JOIN processes p
		   ON p.id = s.process_id
	LEFT JOIN observed_servers o
		   ON o.kind_id = p.kind_id
		  AND ` + SameTmuxServerSQL("o.server_uuid", "p.server_uuid") + `
	LEFT JOIN gone_servers g
		   ON g.kind_id = p.kind_id
		  AND ` + SameTmuxServerSQL("g.server_uuid", "p.server_uuid") + `
	LEFT JOIN live_processes l
		   ON l.kind_id = p.kind_id
		  AND ` + SameTmuxServerSQL("l.server_uuid", "p.server_uuid") + `
		  AND l.address = p.address`

var (
	// livenessOnce gives a short-lived process (a CLI invocation, a hook) one
	// automatic observation on first read, so no caller can forget. Long-lived
	// processes (`endless serve`, `session monitor`) call RefreshLiveness
	// explicitly per request / per repaint — that, not process lifetime, is the
	// "unit of work" the snapshot is scoped to.
	livenessOnce = &sync.Once{}
	livenessErr  error

	// observeServers is the ONE seam through which this package looks at tmux.
	// Everything else — the view, the JOINs, the four states — is pure SQL over
	// tables, which is why the liveness truth table can be tested exhaustively
	// with no tmux server in existence (E-1898 invariant I4).
	observeServers = observeTmuxServers

	// probeSocket is the seam through which a recorded server's own socket is
	// asked who is there (E-2300). Tests stub it, so no test reaches a socket.
	probeSocket = probeTmuxSocket
)

// SetTestTmuxServer pins ONLY the server IDENTITY, leaving the observation
// empty so every session reads `unknown`.
//
// This is the default every monitor test gets (withTestDB applies it), and the
// split matters: identity is what write paths need to record a binding at all,
// while observation is what read paths judge with. Pinning identity without
// observation gives a fixture that can bind panes but has no opinion about
// whether they are alive — which is both the safe default and the pre-E-1898
// behavior, since `unknown` sessions stay listed.
//
// USE ONLY IN TESTS. It exists because `go test` inherits the developer's TMUX
// and TMUX_PANE when run from inside tmux; without this seam the write paths
// would stamp @server_uuid on the real tmux server and bind fixtures to real
// panes, making the suite both destructive and environment-dependent.
//
// Mutates package state, so tests using it must NOT call t.Parallel().
func SetTestTmuxServer(serverUUID string) (restore func()) {
	return setTestTmux(serverUUID, nil, false)
}

// SetTestTmuxObservation pins the server identity AND makes that server
// REACHABLE with the given panes (address -> pane_current_command), so its
// sessions resolve to `live` or `dead` rather than `unknown`.
//
// Pass an empty uuid for "no server reachable at all". Exported rather than
// package-private because cross-package tests (internal/events,
// internal/sessionquerycmd) need it and cannot import _test.go helpers — the
// same reasoning as SetTestDB.
func SetTestTmuxObservation(serverUUID string, panes map[string]string) (restore func()) {
	return setTestTmux(serverUUID, panes, serverUUID != "")
}

func setTestTmux(serverUUID string, panes map[string]string, reachable bool) (restore func()) {
	prevObserve, prevUUID, prevOnce := observeServers, tmuxServerUUIDFn, livenessOnce
	prevSocket, prevProbe, prevDown := tmuxSocketPathFn, probeSocket, endlessServerDown
	prevStart := tmuxStartTimeFn

	tmuxServerUUIDFn = func() (string, error) { return serverUUID, nil }
	// No socket is recorded and none is probed: a fixture server has no socket,
	// and a probe of a real path would judge real servers from a test.
	tmuxSocketPathFn = func() string { return "" }
	tmuxStartTimeFn = func() time.Time { return time.Time{} }
	probeSocket = func(string) socketProbe { return socketProbe{} }
	endlessServerDown = func(string) bool { return false }
	observeServers = func() []observedServer {
		if !reachable {
			return nil
		}
		srv := observedServer{uuid: serverUUID}
		for address, command := range panes {
			srv.panes = append(srv.panes, observedPane{address: address, command: command})
		}
		return []observedServer{srv}
	}
	// A pinned observation is a NEW observation; drop the once-guard so the next
	// read re-observes rather than reusing whatever a prior test left behind.
	livenessOnce = &sync.Once{}
	livenessErr = nil

	return func() {
		observeServers, tmuxServerUUIDFn, livenessOnce = prevObserve, prevUUID, prevOnce
		tmuxSocketPathFn, probeSocket, endlessServerDown = prevSocket, prevProbe, prevDown
		tmuxStartTimeFn = prevStart
		livenessErr = nil
	}
}

// EnsureLivenessTables creates the observation tables and the session_liveness
// view if absent. Idempotent.
//
// Exported for tests: seeding live_processes / observed_servers directly is how
// the liveness truth table is tested without a tmux server anywhere in sight.
// That is a deliberate design goal, not a convenience — the previous design
// could only be tested by standing up private tmux servers, which is why its
// edge cases went unexplored.
func EnsureLivenessTables(db *sql.DB) error {
	for _, stmt := range []string{
		createLiveProcesses, createLiveProcessesIdentity,
		createObservedServers, createObservedServersIdentity,
		createGoneServers, createGoneServersIdentity,
		createLivenessView,
	} {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("liveness: create observation tables: %w", err)
		}
	}
	return nil
}

// RefreshLiveness re-observes the world into the snapshot tables, replacing the
// previous observation. Call it once per unit of work: a CLI invocation, an HTTP
// request, a monitor repaint.
//
// It NEVER returns an error for "nothing observed". tmux missing, no server
// running, an unreadable socket directory — all leave the snapshot empty, which
// renders every session `unknown`, which condemns nothing. An error here means
// the DATABASE could not be written, which is a real failure.
func RefreshLiveness() error {
	db, err := DB()
	if err != nil {
		return err
	}
	return refreshLiveness(db)
}

func refreshLiveness(db *sql.DB) error {
	if err := EnsureLivenessTables(db); err != nil {
		return err
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("liveness: begin: %w", err)
	}
	defer tx.Rollback()

	// Replace wholesale rather than merge: a stale row from a previous
	// observation is indistinguishable from a fresh one, and "I saw this a
	// minute ago" is exactly the kind of half-truth this design removes.
	for _, stmt := range []string{
		`DELETE FROM live_processes`, `DELETE FROM observed_servers`, `DELETE FROM gone_servers`,
	} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("liveness: clear snapshot: %w", err)
		}
	}

	for _, srv := range observeServers() {
		if _, err = tx.Exec(
			`INSERT OR IGNORE INTO observed_servers (kind_id, server_uuid) VALUES (?, ?)`,
			int(processkind.ProcessKindTmux), srv.uuid,
		); err != nil {
			return fmt.Errorf("liveness: record observed server: %w", err)
		}
		for _, pane := range srv.panes {
			if _, err = tx.Exec(
				`INSERT OR IGNORE INTO live_processes (kind_id, server_uuid, address, command)
				 VALUES (?, ?, ?, ?)`,
				int(processkind.ProcessKindTmux), srv.uuid, pane.address, pane.command,
			); err != nil {
				return fmt.Errorf("liveness: record observed pane: %w", err)
			}
		}
	}

	gone, err := goneTmuxServers(tx)
	if err != nil {
		return err
	}
	for _, uuid := range gone {
		if _, err = tx.Exec(
			`INSERT OR IGNORE INTO gone_servers (kind_id, server_uuid) VALUES (?, ?)`,
			int(processkind.ProcessKindTmux), uuid,
		); err != nil {
			return fmt.Errorf("liveness: record gone server: %w", err)
		}
	}

	return tx.Commit()
}

// goneTmuxServers returns the recorded servers this snapshot did not observe
// whose own socket proves them gone. Runs after observed_servers is filled, so
// a server we reached is never probed: its panes already decide.
//
// Each socket is probed once however many servers recorded it — every restart
// of the task-session server records the same fixed path, and one answer there
// judges all of them.
func goneTmuxServers(tx *sql.Tx) ([]string, error) {
	rows, err := tx.Query(
		`SELECT ts.server_uuid, ts.socket_path
		   FROM tmux_servers ts
		  WHERE EXISTS (SELECT 1 FROM processes p
		                 WHERE p.kind_id = ?1
		                   AND `+SameTmuxServerSQL("p.server_uuid", "ts.server_uuid")+`)
		    AND NOT EXISTS (SELECT 1 FROM observed_servers o
		                     WHERE o.kind_id = ?1
		                       AND `+SameTmuxServerSQL("o.server_uuid", "ts.server_uuid")+`)`,
		int(processkind.ProcessKindTmux),
	)
	if err != nil {
		return nil, fmt.Errorf("liveness: read recorded servers: %w", err)
	}
	type recorded struct{ uuid, socket string }
	var candidates []recorded
	for rows.Next() {
		var r recorded
		if err = rows.Scan(&r.uuid, &r.socket); err != nil {
			rows.Close()
			return nil, fmt.Errorf("liveness: scan recorded server: %w", err)
		}
		candidates = append(candidates, r)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("liveness: read recorded servers: %w", err)
	}

	probes := map[string]socketProbe{}
	var gone []string
	for _, c := range candidates {
		probe, ok := probes[c.socket]
		if !ok {
			probe = probeSocket(c.socket)
			probes[c.socket] = probe
		}
		proved, perr := probe.proves(tx, c.uuid)
		if perr != nil {
			return nil, perr
		}
		if proved {
			gone = append(gone, c.uuid)
		}
	}
	return gone, nil
}

// livenessReady performs the one automatic observation for short-lived
// processes. Read helpers call it; RefreshLiveness bypasses it.
func livenessReady() error {
	livenessOnce.Do(func() { livenessErr = RefreshLiveness() })
	return livenessErr
}

// SessionLiveness returns the observed state of one session row.
//
// A session id with no row returns LivenessUnbound rather than an error: a
// caller asking about a session that no longer exists is asking about something
// with no binding, and inventing an error would push every call site into
// deciding what to do about it — with `dead` the tempting default.
func SessionLiveness(sessionID int64) (Liveness, error) {
	db, err := DB()
	if err != nil {
		return LivenessUnknown, err
	}
	if err = livenessReady(); err != nil {
		return LivenessUnknown, err
	}
	var state string
	err = db.QueryRow(
		`SELECT liveness FROM session_liveness WHERE session_id = ?`, sessionID,
	).Scan(&state)
	if err == sql.ErrNoRows {
		return LivenessUnbound, nil
	}
	if err != nil {
		return LivenessUnknown, fmt.Errorf("liveness: read session %d: %w", sessionID, err)
	}
	return Liveness(state), nil
}

// SessionLivenessRow is one session's observed run state plus the pane it is
// bound to, for callers that report liveness per session rather than act on it
// (`task sessions`, `task show`'s Touched-by block, E-2294).
//
// Pane is set only when Liveness is `live`. A pane id is unique per tmux
// server, not globally (E-2196): the address of a `dead` or `unknown` binding
// names a pane on a server we did not see it on, and printing it beside a live
// pane that happens to share the id invites going to the wrong one.
type SessionLivenessRow struct {
	SessionID int64    `json:"session_id"`
	Liveness  Liveness `json:"liveness"`
	Pane      *string  `json:"pane"`
}

// SessionsLiveness returns the observed state of each named session, in the
// order given. Reads the session_liveness view, the one definition of liveness,
// rather than re-deriving it: a reader comparing this with the claim guard or
// the status monitor must be looking at the same answer.
//
// An id with no session row reads `unbound`, exactly as SessionLiveness does
// for one — a session that no longer exists has no binding to judge.
func SessionsLiveness(sessionIDs []int64) ([]SessionLivenessRow, error) {
	out := make([]SessionLivenessRow, 0, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return out, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}
	if err = livenessReady(); err != nil {
		return nil, err
	}
	stmt, err := db.Prepare(
		`SELECT liveness, COALESCE(address, '') FROM session_liveness WHERE session_id = ?`,
	)
	if err != nil {
		return nil, fmt.Errorf("liveness: prepare session read: %w", err)
	}
	defer stmt.Close()
	for _, id := range sessionIDs {
		row := SessionLivenessRow{SessionID: id, Liveness: LivenessUnbound}
		var state, address string
		err = stmt.QueryRow(id).Scan(&state, &address)
		switch {
		case err == sql.ErrNoRows:
		case err != nil:
			return nil, fmt.Errorf("liveness: read session %d: %w", id, err)
		default:
			row.Liveness = Liveness(state)
			if row.Liveness == LivenessLive && address != "" {
				row.Pane = &address
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// ── observation: tmux ───────────────────────────────────────────────────────

// observedServer is one tmux server we successfully reached, plus its panes.
type observedServer struct {
	uuid  string
	panes []observedPane
}

type observedPane struct {
	address string // tmux pane id, e.g. "%414"
	command string // pane_current_command; STATUS-LINE HINT ONLY (see header)
}

// tmuxSocketDirArgs builds `tmux display-message -p #{socket_path}`.
func tmuxSocketDirArgs() []string {
	return []string{"display-message", "-p", "#{socket_path}"}
}

// tmuxServerUUIDArgs builds `tmux -S <socket> show-options -gv @server_uuid`.
func tmuxServerUUIDArgs(socket string) []string {
	return []string{"-S", socket, "show-options", "-gv", "@server_uuid"}
}

// tmuxListPanesArgs builds the ONE call that reads a whole server's panes.
// Both fields come from a single invocation on purpose: asking tmux per pane
// would be N subprocesses per refresh, and — worse — would observe different
// panes at different instants, so the snapshot would not be internally
// consistent.
func tmuxListPanesArgs(socket string) []string {
	return []string{"-S", socket, "list-panes", "-a", "-F", "#{pane_id}\t#{pane_current_command}"}
}

// observeTmuxServers enumerates reachable tmux servers and reads each one's
// panes. Returns only servers it BOTH reached and could identify.
//
// A server with no @server_uuid is skipped rather than guessed at: without an
// identity there is no way to say which bindings its panes correspond to, and
// attributing them to the wrong server is the original defect. Skipping leaves
// those sessions `unknown`, which is the honest answer.
//
// Enumeration is best-effort by design. A server on a socket outside this
// directory (`tmux -S /elsewhere`, a relocated TMUX_TMPDIR) is simply not
// observed, and its sessions read `unknown`. Under the previous design an
// incomplete enumeration was a safety hole, because completeness was what
// justified a destructive write; here it costs only certainty about rows we
// then decline to judge.
//
// Three directories are enumerated: the one holding the socket of the server
// this process can reach (the one it is inside, else the default), tmux's
// default socket directory — which differs from the first inside one of
// Endless's servers — and Endless's own socket directory (E-2240), where the
// console and task-session servers live at fixed paths. Any may be absent; a
// directory listed twice is read once.
func observeTmuxServers() []observedServer {
	var dirs []string
	if out, err := tmuxCommand(tmuxSocketDirArgs()...).Output(); err == nil {
		dirs = append(dirs, filepath.Dir(strings.TrimSpace(string(out))))
	}
	dirs = append(dirs, defaultTmuxSocketDir(), tmuxservers.SocketDir())

	var servers []observedServer
	seen := map[string]bool{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		// /tmp is a symlink on macOS, so one directory can arrive under two
		// spellings.
		key := filepath.Clean(dir)
		if real, err := filepath.EvalSymlinks(dir); err == nil {
			key = real
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		servers = append(servers, observeSocketDir(dir)...)
	}
	return servers
}

// defaultTmuxSocketDir is where tmux puts `-L` sockets: $TMUX_TMPDIR, else
// /tmp, then tmux-<uid>.
func defaultTmuxSocketDir() string {
	base := os.Getenv("TMUX_TMPDIR")
	if base == "" {
		base = "/tmp"
	}
	return filepath.Join(base, fmt.Sprintf("tmux-%d", os.Getuid()))
}

// observeSocketDir reads every identifiable tmux server whose socket is in dir.
// An unreadable or missing directory observes nothing — see RefreshLiveness's
// contract.
func observeSocketDir(dir string) []observedServer {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	var servers []observedServer
	for _, entry := range entries {
		info, statErr := entry.Info()
		if statErr != nil || info.Mode()&os.ModeSocket == 0 {
			continue
		}
		socket := filepath.Join(dir, entry.Name())
		if !socketAccepts(socket) {
			// A socket file nothing is listening on — a server that was killed
			// without unlinking it. Answered here by a connect() rather than by
			// the tmux exec below, which would say the same thing for the price
			// of a fork (E-2228).
			continue
		}

		uuidOut, uuidErr := tmuxCommand(tmuxServerUUIDArgs(socket)...).Output()
		if uuidErr != nil {
			// Stale socket (no server listening) or a live server with no
			// @server_uuid set. Both exit non-zero; neither is identifiable.
			continue
		}
		uuid := strings.TrimSpace(string(uuidOut))
		if uuid == "" {
			continue
		}

		panesOut, panesErr := tmuxCommand(tmuxListPanesArgs(socket)...).Output()
		if panesErr != nil {
			// Reached it a moment ago, cannot read it now (it died mid-refresh).
			// Do NOT record it as observed: an observed server with zero panes
			// would declare every one of its sessions dead.
			continue
		}
		servers = append(servers, observedServer{uuid: uuid, panes: parsePaneList(string(panesOut))})
	}
	return servers
}

// socketProbe is what a recorded server's socket said when asked directly. The
// zero value is "no verdict", so a probe that gives up says nothing.
type socketProbe struct {
	// refused: the socket file exists and nothing is listening on it.
	refused bool
	// answered: a server answered; uuid is its @server_uuid, "" when it has none.
	answered bool
	uuid     string
}

// proves reports whether this probe proves the server named uuid gone. A server
// that answered with no uuid is a different one: the recorded server had an id
// in memory, and a running server does not lose an option. One that answered
// with uuid, or with an id uuid is an alias of (E-2428), is the same server and
// proves nothing; any other id still proves it gone.
func (p socketProbe) proves(q rowQuerier, uuid string) (bool, error) {
	if p.refused {
		return true, nil
	}
	if !p.answered {
		return false, nil
	}
	if p.uuid == "" {
		return true, nil
	}
	same, err := SameTmuxServer(q, p.uuid, uuid)
	if err != nil {
		return false, fmt.Errorf("liveness: %w", err)
	}
	return !same, nil
}

// tmuxProbeServerUUIDArgs builds `tmux -S <socket> show-options -gqv
// @server_uuid`. The -q is load-bearing: without it an unset option exits
// non-zero, the same as a failed exec, and "no id" — which proves the recorded
// server gone — could not be told from "could not ask", which proves nothing.
func tmuxProbeServerUUIDArgs(socket string) []string {
	return []string{"-S", socket, "show-options", "-gqv", "@server_uuid"}
}

// probeTmuxSocket asks the socket at path who is there (E-2300). See the file
// header's "Gone servers" for the verdicts and why a missing file is none.
//
// tmux is exec'd only after a connect succeeds, so a probe never starts a
// server on a dead socket.
func probeTmuxSocket(path string) socketProbe {
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeSocket == 0 {
		return socketProbe{}
	}
	conn, err := net.DialTimeout("unix", path, socketAcceptTimeout)
	if err != nil {
		return socketProbe{refused: errors.Is(err, syscall.ECONNREFUSED)}
	}
	conn.Close()
	out, err := tmuxCommand(tmuxProbeServerUUIDArgs(path)...).Output()
	if err != nil {
		return socketProbe{}
	}
	return socketProbe{answered: true, uuid: strings.TrimSpace(string(out))}
}

// socketAcceptTimeout bounds the liveness connect() below. A listening server
// accepts at once; the bound only matters for one wedged mid-accept.
const socketAcceptTimeout = 200 * time.Millisecond

// socketAccepts reports whether a server is listening on the unix socket at
// path. It is the cheap half of observeTmuxServers' probe (E-2228): a dead
// socket refuses the connect immediately, so it costs no tmux child.
//
// Dead sockets are not rare. Every tmux server killed without unlinking its
// socket — a test's private server, a verify run's — leaves one, and they
// accumulate in the directory every refresh scans. On the machine that found
// this, 151 of 153 were dead: two tmux execs each, on every tick of every
// monitor, about 1s of child CPU per tick, for servers that did not exist.
//
// A false "accepts" costs only the exec it would have cost anyway: the tmux
// probe that follows is still the verdict.
func socketAccepts(path string) bool {
	conn, err := net.DialTimeout("unix", path, socketAcceptTimeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// parsePaneList turns `list-panes -F "#{pane_id}\t#{pane_current_command}"`
// output into pane records. Split out as a pure function so the parsing is
// tested without tmux.
//
// A line with no tab yields a pane with an empty command rather than being
// dropped: the address is what liveness needs, and the command feeds only a
// cosmetic hint. Losing a pane because its command was unreadable would turn a
// live session `dead`.
func parsePaneList(out string) []observedPane {
	var panes []observedPane
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		address, command, _ := strings.Cut(line, "\t")
		address = strings.TrimSpace(address)
		if address == "" {
			continue
		}
		panes = append(panes, observedPane{address: address, command: strings.TrimSpace(command)})
	}
	return panes
}
