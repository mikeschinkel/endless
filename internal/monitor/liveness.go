package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/mikeschinkel/endless/internal/processkind"
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
//	dead     — we reached its server, and the address was absent
//	unknown  — we could NOT reach its server; we have no opinion
//	unbound  — the session has no process binding at all (background agents,
//	           and any session started outside a multiplexer)
//
// `unknown` is the load-bearing one. An unreachable server (a socket outside the
// enumerated directory, a relocated TMUX_TMPDIR, tmux not installed) yields
// `unknown` for its sessions, NOT `dead`. That is what bounds the blast radius
// of a failed observation to zero rows: no consumer may treat `unknown` as
// `dead`. The site that matters most is the spawn/claim ownership guard, which
// must read `unknown` as STILL OWNED and refuse — a wrong refusal is an
// annoyance, the inverse hands someone else a live worktree.
//
// `unbound` exists so a background agent, whose `process_id` is legitimately
// NULL, is never mistaken for a session whose pane vanished.
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
	// LivenessDead — we reached the server; the address was absent. The ONLY
	// state that may be read as "this session is over".
	LivenessDead Liveness = "dead"
	// LivenessUnknown — the server was not reached. No opinion. Never `dead`.
	LivenessUnknown Liveness = "unknown"
	// LivenessUnbound — no process binding (background agent, non-multiplexer).
	LivenessUnbound Liveness = "unbound"
)

// IsGone reports whether this state may be treated as "the session is over".
// ONLY `dead` qualifies. Every consumer that has to decide something
// consequential — freeing a task, dropping an owner — calls this rather than
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

	// createObservedServers records WHICH servers the snapshot actually reached.
	// Without it, "no live_processes row" is ambiguous between "the pane is gone"
	// and "we never looked", and collapsing those two is exactly the 2026-08-05
	// incident. This table is what makes `unknown` expressible.
	createObservedServers = `CREATE TEMP TABLE IF NOT EXISTS observed_servers (
		kind_id     INTEGER NOT NULL,
		server_uuid TEXT
	)`

	// createLivenessView is the single definition of liveness. Consumers SELECT
	// from it; none re-derive the CASE. ifnull() on both sides of the server
	// comparison because SQL equality with NULL is never true, and a NULL
	// server_uuid is legitimate for kind=pid and for E-1898's migration backfill.
	createLivenessView = `CREATE TEMP VIEW IF NOT EXISTS session_liveness AS
		SELECT
			s.id           AS session_id,
			s.process_id   AS process_id,
			p.kind_id      AS kind_id,
			p.server_uuid  AS server_uuid,
			p.address      AS address,
			l.command      AS command,
			CASE
				WHEN p.id IS NULL      THEN 'unbound'
				WHEN o.kind_id IS NULL THEN 'unknown'
				WHEN l.address IS NULL THEN 'dead'
				ELSE                        'live'
			END AS liveness
		FROM sessions s
		LEFT JOIN processes p
			   ON p.id = s.process_id
		LEFT JOIN observed_servers o
			   ON o.kind_id = p.kind_id
			  AND ifnull(o.server_uuid, '') = ifnull(p.server_uuid, '')
		LEFT JOIN live_processes l
			   ON l.kind_id = p.kind_id
			  AND ifnull(l.server_uuid, '') = ifnull(p.server_uuid, '')
			  AND l.address = p.address`
)

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

	tmuxServerUUIDFn = func() (string, error) { return serverUUID, nil }
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
	for _, stmt := range []string{createLiveProcesses, createObservedServers, createLivenessView} {
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
	for _, stmt := range []string{`DELETE FROM live_processes`, `DELETE FROM observed_servers`} {
		if _, err = tx.Exec(stmt); err != nil {
			return fmt.Errorf("liveness: clear snapshot: %w", err)
		}
	}

	for _, srv := range observeServers() {
		if _, err = tx.Exec(
			`INSERT INTO observed_servers (kind_id, server_uuid) VALUES (?, ?)`,
			int(processkind.ProcessKindTmux), srv.uuid,
		); err != nil {
			return fmt.Errorf("liveness: record observed server: %w", err)
		}
		for _, pane := range srv.panes {
			if _, err = tx.Exec(
				`INSERT INTO live_processes (kind_id, server_uuid, address, command)
				 VALUES (?, ?, ?, ?)`,
				int(processkind.ProcessKindTmux), srv.uuid, pane.address, pane.command,
			); err != nil {
				return fmt.Errorf("liveness: record observed pane: %w", err)
			}
		}
	}

	return tx.Commit()
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
func observeTmuxServers() []observedServer {
	out, err := exec.Command("tmux", tmuxSocketDirArgs()...).Output()
	if err != nil {
		// No tmux, or no server at all. Observe nothing; every session reads
		// `unknown`. Not an error — see RefreshLiveness's contract.
		return nil
	}
	dir := filepath.Dir(strings.TrimSpace(string(out)))
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

		uuidOut, uuidErr := exec.Command("tmux", tmuxServerUUIDArgs(socket)...).Output()
		if uuidErr != nil {
			// Stale socket (no server listening) or a live server with no
			// @server_uuid set. Both exit non-zero; neither is identifiable.
			continue
		}
		uuid := strings.TrimSpace(string(uuidOut))
		if uuid == "" {
			continue
		}

		panesOut, panesErr := exec.Command("tmux", tmuxListPanesArgs(socket)...).Output()
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
