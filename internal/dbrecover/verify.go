package dbrecover

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mikeschinkel/go-dt"
	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/schema"
)

// Check is what the restored database proved against its backup.
type Check struct {
	JournalMode   string   `json:"journal_mode"`
	Integrity     string   `json:"integrity"`
	Version       int64    `json:"version"`
	BackupVersion int64    `json:"backup_version"`
	Tables        int      `json:"tables"`
	Mismatches    []string `json:"mismatches,omitempty"`
}

// Problems lists every way the restored copy fails to match its backup. Empty
// means it verified.
func (c Check) Problems() (problems []string) {
	if c.JournalMode != "wal" {
		problems = append(problems, fmt.Sprintf("journal_mode %s (wanted wal)", c.JournalMode))
	}
	if c.Integrity != "ok" {
		problems = append(problems, fmt.Sprintf("integrity_check %s (wanted ok)", c.Integrity))
	}
	if c.Version != c.BackupVersion {
		problems = append(problems, fmt.Sprintf("schema version %d (the backup is at %d)",
			c.Version, c.BackupVersion))
	}
	return append(problems, c.Mismatches...)
}

// OpenReadOnly opens the SQLite file at path read-only: nothing it does can
// checkpoint, migrate or otherwise rewrite the copy standing between the user
// and data loss.
func OpenReadOnly(path string) (*sql.DB, error) {
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("cannot open %s read-only: '?' and '#' are URI syntax", path)
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, err
	}
	err = db.Ping()
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	return db, nil
}

// Version is the schema version of the database at path, read read-only.
func Version(ctx context.Context, path string) (version int64, err error) {
	db, err := OpenReadOnly(path)
	if err != nil {
		return 0, err
	}
	defer func() { _ = db.Close() }()
	return versionOf(ctx, db)
}

// versionOf is schema.DBVersion, except that a database goose has never
// stamped is version 0 rather than an error: `db restore` may be handed a copy
// that predates versioning, and comparing it with its own restored copy only
// needs the two answers to agree.
func versionOf(ctx context.Context, db *sql.DB) (int64, error) {
	var stamped bool
	err := db.QueryRowContext(ctx,
		"SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='goose_db_version')",
	).Scan(&stamped)
	if err != nil || !stamped {
		return 0, err
	}
	return schema.DBVersion(ctx, db)
}

// Integrity runs PRAGMA integrity_check on db and joins its rows.
func Integrity(ctx context.Context, db *sql.DB) (string, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return "", err
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var line string
		if err = rows.Scan(&line); err != nil {
			return "", err
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return "no result", rows.Err()
	}
	return strings.Join(lines, "; "), rows.Err()
}

// Verify proves the database at dbPath is its backup: journal_mode set back to
// WAL (VACUUM INTO writes a rollback-journal file), PRAGMA integrity_check ok,
// the backup's schema version, and every table's row count equal to the
// backup's.
//
// The database is opened through schema.OpenExisting, which sets WAL and
// applies no schema; the caller holds the maintenance lock, which exempts it.
func Verify(ctx context.Context, dbPath, backup string) (c Check, err error) {
	var live, bak *sql.DB
	var tables []string

	live, err = schema.OpenExisting(dt.Filepath(dbPath))
	if err != nil {
		goto end
	}
	defer func() { _ = live.Close() }()
	bak, err = OpenReadOnly(backup)
	if err != nil {
		goto end
	}
	defer func() { _ = bak.Close() }()

	err = live.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&c.JournalMode)
	if err != nil {
		goto end
	}
	c.JournalMode = strings.ToLower(c.JournalMode)
	c.Integrity, err = Integrity(ctx, live)
	if err != nil {
		goto end
	}
	c.Version, err = versionOf(ctx, live)
	if err != nil {
		goto end
	}
	c.BackupVersion, err = versionOf(ctx, bak)
	if err != nil {
		goto end
	}

	tables, err = tableNames(ctx, bak)
	if err != nil {
		goto end
	}
	c.Tables = len(tables)
	for _, table := range tables {
		want, countErr := rowCount(ctx, bak, table)
		if countErr != nil {
			err = countErr
			goto end
		}
		got, countErr := rowCount(ctx, live, table)
		if countErr != nil {
			c.Mismatches = append(c.Mismatches, fmt.Sprintf("table %s: %v", table, countErr))
			continue
		}
		if got != want {
			c.Mismatches = append(c.Mismatches,
				fmt.Sprintf("table %s: %d rows (the backup has %d)", table, got, want))
		}
	}

end:
	if err != nil {
		err = fmt.Errorf("verifying %s against %s: %w", dbPath, backup, err)
	}
	return c, err
}

func tableNames(ctx context.Context, db *sql.DB) (names []string, err error) {
	rows, err := db.QueryContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

func rowCount(ctx context.Context, db *sql.DB, table string) (n int64, err error) {
	quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
	err = db.QueryRowContext(ctx, "SELECT count(*) FROM "+quoted).Scan(&n)
	return n, err
}
