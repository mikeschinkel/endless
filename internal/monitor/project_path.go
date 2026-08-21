package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// A project path has TWO forms and they must never be confused (ED-1562):
//
//   - the STORED form — home-relative, `~/Projects/acme`, absolute only for a
//     directory outside $HOME. This is what `projects.path` holds and what
//     comparisons run in. It is NOT a filesystem path: Go expands no tilde, so
//     handing one to os.Stat, filepath.Join or git yields `<cwd>/~/Projects/acme`
//     and a mystery much later.
//   - the RESOLVED form — absolute, every symlink component resolved. This is
//     what touches disk.
//
// StoredProjectPath and ResolvedProjectPath are the two accessors, named so the
// call site says which one it holds. Normalize at the BOUNDARIES — the DB read
// and the harness-supplied cwd, done once at the top of `hook claude` — never
// per comparison.
//
// Stored home-relative for legibility: `endless sql` is a supported surface,
// and an ad-hoc query over the database reads better with `~/Projects/acme` than
// with a column of identical 20-character prefixes (E-2011). The byte saving is
// not the reason; it is under a kilobyte.

// ErrNoHomeDir is returned when $HOME cannot be determined. Both forms need it
// — one to expand a stored tilde, the other to decide whether to write one —
// and neither may guess: silently leaving `~` unexpanded produces `<cwd>/~/x`,
// and silently skipping the relativization produces a second spelling of a
// directory that already has a row. So this fails loudly instead (E-2011).
var ErrNoHomeDir = errors.New("cannot determine home directory ($HOME unset?)")

// ResolvedProjectPath returns the RESOLVED form of a project path: absolute,
// with every symlink component resolved, and a leading `~` expanded. Use it for
// anything that touches the filesystem — os.Stat, filepath.Join, a git -C, a cd
// target handed to a session.
//
// The tilde is no longer mere input tolerance: since E-2011 it is the shape
// `projects.path` normally holds, so expanding it here is the read half of the
// storage rule rather than a kindness to hand-edited rows.
//
// This is the Go half of a rule the Python CLI implements identically in
// endless.project_path.resolved (E-2002). The two halves MUST agree: the Python
// CLI writes projects.path, the Go hook reads it on every Claude event, and a
// disagreement makes the hook miss the registered row and auto-register a
// second project for the same directory — leaving the session bound to an empty
// duplicate. That is not hypothetical; it is the bug this pair exists to close,
// hit on any macOS project reached through /var or /tmp (both symlinks into
// /private) and on any user whose projects live under a symlinked parent.
//
// Non-strict, matching pathlib.Path.resolve(): a path that does not exist yet
// is still resolved as far as it does exist, and the missing tail is appended.
// filepath.EvalSymlinks alone fails outright on a missing leaf, which would
// silently leave those paths unresolved and reintroduce the mismatch for
// exactly the directories a register-then-create flow touches first.
//
// Errors ONLY when it must expand a `~` and cannot. A path with no tilde needs
// no home directory and so cannot fail: an unresolvable path is more useful to
// a caller in its absolute form than as an error, and every comparison here
// degrades to the pre-E-2002 behavior rather than breaking.
func ResolvedProjectPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := resolvedHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolving project path %q: %w", p, err)
		}
		p = filepath.Join(home, strings.TrimPrefix(p[1:], "/"))
	}
	return resolveAbs(p), nil
}

// StoredProjectPath returns the STORED form of a project path: the resolved
// form rewritten home-relative with a `~/` prefix, or left absolute when the
// directory is not under $HOME. Use it for every write to projects.path and
// every comparison against it — and for nothing else, because it is a string,
// not a path.
//
// Symlinks are still resolved first: relativizing an unresolved path would make
// the stored spelling depend on how the caller's shell spelled it, which is
// exactly what E-2002 closed.
//
// Mirrors endless.project_path.stored on the Python side.
func StoredProjectPath(p string) (string, error) {
	if p == "" {
		return "", nil
	}
	resolved, err := ResolvedProjectPath(p)
	if err != nil {
		return "", err
	}
	home, err := resolvedHomeDir()
	if err != nil {
		return "", fmt.Errorf("storing project path %q: %w", p, err)
	}
	return homeRelative(resolved, home), nil
}

// resolvedHomeDir is $HOME in the same resolved form project paths take, so the
// prefix test in homeRelative compares like with like. A $HOME reached through
// a symlink (a relocated home directory, a container bind mount) would
// otherwise never prefix-match a resolved project path, and every project would
// silently store absolute.
//
// Not cached: tests set HOME per-case, and the cost is one EvalSymlinks.
func resolvedHomeDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrNoHomeDir, err)
	}
	if home == "" {
		return "", ErrNoHomeDir
	}
	return resolveAbs(home), nil
}

// homeRelative rewrites a RESOLVED path home-relative. `home` must already be
// resolved. The separator in the prefix test is what keeps `/Users/mikey` from
// being read as living inside `/Users/mike`.
func homeRelative(resolved, home string) string {
	if resolved == home {
		return "~"
	}
	if strings.HasPrefix(resolved, home+string(filepath.Separator)) {
		return "~" + resolved[len(home):]
	}
	return resolved
}

