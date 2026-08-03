package monitor

import (
	"database/sql"
	"os"
	"sync"
)

// SetTestDB rebinds the monitor.DB() singleton to db and returns a
// restore func that reverts the package vars to their prior state. It
// exists so packages outside `monitor` (notably `internal/web`) can
// exercise functions that internally call monitor.DB() without needing
// their own DB-injection refactor.
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
	prevCtxDir, prevPathOverride, prevFromFlag := dbContextDir, dbPathOverride, dbContextFromFlag

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

	return func() {
		dbOnce = prevOnce
		dbConn = prevConn
		dbErr = prevErr
		dbContextDir = prevCtxDir
		dbPathOverride = prevPathOverride
		dbContextFromFlag = prevFromFlag
		if tmpCfg != "test-injected" {
			os.RemoveAll(tmpCfg)
		}
	}
}
