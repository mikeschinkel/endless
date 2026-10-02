package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/sessionstate"
)

// The reads behind `endless project status` / `endless project monitor`
// (E-1976, rebuilt around tasks by E-2156).
//
// One row per TASK. Sessions are consulted and never listed: a task a live
// session holds says so through its row's glyph, which is the one fact about a
// session these views need — whether claimed work is still being worked or has
// stalled. A session holding no task has no row at all; such rows were
// unactionable, and followed up led to panes that no longer existed.
//
// Which tasks: every non-terminal task in the given phases. The caller passes
// urgent/now/next for the default view and later for `project status --later`.
// Grouping into lists and ordering within them are rendering decisions and live
// with the renderer, in projectstatuscmd.

// ProjectStatusRow is one task as `endless project status` prints it and
// `endless project monitor` repaints it.
type ProjectStatusRow struct {
	ProjectID int64
	TaskID    int64
	Title     string
	Status    string
	Phase     string
	TypeSlug  string
	// TaskUpdated is tasks.updated_at, the clock `--sort updated` orders by.
	TaskUpdated string
	// Landed is true when the task has a task_landings row — its work merged
	// while its status is still open. Same fact, same glyph (⏚) as `session
	// status`.
	Landed bool
	// LiveSession is true when a live session holds this task: a session in a
	// sessionstate.Live state whose liveness was not observed `dead`.
	LiveSession bool
	// Prompted is true when one of those live sessions is blocked on a
	// permission prompt (E-2091) — the same fact as LiveSession at a higher
	// urgency.
	Prompted bool
	// Primed is true when one of those live sessions is `primed` (E-1994): it
	// read the task in ahead of need and is holding for the user.
	Primed bool
}

// ErrNoProject is returned when a project cannot be resolved — by name because
// no such row exists, or from a directory that is not inside a registered
// project. Callers turn it into a message; it is never a crash, because "you
// are not in a project" is an ordinary thing for a user to be.
var ErrNoProject = errors.New("no such project")

// ProjectByName resolves a registered project by its `name` column.
//
// Read-only by design. ProjectIDForPath, the other resolver in this package,
// auto-registers an anonymous project when the walk misses — correct for a hook
// that must record activity somewhere, catastrophic for a read view, which
// would silently mint a project row every time someone ran it in the wrong
// directory.
func ProjectByName(name string) (id int64, resolved string, err error) {
	db, err := DB()
	if err != nil {
		return 0, "", err
	}
	err = db.QueryRow(
		"SELECT id, name FROM projects WHERE name = ?", name,
	).Scan(&id, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("%w: %q", ErrNoProject, name)
	}
	if err != nil {
		return 0, "", err
	}
	return id, resolved, nil
}

// ProjectForCwd resolves the project enclosing the working directory.
//
// It WALKS UP, which is the whole point: `project status` is most often run from
// a per-task worktree (.endless/worktrees/e-NNN) or from some subdirectory of a
// checkout, and only the checkout root carries a projects row. Matching the
// literal cwd alone — which is all MatchProjectPath does — reports "not inside a
// registered project" from inside the project.
//
// The walk climbs RESOLVED ancestors (the only form filepath.Dir can climb) and
// queries each in STORED form, which is what the indexed column holds (E-2011);
// only when every rung misses does it fall back to MatchProjectPath's scan for a
// row written in an older spelling.
//
// Read-only, on the same rule as ProjectByName: the otherwise-identical
// ProjectIDForPath auto-registers an anonymous project when the walk misses,
// which is right for a hook that must record activity somewhere and catastrophic
// for a read view, which would mint a project row every time someone ran it in
// the wrong directory.
func ProjectForCwd() (id int64, name string, err error) {
	db, err := DB()
	if err != nil {
		return 0, "", err
	}
	cwd, err := os.Getwd()
	if err != nil {
		return 0, "", err
	}
	dir, err := ResolvedProjectPath(cwd)
	if err != nil {
		return 0, "", err
	}
	home, err := resolvedHomeDir()
	if err != nil {
		return 0, "", err
	}

	for check := dir; ; {
		err = db.QueryRow(
			"SELECT id, name FROM projects WHERE path = ? ORDER BY id LIMIT 1",
			homeRelative(check, home),
		).Scan(&id, &name)
		if err == nil {
			return id, name, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, "", err
		}
		parent := filepath.Dir(check)
		if parent == check {
			break
		}
		check = parent
	}

	stored, ok, err := MatchProjectPath(db, dir)
	if err != nil {
		return 0, "", err
	}
	if ok {
		err = db.QueryRow(
			"SELECT id, name FROM projects WHERE path = ? ORDER BY id LIMIT 1", stored,
		).Scan(&id, &name)
		if err == nil {
			return id, name, nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return 0, "", err
		}
	}
	return 0, "", fmt.Errorf("%w: no registered project encloses %s", ErrNoProject, cwd)
}

