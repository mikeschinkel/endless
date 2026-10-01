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
// unresolved since before E-2002. Those tickets rewrote the rows that existed
// then, so on such a database the fallback never fires — but a row can be
// written by hand or restored from an old backup, and a lookup that missed in
// those cases would auto-register a duplicate all over again. Ordering by id makes the pick
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
