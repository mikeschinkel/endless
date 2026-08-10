package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/processkind"
)

// processes.go owns the DURABLE half of "where does this session run".
// liveness.go owns the volatile half. The split is the whole point of E-1898:
// this file writes facts that stay true forever, that file derives opinions
// that are never written.
//
// A processes row says "pane %414 on tmux server abc-123". That remains true
// after the pane closes, the server dies, and the session ends. Nothing here is
// deleted or rewritten because of something observed about the present.

// ErrNoProcessIdentity is returned when a binding cannot be given an identity —
// today only when a tmux pane id is requested while no tmux server can be
// reached to name itself. Callers record NO binding rather than a partial one:
// a processes row with a guessed server would be an identity that can never
// match an observation, which is worse than an honest absence.
var ErrNoProcessIdentity = errors.New("no process identity available")

// EnsureProcess returns the processes.id for (kind, serverUUID, address),
// creating the row on first sight and bumping last_seen_at otherwise.
//
// serverUUID must be empty only for kinds where that is legitimate (pid). For
// tmux it is half the identity: without it, "%414" from a restarted server
// collides with "%414" from the previous one, which is E-1530 and, ultimately,
// the 2026-08-05 incident.
//
// The ON CONFLICT target repeats the ifnull() expression from the
// processes_identity index because SQLite matches an upsert target against the
// index EXPRESSION, not the column list; a bare (kind_id, server_uuid, address)
// target would not resolve to that index and the upsert would fail.
func EnsureProcess(kind processkind.ProcessKind, serverUUID, address string) (int64, error) {
	if address == "" {
		return 0, fmt.Errorf("ensure process: address required")
	}
	if kind.UsesServer() && serverUUID == "" {
		return 0, ErrNoProcessIdentity
	}
	db, err := DB()
	if err != nil {
		return 0, err
	}
	return ensureProcess(db, kind, serverUUID, address)
}

func ensureProcess(db *sql.DB, kind processkind.ProcessKind, serverUUID, address string) (int64, error) {
	// nullable keeps "" out of the column: server_uuid IS NULL is the schema's
	// way of saying "not applicable / not known", and storing "" alongside it
	// would create two spellings of the same thing.
	var server any
	if serverUUID != "" {
		server = serverUUID
	}

	var id int64
	err := db.QueryRow(
		`INSERT INTO processes (kind_id, server_uuid, address)
		 VALUES (?, ?, ?)
		 ON CONFLICT (kind_id, ifnull(server_uuid, ''), address)
		 DO UPDATE SET last_seen_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
		 RETURNING id`,
		int(kind), server, address,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("ensure process %s/%s: %w", kind, address, err)
	}
	return id, nil
}

// EnsureTmuxPaneProcess resolves a tmux pane id to a processes.id, pairing it
// with the identity of the server this process is talking to.
//
// Returns ErrNoProcessIdentity when the pane is empty (not in tmux) or the
// server cannot be identified. Callers leave sessions.process_id NULL in that
// case, which reads `unbound` — honest, and never `dead`.
func EnsureTmuxPaneProcess(pane string) (int64, error) {
	if pane == "" {
		return 0, ErrNoProcessIdentity
	}
	uuid, err := TmuxServerUUID()
	if err != nil || uuid == "" {
		return 0, ErrNoProcessIdentity
	}
	return EnsureProcess(processkind.ProcessKindTmux, uuid, pane)
}

// currentPaneProcessID resolves this process's own TMUX_PANE to a processes.id
// for direct binding into SQL, returning an untyped nil when there is no
// identifiable binding (not in tmux, or no reachable server).
//
// The `any` return is deliberate: it binds as SQL NULL, so the write paths keep
// using one COALESCE expression for both the bound and unbound cases instead of
// branching. NULL here means `unbound` at read time — never `dead`.
func currentPaneProcessID() any {
	return paneProcessID(os.Getenv("TMUX_PANE"))
}

// paneProcessID is currentPaneProcessID for a pane the caller already holds.
func paneProcessID(pane string) any {
	id, err := EnsureTmuxPaneProcess(pane)
	if err != nil {
		return nil
	}
	return id
}

