package schema

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mikeschinkel/go-dt"
)

// UpResult is what Up reports: the version the database was at, the version it
// is at now, and whether anything moved. Status is "migrated" when the version
// changed and "current" when there was nothing to do, so a caller can tell the
// two apart without comparing numbers.
type UpResult struct {
	Status string `json:"status"`
	From   int64  `json:"from"`
	To     int64  `json:"to"`
}

// Up brings db to the latest version and reconciles the enum mirrors —
// MigrateContext — and reports the versions either side of it.
//
// Two programs call it on a handle from OpenExisting: `endless-migrate up`,
// which a self_dev land runs (E-2192), and `endless-go event upgrade`, which is
// `endless db upgrade` (E-2020). Both exist to work on a database the
// application's own connect is refusing, so neither may reach monitor.DB().
func Up(ctx context.Context, db *sql.DB) (res UpResult, err error) {
	var latest int64

	res.From, err = DBVersion(ctx, db)
	if err != nil {
		goto end
	}
	latest, err = LatestVersion()
	if err != nil {
		goto end
	}
	// A database AHEAD of this binary is not something to migrate: there is
	// nothing forward to apply, and "up" has no business reasoning about
	// versions it does not carry (ED-1570).
	if res.From > latest {
		err = fmt.Errorf(
			"the database is at schema version %d and this binary carries "+
				"version %d: it is older than the database and cannot upgrade it",
			res.From, latest)
		goto end
	}
	err = MigrateContext(ctx, db)
	if err != nil {
		goto end
	}
	res.To, err = DBVersion(ctx, db)
	if err != nil {
		goto end
	}
	res.Status = "current"
	if res.To != res.From {
		res.Status = "migrated"
	}

end:
	return res, err
}

// OpenExisting opens the database FILE at path directly — no version check, no
// enum seed, no integrity gate, no worktree-build check, no sandbox routing —
// refusing a relative path and a file that does not exist.
//
// This is what separates the recovery paths from the application's connect.
// monitor.DB() refuses a database at the wrong version and fail-closes one whose
// enum mirrors have drifted; the tools that FIX those conditions would be stuck
// behind the refusal they exist to clear if they opened through it.
//
// An absolute path or nothing: dbcontext resolves a RELATIVE path when no home
// directory and no XDG_CONFIG_HOME can be found, and a relative one would be
// created under whatever directory this was invoked from — a fresh, empty
// database that migrates flawlessly and is not the ledger anyone meant.
//
// The file must already exist, because sql.Open would create one, and a
// migration that CREATES its target has migrated nothing.
func OpenExisting(path dt.Filepath) (db *sql.DB, err error) {
	var exists bool

	if !path.IsAbs() {
		err = fmt.Errorf(
			"refusing to migrate a database at a relative path: %s\n"+
				"No --db main, no --db-dir, no XDG_CONFIG_HOME and no home "+
				"directory resolved, so there is no way to know which ledger "+
				"was meant.", path)
		goto end
	}

	exists, err = path.Exists()
	if err != nil {
		err = fmt.Errorf("checking for the database at %s: %w", path, err)
		goto end
	}
	if !exists {
		err = fmt.Errorf("no database at %s", path)
		goto end
	}

	db, err = openFile(path)

end:
	return db, err
}

// openFile opens and configures the connection. The three PRAGMAs configure the
// CONNECTION rather than the schema, and monitor.DB() sets exactly these three:
// a migration must run under the same connection settings whichever program
// applies it — foreign_keys above all.
func openFile(path dt.Filepath) (db *sql.DB, err error) {
	var pragma string

	db, err = sql.Open("sqlite", string(path))
	if err != nil {
		err = fmt.Errorf("opening %s: %w", path, err)
		goto end
	}

	// sql.Open is lazy, so nothing above has touched the file yet.
	err = db.Ping()
	if err != nil {
		err = fmt.Errorf("connecting to %s: %w", path, err)
		goto end
	}

	// SQLite is single-writer; one connection is what makes BEGIN IMMEDIATE
	// mean what it says through Go's connection pool.
	db.SetMaxOpenConns(1)

	for _, pragma = range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		_, err = db.Exec(pragma)
		if err != nil {
			err = fmt.Errorf("%s on %s: %w", pragma, path, err)
			goto end
		}
	}

end:
	if err != nil && db != nil {
		_ = db.Close()
		db = nil
	}
	return db, err
}
