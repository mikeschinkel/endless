package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A directory is "not a project" when a projects row for it carries
// ProjectStatusIgnored, or when it holds an IgnoreMarker file (E-2251). Either
// covers the directory's whole subtree, and the NEAREST answer wins: resolving a
// directory walks up from it, and the first rung holding a projects row or a
// marker decides. So `~/Projects` ignored and `~/Projects/endless` registered
// coexist — a cwd inside endless reaches endless's row first, and a new
// `~/Projects/foo` reaches the ignored row first and is never auto-registered.
//
// Ignoring is machine-local registry state, like the projects row it lives on:
// a path is a fact about this machine, a non-project directory has no ledger to
// record it in, and writing one into a project's committed ledger would publish
// the user's directory layout to everyone who clones that project. So these
// writes go straight to the database, the way auto-registration always has.

// ProjectStatusIgnored marks a projects row as "this directory is not a project,
// do not register it or anything under it". `project unregister` and `purge`
// set it on a former project's row; `project ignore` sets it on any directory.
const ProjectStatusIgnored = "ignored"

// IgnoreMarker is the on-disk twin of an ignored row: a file a user can drop in
// a directory to say "Endless, leave this alone" without touching the database.
// Its content is ignored (a free-text reason is welcome). At the same rung as a
// projects row it wins, because a marker is the more deliberate act — someone
// put it on disk after the row existed.
const IgnoreMarker = ".endless-ignore"

// ErrIgnoredDirectory is returned by ProjectIDForPath for a directory that is,
// or sits under, an ignored one. It is not a failure: callers that record
// activity treat it as "nothing to record here".
var ErrIgnoredDirectory = errors.New("directory is ignored by Endless")

// DirVerdict kinds.
const (
	VerdictProject    = "project"
	VerdictIgnored    = "ignored"
	VerdictNone       = "none"
	VerdictDisallowed = "disallowed" // E-2369: a rule says it can never be a project
)

// DirVerdict is what resolving one directory concluded.
type DirVerdict struct {
	Path string `json:"path"` // the directory asked about, resolved
	Kind string `json:"kind"` // VerdictProject, VerdictIgnored or VerdictNone

	// The rung that decided: its stored path, and for a row its id and name.
	// Empty for VerdictNone.
	At        string `json:"at,omitempty"`
	ProjectID int64  `json:"project_id,omitempty"`
	Name      string `json:"name,omitempty"`

	// Marker is true when an IgnoreMarker file, not a row, decided.
	Marker bool `json:"marker,omitempty"`

	// For VerdictDisallowed (E-2369): why, and whether the rule covers the
	// directory alone (ScopeSelf) or everything under it (ScopeSubtree). A
	// walk that skips SELF-disallowed directories may still descend into them.
	Rule  string `json:"rule,omitempty"`
	Scope string `json:"scope,omitempty"`

	// Within is true for VerdictProject when the project is an ANCESTOR's, not
	// the directory's own row: registering the directory would nest a project.
	Within bool `json:"within,omitempty"`

	// Encloses is the stored path of a registered project INSIDE the
	// directory, set for VerdictNone and for a directory's own project row:
	// registering the directory would nest that project inside it.
	Encloses string `json:"encloses,omitempty"`
}