// resolveAbs is the symlink-resolving core shared by both forms: absolute, every
// component resolved, non-strict. It knows nothing about `~` — by the time it
// runs, any tilde has already been expanded.
func resolveAbs(p string) string {
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
// dir, and whether one was found. Exact match on the STORED form first — the
// indexed fast path every correctly-written row takes — then a comparison in
// RESOLVED form across the table.
//
// The two forms are deliberate. The fast path compares what the column actually
// holds; the fallback compares what the rows MEAN, so it still matches a row in
// any older spelling — absolute since E-2011 re-pointed the canonical form,
// unresolved since before E-2002. Each ticket's change script rewrites those
// (see RepairProjectPaths), so on a repaired database the fallback never fires —
// but a row can be written by hand, restored from an old backup, or created by
// a DB that has not run the change yet, and a lookup that missed in those cases
// would auto-register a duplicate all over again. Ordering by id makes the pick
// deterministic when two rows denote the same directory: the older row wins,
// which is the genuine registration rather than the auto-registered duplicate.
func MatchProjectPath(db *sql.DB, dir string) (string, bool, error) {
	target, err := StoredProjectPath(dir)
	if err != nil {
		return "", false, err
	}

	var stored string
	err = db.QueryRow(
		"SELECT path FROM projects WHERE path = ?", target,
	).Scan(&stored)
	if err == nil {
		return stored, true, nil
	}
	if err != sql.ErrNoRows {
		return "", false, err
	}

	resolvedTarget, err := ResolvedProjectPath(dir)
	if err != nil {
		return "", false, err
	}
	rows, err := db.Query("SELECT path FROM projects ORDER BY id")
	if err != nil {
		return "", false, err
	}
	defer rows.Close()
	for rows.Next() {
		var candidate string
		if err = rows.Scan(&candidate); err != nil {
			return "", false, err
		}
		resolved, rerr := ResolvedProjectPath(candidate)
		if rerr != nil {
			return "", false, rerr
		}
		if resolved == resolvedTarget {
			return candidate, true, nil
		}
	}
	return "", false, rows.Err()
}

// projectIDForResolvedPath is the legacy-row half of ProjectIDForPath's lookup,
// run only after the indexed walk up dir's ancestors has missed. It scans the
// projects table once, resolves each stored path, and returns the DEEPEST row
// that is dir or an ancestor of it — the same nearest-enclosing-project answer
// the walk gives, reached without an index.
//
// dir arrives already RESOLVED, and each candidate is resolved to match: this is
// the comparison that has to see through every stored spelling at once, so it
// runs in the form they all mean rather than the form they are written in.
//
// Deepest, not first, because projects nest: a row for ~/Projects and a row for
// ~/Projects/endless must both be reachable, and a cwd inside the latter
// belongs to the latter. Ties (two rows denoting the same directory) go to the
// lower id, matching MatchProjectPath.
func projectIDForResolvedPath(db *sql.DB, dir string) (int64, bool, error) {
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
		if err = rows.Scan(&id, &path); err != nil {
			return 0, false, err
		}
		resolved, rerr := ResolvedProjectPath(path)
		if rerr != nil {
			return 0, false, rerr
		}
		if resolved != dir && !strings.HasPrefix(dir, resolved+string(filepath.Separator)) {
			continue
		}
		if len(resolved) > bestLen {
			bestID, bestLen = id, len(resolved)
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

// RepairProjectPaths rewrites every projects.path to its canonical STORED form
// and folds away rows that turn out to denote the same directory.
//
// This is the one-shot data half of E-2002, run by
// internal/schema/changes/e-2002-normalize-project-paths.go, and re-run by
// internal/schema/changes/e-2011-home-relative-project-paths.go once E-2011
// re-pointed the canonical form from absolute to home-relative. One function
// serves both because "canonical" is defined in exactly one place —
// StoredProjectPath — so a database that has run either change ends in whatever
// spelling the current build calls canonical, and a database that runs both in
// sequence is not rewritten twice for nothing. It lives here rather than in
// those scripts so it can be tested; it stays here forever because a migration
// has to keep working on a DB that has never seen it.
//
// Grouping is by RESOLVED form and rewriting is to STORED form: two rows denote
// the same directory when they resolve to the same place, whatever spelling
// each is written in.
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
// running unattended over someone's database must not have.
func RepairProjectPaths(tx *sql.Tx) (ProjectPathRepair, error) {
	var repair ProjectPathRepair

	type row struct {
		id       int64
		path     string
		resolved string // identity: which directory this row denotes
		stored   string // what the path column should hold
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
		if r.resolved, err = ResolvedProjectPath(r.path); err != nil {
			rows.Close()
			return repair, fmt.Errorf("resolving project %d path %s: %w", r.id, r.path, err)
		}
		if r.stored, err = StoredProjectPath(r.path); err != nil {
			rows.Close()
			return repair, fmt.Errorf("canonicalizing project %d path %s: %w", r.id, r.path, err)
		}
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

	// Group by the directory each row denotes, preserving id order so the first
	// member of each group is its survivor.
	order := make([]string, 0, len(all))
	groups := make(map[string][]row, len(all))
	for _, r := range all {
		if _, seen := groups[r.resolved]; !seen {
			order = append(order, r.resolved)
		}
		groups[r.resolved] = append(groups[r.resolved], r)
	}

	for _, resolved := range order {
		group := groups[resolved]
		keeper := group[0]

		for _, dup := range group[1:] {
			if err = mergeProjectRow(tx, refs, dup.id, keeper.id); err != nil {
				return repair, err
			}
			repair.Merged++
		}

		if keeper.path != keeper.stored {
			if _, err = tx.Exec(
				"UPDATE projects SET path = ? WHERE id = ?", keeper.stored, keeper.id,
			); err != nil {
				return repair, fmt.Errorf(
					"rewriting project %d path %s -> %s: %w",
					keeper.id, keeper.path, keeper.stored, err,
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
