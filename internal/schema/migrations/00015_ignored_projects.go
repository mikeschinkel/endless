package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
)

// ignoredProjects is migration 15 (E-2251): "not a project" moves from the
// global config's `ignore` list onto projects rows with status 'ignored'.
//
//   - live_projects — every row that IS a project. Readers that list or look up
//     projects read it; the resolvers that walk a directory up to its project
//     still read projects, because an ignored row is exactly what stops them.
//   - A one-time import of the `ignore` list from the config.json that sits
//     BESIDE the database being migrated. That pairing is the real one only for
//     the main database (~/.config/endless/endless.db next to its config.json);
//     a sandbox, a test database and the projector's temp database have no
//     config.json beside them, so they import nothing — which is what keeps a
//     migration that reads a user file from leaking that file into databases it
//     does not describe.
//
// An entry whose directory already has a row is left alone: the old list was
// read only by `project discover`, so a row there was registered deliberately,
// and an explicit registration under an ignored path is a project (E-2251's
// rule A). A new ignored row is named by its stored path, which can never
// collide with a real project's name.
//
// The config key itself is left in place, now inert — a schema step does not
// rewrite a user's hand-edited file.
//
// Go rather than SQL for the import. No Down: the rows are data the user can
// clear with `endless project unignore`.
func ignoredProjects() *goose.Migration {
	return goose.NewGoMigration(15, &goose.GoFunc{RunTx: ignoredProjectsUp}, nil)
}

func ignoredProjectsUp(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx,
		`CREATE VIEW IF NOT EXISTS live_projects AS
    SELECT * FROM projects WHERE status != 'ignored'`); err != nil {
		return fmt.Errorf("creating live_projects: %w", err)
	}

	cfgPath, err := siblingConfigPath(ctx, tx)
	if err != nil || cfgPath == "" {
		return err
	}
	entries, err := readIgnoreList(cfgPath)
	if err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	for _, e := range entries {
		stored := storedIgnorePath(e, home)
		if stored == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO projects (name, path, status, created_at, updated_at)
			 SELECT ?, ?, 'ignored', ?, ?
			  WHERE NOT EXISTS (SELECT 1 FROM projects WHERE path = ?)`,
			stored, stored, now, now, stored,
		); err != nil {
			return fmt.Errorf("importing ignored %s: %w", stored, err)
		}
	}
	return nil
}

// siblingConfigPath returns the config.json beside the main database file, or
// "" when the database is in memory or has no config beside it.
func siblingConfigPath(ctx context.Context, tx *sql.Tx) (string, error) {
	rows, err := tx.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return "", fmt.Errorf("locating the database file: %w", err)
	}
	defer rows.Close()
	var file string
	for rows.Next() {
		var seq int
		var name, f string
		if err := rows.Scan(&seq, &name, &f); err != nil {
			return "", err
		}
		if name == "main" {
			file = f
		}
	}
	if err := rows.Err(); err != nil || file == "" {
		return "", err
	}
	p := filepath.Join(filepath.Dir(file), "config.json")
	if _, err := os.Stat(p); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	return p, nil
}

func readIgnoreList(path string) ([]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	var cfg struct {
		Ignore []string `json:"ignore"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		// Not this migration's file to judge: the CLI reports a malformed
		// config where it reads it. There is simply nothing to import.
		return nil, nil
	}
	return cfg.Ignore, nil
}

// storedIgnorePath turns a config entry into the STORED form projects.path
// holds — home-relative, symlinks resolved where the directory exists. Frozen
// here rather than calling monitor.StoredProjectPath, which this package cannot
// import (monitor runs the migrations).
func storedIgnorePath(entry, home string) string {
	entry = strings.TrimSpace(entry)
	if entry == "" {
		return ""
	}
	abs := entry
	if entry == "~" || strings.HasPrefix(entry, "~/") {
		if home == "" {
			return ""
		}
		abs = filepath.Join(home, strings.TrimPrefix(entry, "~"))
	}
	if !filepath.IsAbs(abs) {
		return ""
	}
	abs = filepath.Clean(abs)
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		abs = r
	}
	// Both spellings of home: a directory that no longer exists keeps the
	// unresolved one, since EvalSymlinks fails on a missing leaf.
	homes := []string{}
	if home != "" {
		if h, err := filepath.EvalSymlinks(home); err == nil {
			homes = append(homes, h)
		}
		homes = append(homes, filepath.Clean(home))
	}
	for _, h := range homes {
		if abs == h {
			return "~"
		}
		if strings.HasPrefix(abs, h+string(filepath.Separator)) {
			return "~" + abs[len(h):]
		}
	}
	return abs
}