// ResolveDirectory answers whether dir belongs to a project, is ignored, is
// disallowed, or is none of these — the one implementation every registration
// path consults (the hook's auto-registration here, and Python's register,
// discover and reconcile through `endless-go project resolve`).
//
// dir arrives RESOLVED. The walk climbs resolved ancestors and queries each in
// STORED form (E-2011); only when every rung misses does it fall back to a scan
// for a row written in an older spelling, which cannot be outranked by a marker
// because the walk already looked for one at every rung.
//
// The disallowed rules (E-2369, disallowed_paths.go) shape the walk: a
// directory inside a SUBTREE rule is disallowed whatever rows sit nearer; a
// SELF rule refuses that directory and hides any row at that rung from the
// directories below it; a parent directory ends the walk, so nothing below it
// inherits a project registered above.
func ResolveDirectory(db *sql.DB, dir string) (DirVerdict, error) {
	v := DirVerdict{Path: dir, Kind: VerdictNone}
	home, err := resolvedHomeDir()
	if err != nil {
		return v, err
	}
	rules, err := loadDirRules(home)
	if err != nil {
		return v, err
	}
	if at, r, ok := rules.subtreeRoot(dir); ok {
		return disallowed(v, homeRelative(at, home), r), nil
	}

	floor := ""
	for check := dir; ; {
		stored := homeRelative(check, home)
		if hasIgnoreMarker(check) {
			v.Kind, v.At, v.Marker = VerdictIgnored, stored, true
			return v, nil
		}
		r, ruled := rules[pathKey(check)]
		if hasParentDirMarker(check) {
			r, ruled = dirRule{Scope: ScopeSelf, ParentDir: true, Why: whyParentDir}, true
		}
		if ruled {
			if check == dir {
				return disallowed(v, stored, r), nil
			}
			if r.ParentDir {
				floor = check
				break
			}
		} else {
			var id int64
			var name, status string
			err = db.QueryRow(
				"SELECT id, name, status FROM projects WHERE path = ?", stored,
			).Scan(&id, &name, &status)
			if err == nil {
				v = rowVerdict(v, stored, id, name, status)
				v.Within = v.Kind == VerdictProject && check != dir
				return withEncloses(db, v, dir, home, rules)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return v, err
			}
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}

	row, found, err := deepestRowForResolvedPath(db, dir, floor, rules)
	if err != nil {
		return v, err
	}
	if found {
		resolved, rerr := ResolvedProjectPath(row.path)
		if rerr != nil {
			return v, rerr
		}
		v = rowVerdict(v, row.path, row.id, row.name, row.status)
		v.Within = v.Kind == VerdictProject && resolved != dir
	}
	return withEncloses(db, v, dir, home, rules)
}

func disallowed(v DirVerdict, at string, r dirRule) DirVerdict {
	v.Kind, v.At, v.Rule, v.Scope = VerdictDisallowed, at, r.Why, r.Scope
	if r.ParentDir {
		v.Rule = "a parent directory: " + r.Why
	}
	return v
}

// withEncloses fills Encloses when dir is unregistered or is its own project:
// the cases where registering it, or keeping it, would hold another project.
// Rows that can never resolve — ignored, or at a disallowed path — don't count.
func withEncloses(db *sql.DB, v DirVerdict, dir, home string, rules dirRules) (DirVerdict, error) {
	if v.Kind != VerdictNone && (v.Kind != VerdictProject || v.Within) {
		return v, nil
	}
	rows, err := db.Query(
		"SELECT path FROM projects WHERE status != ? ORDER BY path", ProjectStatusIgnored,
	)
	if err != nil {
		return v, err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err = rows.Scan(&p); err != nil {
			return v, err
		}
		resolved, rerr := ResolvedProjectPath(p)
		if rerr != nil {
			return v, rerr
		}
		if !strings.HasPrefix(resolved, dir+string(filepath.Separator)) {
			continue
		}
		if _, ruled := rules[pathKey(resolved)]; ruled {
			continue
		}
		v.Encloses = homeRelative(resolved, home)
		break
	}
	return v, rows.Err()
}

func rowVerdict(v DirVerdict, at string, id int64, name, status string) DirVerdict {
	v.At, v.ProjectID, v.Name = at, id, name
	if status == ProjectStatusIgnored {
		v.Kind = VerdictIgnored
	} else {
		v.Kind = VerdictProject
	}
	return v
}

func hasParentDirMarker(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, ParentDirMarker))
	return err == nil && !st.IsDir()
}

func hasIgnoreMarker(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, IgnoreMarker))
	return err == nil && !st.IsDir()
}

type projectRow struct {
	id                 int64
	name, path, status string
}

