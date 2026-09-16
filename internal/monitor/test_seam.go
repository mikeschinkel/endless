package monitor

import (
	"database/sql"
	"os"
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
// produce.
func SeedPaneProcess(db *sql.DB, serverUUID, pane string) (int64, error) {
	return ensureProcess(db, processkind.ProcessKindTmux, serverUUID, pane)
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
