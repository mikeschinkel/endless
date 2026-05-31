package monitor

import (
	"database/sql"
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
	prevCtxDir, prevPathOverride := dbContextDir, dbPathOverride

	dbOnce = &sync.Once{}
	dbOnce.Do(func() {}) // mark consumed so DB() returns dbConn directly
	dbConn = db
	dbErr = nil
	// Non-empty dbContextDir satisfies the E-1429 self-dev-worktree gate
	// for tests running from inside the worktree. The value is opaque to
	// the gate; only its non-empty-ness matters.
	dbContextDir = "test-injected"

	return func() {
		dbOnce = prevOnce
		dbConn = prevConn
		dbErr = prevErr
		dbContextDir = prevCtxDir
		dbPathOverride = prevPathOverride
	}
}