// deepestRowForResolvedPath is the legacy-spelling half of ResolveDirectory: it
// scans every row once, resolves each stored path, and returns the DEEPEST row
// that is dir or an ancestor of it — the nearest-enclosing answer the walk
// gives, reached without the index. Deepest, not first, because rows nest.
// Ties (two rows denoting one directory) go to the lower id.
//
// It honors the same rules as the walk: a row at a disallowed path never
// matches, and none at or above floor — the parent directory that ended the
// walk — does.
func deepestRowForResolvedPath(db *sql.DB, dir, floor string, rules dirRules) (projectRow, bool, error) {
	var best projectRow
	rows, err := db.Query("SELECT id, name, path, status FROM projects ORDER BY id")
	if err != nil {
		return best, false, err
	}
	defer rows.Close()

	bestLen := -1
	for rows.Next() {
		var r projectRow
		if err = rows.Scan(&r.id, &r.name, &r.path, &r.status); err != nil {
			return best, false, err
		}
		resolved, rerr := ResolvedProjectPath(r.path)
		if rerr != nil {
			return best, false, rerr
		}
		if resolved != dir && !strings.HasPrefix(dir, resolved+string(filepath.Separator)) {
			continue
		}
		if _, ruled := rules[pathKey(resolved)]; ruled {
			continue
		}
		if floor != "" && len(resolved) <= len(floor) {
			continue
		}
		if len(resolved) > bestLen {
			best, bestLen = r, len(resolved)
		}
	}
	if err := rows.Err(); err != nil {
		return best, false, err
	}
	return best, bestLen >= 0, nil
}

// SetDirectoryIgnored records dir as ignored. An existing row — a former
// project's, keeping its name, tasks and history — has its status changed; a
// directory with no row gets one named by its stored path, which can never
// collide with a real project's name (names carry no slash).
//
// Returns the stored path written and whether a row already existed.
func SetDirectoryIgnored(db *sql.DB, dir string) (stored string, existed bool, err error) {
	return setDirectoryStatus(db, dir, ProjectStatusIgnored)
}

// SetDirectoryActive re-activates dir's existing row. Refused when there is no
// row: creating a project is `project register`'s job, with everything it
// infers, not a bare status write.
func SetDirectoryActive(db *sql.DB, dir string) (stored string, err error) {
	stored, existed, err := setDirectoryStatus(db, dir, "active")
	if err == nil && !existed {
		err = fmt.Errorf("%w: no row for %s", ErrNoProject, stored)
	}
	return stored, err
}

func setDirectoryStatus(db *sql.DB, dir, status string) (stored string, existed bool, err error) {
	stored, err = StoredProjectPath(dir)
	if err != nil {
		return "", false, err
	}
	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	res, err := db.Exec(
		"UPDATE projects SET status = ?, updated_at = ? WHERE path = ?",
		status, now, stored,
	)
	if err != nil {
		return stored, false, fmt.Errorf("setting %s to %s: %w", stored, status, err)
	}
	if n, _ := res.RowsAffected(); n > 0 {
		return stored, true, nil
	}
	if status != ProjectStatusIgnored {
		return stored, false, nil
	}
	if _, err = db.Exec(
		"INSERT INTO projects (name, path, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
		stored, stored, status, now, now,
	); err != nil {
		return stored, false, fmt.Errorf("recording %s as ignored: %w", stored, err)
	}
	return stored, false, nil
}

