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
	VerdictProject = "project"
	VerdictIgnored = "ignored"
	VerdictNone    = "none"
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
}

// ResolveDirectory answers whether dir belongs to a project, is ignored, or is
// neither — the one implementation every registration path consults (the hook's
// auto-registration here, and Python's register, discover and reconcile through
// `endless-go project resolve`).
//
// dir arrives RESOLVED. The walk climbs resolved ancestors and queries each in
// STORED form (E-2011); only when every rung misses does it fall back to a scan
// for a row written in an older spelling, which cannot be outranked by a marker
// because the walk already looked for one at every rung.
func ResolveDirectory(db *sql.DB, dir string) (DirVerdict, error) {
	v := DirVerdict{Path: dir, Kind: VerdictNone}
	home, err := resolvedHomeDir()
	if err != nil {
		return v, err
	}

	for check := dir; ; {
		stored := homeRelative(check, home)
		if hasIgnoreMarker(check) {
			v.Kind, v.At, v.Marker = VerdictIgnored, stored, true
			return v, nil
		}
		var id int64
		var name, status string
		err = db.QueryRow(
			"SELECT id, name, status FROM projects WHERE path = ?", stored,
		).Scan(&id, &name, &status)
		if err == nil {
			return rowVerdict(v, stored, id, name, status), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return v, err
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}

	row, found, err := deepestRowForResolvedPath(db, dir)
	if err != nil || !found {
		return v, err
	}
	return rowVerdict(v, row.path, row.id, row.name, row.status), nil
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
func deepestRowForResolvedPath(db *sql.DB, dir string) (projectRow, bool, error) {
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
// keeps its tasks, so it is refused here and re-activated with `project
// register` instead.
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
	var tasks int
	if err = db.QueryRow("SELECT count(*) FROM tasks WHERE project_id = ?", id).Scan(&tasks); err != nil {
		return stored, err
	}
	if tasks > 0 {
		return stored, fmt.Errorf("%w: %s has %d task(s)", ErrIgnoredHasHistory, stored, tasks)
	}
	if _, err = db.Exec("DELETE FROM projects WHERE id = ?", id); err != nil {
		return stored, fmt.Errorf("clearing %s: %w", stored, err)
	}
	return stored, nil
}

// ErrNotIgnored refuses clearing a row that is a registered project.
var ErrNotIgnored = errors.New("a registered project, not an ignored directory")

// ErrIgnoredHasHistory refuses deleting an ignored row that is a former
// project's, because its tasks reference it.
var ErrIgnoredHasHistory = errors.New("ignored directory was a project and still holds tasks")