// ProjectStatusRows returns every non-terminal task of one project in the
// given phases, each carrying whether a live session holds it.
//
// Unordered: projectstatuscmd sorts, because the sort key is a flag.
func ProjectStatusRows(projectID int64, phases []string) ([]ProjectStatusRow, error) {
	if len(phases) == 0 {
		return nil, nil
	}
	db, err := DB()
	if err != nil {
		return nil, err
	}

	// RefreshLiveness, not livenessReady: `project monitor` is a repeating view,
	// and livenessReady's sync.Once would freeze the observation at the first
	// frame — so a session that ended while the monitor was open would keep its
	// task reading ⟳ forever. A one-shot render pays the same single observation
	// either way.
	//
	// Reaching nothing is not fatal: RefreshLiveness's contract is that an
	// unreachable server means `unknown`, not `dead`, so a stale snapshot
	// over-reports live sessions rather than calling held work stalled.
	if err = RefreshLiveness(); err != nil {
		return nil, err
	}

	ph, args := stringPlaceholders(phases)

	// The live-session subqueries mirror the filter ListLiveSessions applies:
	// a Live state, and liveness not observed `dead` (`unknown` stays, because
	// "could not disprove" is not "gone" — E-1898). A hidden session still
	// counts: `session hide` asks not to SEE a session, and hiding one must not
	// make the work it holds read as stalled.
	liveHolder := `
	    FROM sessions s
	    JOIN session_liveness sl ON sl.session_id = s.id
	   WHERE s.task_id = t.id
	     AND s.state IN (` + sessionstate.SQLList(sessionstate.Live) + `)
	     AND sl.liveness != 'dead'`

	query := `
		SELECT t.id, t.title, t.status, t.phase,
		       COALESCE(ty.slug, ''), COALESCE(t.updated_at, ''),
		       EXISTS(SELECT 1 FROM task_landings tl WHERE tl.task_id = t.id),
		       EXISTS(SELECT 1 ` + liveHolder + `),
		       EXISTS(SELECT 1 ` + liveHolder + ` AND s.state = '` + string(sessionstate.Prompted) + `'),
		       EXISTS(SELECT 1 ` + liveHolder + ` AND s.state = '` + string(sessionstate.Primed) + `')
		  FROM live_tasks t
		  LEFT JOIN task_types ty ON ty.id = t.type_id
		 WHERE t.project_id = ?
		   AND t.status NOT IN (` + terminalStatusSet + `)
		   AND t.phase IN (` + ph + `)`
	rows, err := db.Query(query, append([]any{projectID}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("project status tasks: %w", err)
	}
	defer rows.Close()

	var out []ProjectStatusRow
	for rows.Next() {
		r := ProjectStatusRow{ProjectID: projectID}
		if err = rows.Scan(
			&r.TaskID, &r.Title, &r.Status, &r.Phase, &r.TypeSlug, &r.TaskUpdated,
			&r.Landed, &r.LiveSession, &r.Prompted, &r.Primed,
		); err != nil {
			return nil, fmt.Errorf("project status tasks: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// stringPlaceholders is intPlaceholders for strings: "?,?,…" and the matching
// args for a dynamic SQL `IN` clause.
func stringPlaceholders(vals []string) (string, []any) {
	ph := make([]string, len(vals))
	args := make([]any, len(vals))
	for i, v := range vals {
		ph[i] = "?"
		args[i] = v
	}
	return strings.Join(ph, ","), args
}

// SanitizeTmuxName folds a project name into something tmux will accept as part
// of a session name.
//
// tmux forbids '.' and ':' in a session name (both are target separators) and
// treats a leading '-' as a flag, so every character outside [A-Za-z0-9_-] is
// folded to '-'. PRODUCT: project names are user-chosen and arrive with spaces,
// dots and slashes in them; this must not be a rule the user has to know.
//
// A name that folds away to nothing yields "project", so the monitor stays
// reachable rather than the launcher refusing over a naming detail.
func SanitizeTmuxName(project string) string {
	var b strings.Builder
	for _, r := range project {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		return "project"
	}
	return name
}

// ProjectNameByID resolves a project's name from its id. Used by the headless
// --project-id path, which names the project explicitly without going through the
// name or cwd resolvers.
func ProjectNameByID(id int64) (int64, string, error) {
	db, err := DB()
	if err != nil {
		return 0, "", err
	}
	var name string
	err = db.QueryRow("SELECT name FROM projects WHERE id = ?", id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, "", fmt.Errorf("%w: id %d", ErrNoProject, id)
	}
	if err != nil {
		return 0, "", err
	}
	return id, name, nil
}
