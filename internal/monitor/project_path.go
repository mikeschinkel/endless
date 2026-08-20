package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NormalizeProjectPath returns the one canonical form Endless stores and
// compares a project path in: absolute, with every symlink component resolved.
//
// It also expands a leading `~`. That is INPUT TOLERANCE, not part of the
// canonical form — nothing writes a tilde into projects.path, since every
// writer runs through here first. It is kept because the Python half's
// `Path.resolve()` treats a literal `~` as an ordinary directory name and
// silently yields `<cwd>/~/x`, and because ProjectPath expanded `~` before
// E-2002 folded it into this rule; dropping it would quietly change what a
// hand-edited row resolves to.
//
// This is the Go half of a rule the Python CLI implements identically in
// endless.project_path.normalize (E-2002). The two halves MUST agree: the
// Python CLI writes projects.path, the Go hook reads it on every Claude event,
// and a disagreement makes the hook miss the registered row and auto-register a
// second project for the same directory — leaving the session bound to an empty
// duplicate. That is not hypothetical; it is the bug this function exists to
// close, hit on any macOS project reached through /var or /tmp (both symlinks
// into /private) and on any user whose projects live under a symlinked parent.
//
// Non-strict, matching pathlib.Path.resolve(): a path that does not exist yet
// is still resolved as far as it does exist, and the missing tail is appended.
// filepath.EvalSymlinks alone fails outright on a missing leaf, which would
// silently leave those paths unresolved and reintroduce the mismatch for
// exactly the directories a register-then-create flow touches first.
//
// Never returns an error: a path Endless cannot resolve is more useful to a
// caller in its absolute form than as a failure, and every call site here is a
// comparison that degrades to the pre-E-2002 behavior rather than breaking.
func NormalizeProjectPath(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
		}
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	// Missing leaf (or an unreadable component): resolve the deepest ancestor
	// that does exist and re-append the part below it, as pathlib does.
	tail := ""
	dir := abs
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return abs
		}
		tail = filepath.Join(filepath.Base(dir), tail)
		dir = parent
		if resolved, err := filepath.EvalSymlinks(dir); err == nil {
			return filepath.Join(resolved, tail)
		}
	}
}

// MatchProjectPath returns the projects.path AS STORED for the row that denotes
// dir, and whether one was found. Exact string match first — the indexed fast
// path that every correctly-normalized row takes — then a normalized comparison
// across the table.
//
// The fallback covers rows holding an unresolved path. E-2002's change script
// rewrites those (see RepairProjectPaths), so on a repaired ledger it never
// fires — but a row can be written by hand, restored from an old backup, or
// created by a DB that has not run the change yet, and a lookup that missed in
// those cases would auto-register a duplicate all over again. Ordering by id
// makes the pick deterministic when two rows normalize to the same directory:
// the older row wins, which is the genuine registration rather than the
// auto-registered duplicate.
func MatchProjectPath(db *sql.DB, dir string) (string, bool, error) {
	target := NormalizeProjectPath(dir)

	var stored string
	err := db.QueryRow(
		"SELECT path FROM projects WHERE path = ?", target,
	).Scan(&stored)
	if err == nil {
		return stored, true, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}

	rows, err := db.Query("SELECT path FROM projects ORDER BY id")
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate string
		if err := rows.Scan(&candidate); err != nil {
			return "", false, err
		}
		if NormalizeProjectPath(candidate) == target {
			return candidate, true, nil
		}
	}
	return "", false, rows.Err()
}