// ClearIgnoredDirectory deletes dir's ignored row. Only a row that is ignored
// AND has nothing hanging off it is deleted outright — a former project's row
// keeps its history, so it is refused here and re-activated with `project
// register` instead.
//
// "Nothing hanging off it" means no row in ANY table that references projects
// (see projectReferences), not merely no tasks. The tasks-only guard this
// replaces let a project with sessions, activity, notes or decisions but no
// tasks through, and eight of those ten references are ON DELETE CASCADE, so
// the delete took all of it (the other two are ON DELETE SET NULL, which
// loses the attribution instead of the row). That is the same mechanism that cost 195 sessions
// and 1,616 activity rows, reached by a different route (E-2010, ED-1547).
func ClearIgnoredDirectory(db *sql.DB, dir string) (stored string, err error) {
	stored, err = StoredProjectPath(dir)
	if err != nil {
		return "", err
	}
	var id int64
	var status string
	err = db.QueryRow("SELECT id, status FROM projects WHERE path = ?", stored).Scan(&id, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return stored, fmt.Errorf("%w: %s is not recorded as ignored", ErrNoProject, stored)
	}
	if err != nil {
		return stored, err
	}
	if status != ProjectStatusIgnored {
		return stored, fmt.Errorf("%w: %s", ErrNotIgnored, stored)
	}
	held, err := projectReferrers(db, id)
	if err != nil {
		return stored, err
	}
	if len(held) > 0 {
		return stored, fmt.Errorf("%w: %s still holds %s",
			ErrIgnoredHasHistory, stored, strings.Join(held, ", "))
	}
	if _, err = db.Exec("DELETE FROM projects WHERE id = ?", id); err != nil {
		return stored, fmt.Errorf("clearing %s: %w", stored, err)
	}
	return stored, nil
}

// projectReferences is every table that refers to a projects row, with the
// column that does the referring. It is the guard list for the one remaining
// hard delete of a project, and it is deliberately the WHOLE set rather than a
// judgement about which rows matter: ON DELETE CASCADE does not distinguish
// either, so any table left off this list is a table the delete silently
// empties. project_deps appears twice because either end of a dependency is a
// reference.
//
// Keep it in step with schema.sql. A new table referencing projects(id) that is
// not listed here reopens exactly the hole E-2010 closed, which is why
// TestProjectReferences_CoversEverySchemaReference reads the foreign keys out
// of the live schema and fails when the two disagree.
var projectReferences = []struct {
	table  string
	column string
	label  string
}{
	{table: "tasks", column: "project_id", label: "task"},
	{table: "decisions", column: "project_id", label: "decision"},
	{table: "sessions", column: "project_id", label: "session"},
	{table: "activity", column: "project_id", label: "activity row"},
	{table: "notes", column: "project_id", label: "note"},
	{table: "project_deps", column: "project_id", label: "dependency"},
	{table: "project_deps", column: "depends_on_id", label: "dependent"},
	{table: "shipped_file_writes", column: "project_id", label: "shipped-file record"},
	{table: "errors", column: "project_id", label: "error"},
	{table: "fault_triage_projects", column: "project_id", label: "fault-triage row"},
}

// projectReferrers counts the rows that refer to project id, as phrases ready
// to join into a refusal ("3 tasks", "1 session"). An empty result means the
// row is a bare ignore marker that nothing points at, which is the only kind
// this package deletes outright.
//
// It counts rather than short-circuits on the first hit so the refusal can name
// everything the delete would have taken. Ten counts, on a command a human runs
// by hand against one project id.
func projectReferrers(db *sql.DB, id int64) ([]string, error) {
	var held []string
	for _, ref := range projectReferences {
		var n int
		err := db.QueryRow(fmt.Sprintf(
			"SELECT count(*) FROM %s WHERE %s = ?", ref.table, ref.column,
		), id).Scan(&n)
		if err != nil {
			return nil, fmt.Errorf("counting %s.%s for project %d: %w",
				ref.table, ref.column, id, err)
		}
		if n == 0 {
			continue
		}
		held = append(held, fmt.Sprintf("%d %s", n, plural(n, ref.label)))
	}
	return held, nil
}

// ErrNotIgnored refuses clearing a row that is a registered project.
var ErrNotIgnored = errors.New("a registered project, not an ignored directory")

// ErrIgnoredHasHistory refuses deleting an ignored row that is a former
// project's, because something still references it. See projectReferences for
// what counts.
var ErrIgnoredHasHistory = errors.New("ignored directory was a project and still holds history")