// ProcessIDsForPanes maps tmux pane ids to EXISTING processes.id values on the
// server this call can currently reach. It never creates rows: it backs read
// paths ("which session owns this pane?"), and a lookup that minted identities
// would write on every status-line repaint.
//
// Panes with no processes row are silently absent from the result, so a caller
// passing three panes may get back one id. Returns an empty slice, never an
// error, when the server cannot be identified — a lookup that cannot name its
// own server must match nothing rather than match by bare pane id, which is the
// collision E-1898 removes.
func ProcessIDsForPanes(panes []string) ([]int64, error) {
	if len(panes) == 0 {
		return nil, nil
	}
	uuid, err := TmuxServerUUID()
	if err != nil || uuid == "" {
		return nil, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}

	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(panes)), ",")
	args := make([]any, 0, len(panes)+2)
	args = append(args, int(processkind.ProcessKindTmux), uuid)
	for _, p := range panes {
		args = append(args, p)
	}

	rows, err := db.Query(
		`SELECT id FROM processes
		 WHERE kind_id = ? AND server_uuid = ? AND address IN (`+placeholders+`)`,
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("process ids for panes: %w", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("process ids for panes: scan: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// processIDArgs renders a processes.id slice as an IN-list placeholder string
// plus its bind arguments. Shared by every pane-scoped reader so they cannot
// drift apart in how they match a binding.
func processIDArgs(ids []int64) (placeholders string, args []any) {
	placeholders = strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	args = make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return placeholders, args
}

// ProcessAddress returns the identity behind a processes.id. Used by readers
// that still need the raw pane string (the tmux argv builders, the status line).
func ProcessAddress(processID int64) (kind processkind.ProcessKind, serverUUID, address string, err error) {
	db, dbErr := DB()
	if dbErr != nil {
		return 0, "", "", dbErr
	}
	var kindID int
	var server sql.NullString
	err = db.QueryRow(
		`SELECT kind_id, server_uuid, address FROM processes WHERE id = ?`, processID,
	).Scan(&kindID, &server, &address)
	if err != nil {
		return 0, "", "", fmt.Errorf("process %d: %w", processID, err)
	}
	return processkind.ProcessKind(kindID), server.String, address, nil
}

// ── tmux server identity ────────────────────────────────────────────────────

// tmuxShowServerUUIDArgs builds `tmux show-options -gv @server_uuid` against the
// ambient server.
func tmuxShowServerUUIDArgs() []string {
	return []string{"show-options", "-gv", "@server_uuid"}
}

// tmuxSetServerUUIDArgs builds `tmux set-option -g @server_uuid <uuid>`.
func tmuxSetServerUUIDArgs(uuid string) []string {
	return []string{"set-option", "-g", "@server_uuid", uuid}
}

// tmuxServerUUIDFn is the seam SetTestTmuxObservation swaps so no test ever
// stamps an option on the developer's real tmux server. Production always uses
// liveTmuxServerUUID.
var tmuxServerUUIDFn = liveTmuxServerUUID

// TmuxServerUUID returns the identity of the tmux server this process is
// talking to, STAMPING a fresh one if the server does not have it yet.
//
// Stamping here (rather than only in `endless tmux init`) is what lets the
// binding path assume identity is always available. The previous design had to
// GATE on the uuid's presence — refuse to act when the server was
// uninitialized — because a binding could be made without one. Minting it on
// demand removes the gate and the whole class of "what do we do when it is
// missing" edge cases along with it.
//
// It is safe to stamp any server we are running inside: the uuid means only
// "this server instance, this lifetime". Stamping a private test server is
// correct — its panes then attribute to it, and to nothing else. A server
// restart mints a new uuid, which is precisely what makes a reissued pane id a
// different identity.
//
// Returns "" with no error when tmux is unreachable; callers treat that as
// ErrNoProcessIdentity rather than a failure.
func TmuxServerUUID() (string, error) {
	return tmuxServerUUIDFn()
}

// liveTmuxServerUUID is TmuxServerUUID's real implementation; see that doc.
func liveTmuxServerUUID() (string, error) {
	out, err := exec.Command("tmux", tmuxShowServerUUIDArgs()...).Output()
	if err == nil {
		if uuid := strings.TrimSpace(string(out)); uuid != "" {
			return uuid, nil
		}
	}
	// `show-options -gv` on an unset user option exits non-zero, so a failure
	// here is indistinguishable from "unset" — try to stamp, and let THAT tell
	// us whether a server is reachable at all.
	uuid := generateUUID()
	if err = exec.Command("tmux", tmuxSetServerUUIDArgs(uuid)...).Run(); err != nil {
		return "", nil // no reachable tmux server
	}
	return uuid, nil
}