// projectIDForNormalizedPath is the legacy-row half of ProjectIDForPath's
// lookup, run only after the indexed walk up dir's ancestors has missed. It
// scans the projects table once, normalizes each stored path, and returns the
// DEEPEST row that is dir or an ancestor of it — the same nearest-enclosing
// -project answer the walk gives, reached without an index.
//
// Deepest, not first, because projects nest: a row for ~/Projects and a row for
// ~/Projects/endless must both be reachable, and a cwd inside the latter
// belongs to the latter. Ties (two rows normalizing to the same directory) go
// to the lower id, matching MatchProjectPath.
func projectIDForNormalizedPath(db *sql.DB, dir string) (int64, bool, error) {
	rows, err := db.Query("SELECT id, path FROM projects ORDER BY id")
	if err != nil {
		return 0, false, err
	}
	defer rows.Close()

	var bestID int64
	bestLen := -1
	for rows.Next() {
		var id int64
		var path string
		if err := rows.Scan(&id, &path); err != nil {
			return 0, false, err
		}
		normalized := NormalizeProjectPath(path)
		if normalized != dir && !strings.HasPrefix(dir, normalized+string(filepath.Separator)) {
			continue
		}
		if len(normalized) > bestLen {
			bestID, bestLen = id, len(normalized)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, false, err
	}
	if bestLen < 0 {
		return 0, false, nil
	}
	return bestID, true, nil
}

// ProjectPathRepair reports what RepairProjectPaths changed.
type ProjectPathRepair struct {
	Merged    int // duplicate rows folded into the row that kept the directory
	Rewritten int // rows whose stored path was replaced by its canonical form
}

// RepairProjectPaths rewrites every projects.path to its canonical form and
// folds away rows that turn out to denote the same directory.
//
// This is the one-shot data half of E-2002, run by
// internal/schema/changes/e-2002-normalize-project-paths.go. It lives here
// rather than in that script so it can be tested; it stays here forever because
// a migration has to keep working on a DB that has never seen it.
//
// The duplicates are real rows with real history: the auto-registered project
// the hook created is what the sessions of that period were bound to, and
// anything auto-imported while it was live hangs off it. So a duplicate is
// MERGED, not deleted — every row referencing it is repointed at the survivor
// first. `projects.path` is UNIQUE, which is what forces the merge to happen
// here rather than being left for later: without it, rewriting the second row's
// path to the first's would simply fail.
//
// The survivor is the LOWEST id in each group — the row that existed before the
// bug produced its twin, i.e. the genuine registration carrying the tasks.
//
// Referencing columns are discovered from the schema at run time
// (`PRAGMA foreign_key_list`) rather than listed here. A hardcoded list would
// be correct on the day it was written and silently wrong the first time a
// table gained a project_id, which is precisely the failure mode a repair
// running unattended over someone's ledger must not have.
func RepairProjectPaths(tx *sql.Tx) (ProjectPathRepair, error) {
	var repair ProjectPathRepair

	type row struct {
		id         int64
		path       string
		normalized string
	}
	rows, err := tx.Query("SELECT id, path FROM projects ORDER BY id")
	if err != nil {
		return repair, fmt.Errorf("reading projects: %w", err)
	}
	var all []row
	for rows.Next() {
		var r row
		if err = rows.Scan(&r.id, &r.path); err != nil {
			rows.Close()
			return repair, fmt.Errorf("scanning projects: %w", err)
		}
		r.normalized = NormalizeProjectPath(r.path)
		all = append(all, r)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return repair, fmt.Errorf("reading projects: %w", err)
	}
	rows.Close()

	refs, err := projectRefColumns(tx)
	if err != nil {
		return repair, err
	}

	// Group by canonical path, preserving id order so the first member of each
	// group is its survivor.
	order := make([]string, 0, len(all))
	groups := make(map[string][]row, len(all))
	for _, r := range all {
		if _, seen := groups[r.normalized]; !seen {
			order = append(order, r.normalized)
		}
		groups[r.normalized] = append(groups[r.normalized], r)
	}

	for _, normalized := range order {
		group := groups[normalized]
		keeper := group[0]

		for _, dup := range group[1:] {
			if err = mergeProjectRow(tx, refs, dup.id, keeper.id); err != nil {
				return repair, err
			}
			repair.Merged++
		}

		if keeper.path != normalized {
			if _, err = tx.Exec(
				"UPDATE projects SET path = ? WHERE id = ?", normalized, keeper.id,
			); err != nil {
				return repair, fmt.Errorf(
					"rewriting project %d path %s -> %s: %w",
					keeper.id, keeper.path, normalized, err,
				)
			}
			repair.Rewritten++
		}
	}

	return repair, nil
}

// projectRef is one column somewhere in the schema that points at projects(id).
type projectRef struct {
	table  string
	column string
}

// projectRefColumns finds every column in the database that is a foreign key
// onto projects(id), by asking SQLite rather than by keeping a list.
func projectRefColumns(tx *sql.Tx) ([]projectRef, error) {
	tables, err := tx.Query(
		"SELECT name FROM sqlite_master WHERE type = 'table' " +
			"AND name NOT LIKE 'sqlite_%' ORDER BY name",
	)
	if err != nil {
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err = tables.Scan(&name); err != nil {
			tables.Close()
			return nil, fmt.Errorf("scanning table name: %w", err)
		}
		names = append(names, name)
	}
	if err = tables.Err(); err != nil {
		tables.Close()
		return nil, fmt.Errorf("listing tables: %w", err)
	}
	tables.Close()

	var refs []projectRef
	for _, name := range names {
		fks, err := tx.Query(
			"SELECT \"table\", \"from\" FROM pragma_foreign_key_list(?)", name,
		)
		if err != nil {
			return nil, fmt.Errorf("reading foreign keys of %s: %w", name, err)
		}
		for fks.Next() {
			var target, column string
			if err = fks.Scan(&target, &column); err != nil {
				fks.Close()
				return nil, fmt.Errorf("scanning foreign key of %s: %w", name, err)
			}
			if target == "projects" {
				refs = append(refs, projectRef{table: name, column: column})
			}
		}
		if err = fks.Err(); err != nil {
			fks.Close()
			return nil, fmt.Errorf("reading foreign keys of %s: %w", name, err)
		}
		fks.Close()
	}
	return refs, nil
}

// mergeProjectRow repoints everything referencing dup at keep, then deletes dup.
//
// Repointing can collide: project_next.project_id is UNIQUE, so a duplicate
// that acquired a curated next list cannot be moved onto a survivor that
// already has one. The survivor's row wins and the duplicate's is dropped —
// the duplicate is the auto-registered twin, and the alternative is to abort
// the whole repair over a per-project scratch list. The DELETE cascades to its
// children, which is why nothing below has to walk them.
func mergeProjectRow(tx *sql.Tx, refs []projectRef, dup, keep int64) error {
	for _, ref := range refs {
		if ref.table == "projects" {
			continue
		}
		update := fmt.Sprintf(
			"UPDATE %q SET %q = ? WHERE %q = ?", ref.table, ref.column, ref.column,
		)
		if _, err := tx.Exec(update, keep, dup); err == nil {
			continue
		}
		del := fmt.Sprintf("DELETE FROM %q WHERE %q = ?", ref.table, ref.column)
		if _, err := tx.Exec(del, dup); err != nil {
			return fmt.Errorf(
				"merging project %d into %d: %s.%s would not repoint and would not drop: %w",
				dup, keep, ref.table, ref.column, err,
			)
		}
	}
	if _, err := tx.Exec("DELETE FROM projects WHERE id = ?", dup); err != nil {
		return fmt.Errorf("deleting merged project %d: %w", dup, err)
	}
	return nil
}
