package monitor

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"github.com/mikeschinkel/endless/internal/processkind"
)

// TestServerUUID is the tmux server identity fixtures pin by convention, so a
// test that seeds a pane binding and a test that pins the observation agree
// about which server they are talking about without repeating a literal.
const TestServerUUID = "test-server-uuid"

// SeedPaneProcess returns the processes.id for a tmux pane on serverUUID,
// creating the row if absent. USE ONLY IN TESTS: fixtures that used to write a
// bare pane string into sessions.process now write this id into
// sessions.process_id.
//
// It goes through the same ensureProcess the production path uses, so a fixture
// cannot accidentally construct an identity shape the real code would never
// produce. For the same reason it records the server (SeedTestTmuxServer), as
// EnsureTmuxPaneProcess records every real one on its first binding: since
// E-2519 a binding on an unrecorded server the socket scan does not see reads
// `dead`, and a fixture that wants that pre-E-2300 shape says so by deleting
// the row.
func SeedPaneProcess(db *sql.DB, serverUUID, pane string) (int64, error) {
	id, err := ensureProcess(db, processkind.ProcessKindTmux, serverUUID, pane)
	if err != nil {
		return 0, err
	}
	if err = SeedTestTmuxServer(db, serverUUID); err != nil {
		return 0, err
	}
	return id, nil
}

// testTmuxSocketPath is the socket a test server is recorded on: absolute, so
// recordTmuxServer accepts it, and one nothing listens on.
func testTmuxSocketPath(serverUUID string) string {
	return "/endless-test/sockets/" + serverUUID
}

// SeedTestTmuxServer records serverUUID in tmux_servers on testTmuxSocketPath,
// unless a row already names its socket, so its sessions read `unknown` when it
// is not observed rather than `dead` (E-2519). USE ONLY IN TESTS. The test
// prober gives no verdict for any path, which is the `unknown` of a recorded
// server whose socket file is gone.
func SeedTestTmuxServer(db *sql.DB, serverUUID string) error {
	if serverUUID == "" {
		return nil
	}
	_, err := db.Exec(
		`INSERT OR IGNORE INTO tmux_servers (server_uuid, socket_path, first_seen_at)
		 VALUES (?, ?, '2026-01-01T00:00:00')`,
		serverUUID, testTmuxSocketPath(serverUUID),
	)
	return err
}

// SetTestDB rebinds the monitor.DB() singleton to db and returns a
// restore func that reverts the package vars to their prior state. It
// exists so packages outside `monitor` can exercise functions that
// internally call monitor.DB() without needing their own DB-injection
// refactor.
//
// USE ONLY IN TESTS. Production callers never need this — they accept
// the singleton's lifecycle. The function is exported (rather than
// package-private) only because cross-package tests cannot import
// `_test.go` helpers; collocating this in a regular .go file is the
// idiomatic workaround. Code review should flag any production import
// of this symbol.
//
// Concurrency: SetTestDB mutates package-level state, so tests using
// it must NOT call t.Parallel().
//
// E-1506.
func SetTestDB(db *sql.DB) (restore func()) {
	prevOnce, prevConn, prevErr := dbOnce, dbConn, dbErr
	prevCtxDir, prevPathOverride := dbContextDir, dbPathOverride

	dbOnce = &sync.Once{}
	dbOnce.Do(func() {}) // mark consumed so DB() returns dbConn directly
	dbConn = db
	dbErr = nil
	// dbContextDir must be non-empty to satisfy the E-1429 self-dev-worktree
	// gate, and ABSOLUTE so ConfigDir()-derived writes (config.json, the
	// machine-local diagnostic log at log/user-machine.jsonl) land in a throwaway
	// temp dir instead of a relative "test-injected/" under the package's working
	// tree. An absolute temp dir keeps such writes out of the repo; RemoveAll in
	// restore cleans it up.
	tmpCfg, err := os.MkdirTemp("", "endless-testcfg-")
	if err != nil {
		tmpCfg = "test-injected" // last resort: keep the gate satisfied
	}
	dbContextDir = tmpCfg
	_ = FreeTestTempTree(tmpCfg)

	// A different database has different (which is to say, no) TEMP observation
	// tables, so any snapshot taken against the previous one is meaningless
	// here. Dropping the once-guard makes the next liveness read re-observe
	// into this DB instead of trusting a stale "already refreshed" flag.
	prevLivenessOnce := livenessOnce
	livenessOnce = &sync.Once{}
	livenessErr = nil

	return func() {
		livenessOnce = prevLivenessOnce
		livenessErr = nil
		dbOnce = prevOnce
		dbConn = prevConn
		dbErr = prevErr
		dbContextDir = prevCtxDir
		dbPathOverride = prevPathOverride
		if tmpCfg != "test-injected" {
			os.RemoveAll(tmpCfg)
		}
	}
}

// TestTempTreeRemovals are the built-in rules that refuse the system temp
// trees: /private on macOS (where $TMPDIR resolves), /tmp and /var/tmp on Linux.
// A test's fixtures live there, so a test removes them (E-2369).
var TestTempTreeRemovals = []string{"/private", "/tmp", "/var/tmp"}

// FreeTestTempTree writes an overrides file into cfgDir that removes the rules
// refusing the temp trees, so a test may treat directories under its temp dir
// as projects. It uses the same overrides file a user would edit; cfgDir is
// the test's own config dir, so the removal reaches no one else.
//
// USE ONLY IN TESTS.
func FreeTestTempTree(cfgDir string) error {
	body, err := json.Marshal(map[string]any{"remove": TestTempTreeRemovals})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(cfgDir, DisallowedOverridesFile), body, 0o644)
}
