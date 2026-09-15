package monitor

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// The reads behind `endless project status` / `endless project monitor`
// (E-1976).
//
// This file answers ONE question — "what in this project is claiming a person's
// attention right now?" — and it answers it in two halves that the caller merges
// into one row set:
//
//  1. Every live session in the project. A session is an agent that is running,
//     has stopped mid-turn, or has finished a turn; all three are things a
//     person may need to look at.
//  2. Every task in taskstatus.AwaitsUser — work that has stopped and is waiting
//     on a person — plus every `underway` task, which is how an ORPHAN becomes
//     visible: work that was claimed and whose session is gone.
//
// The merge belongs in Go rather than in a UNION because the two halves overlap
// on exactly one key (a session's claimed task) and the overlap is a JOIN of
// facts, not a concatenation of rows: a session working E-1976 and the task
// E-1976 are ONE thing on the board, not two lines saying the same thing twice.
//
// Unlike SessionStatusRows this set is PROJECT-scoped, not focal-task-scoped,
// and it is deliberately not a superset: `ready` arrives only under all=true,
// because spawnable work claims capacity, not attention.

// ProjectStatusRow is one row of what `endless project status` prints and
// `endless project monitor` repaints. Every row carries a task, a session, or
// both — never neither.
//
// The zero value of each half is its "absent" marker (TaskID == 0, SessionID ==
// 0), which is why neither is a pointer: a board row is read a dozen times per
// repaint by width, sort and legend code, and a nil check at each of those is a
// nil panic waiting for the one row shaped differently than the author pictured.
type ProjectStatusRow struct {
	ProjectID int64

	// --- the task half (zero when a live session has claimed nothing) ---
	TaskID   int64
	Title    string
	Status   string
	Phase    string
	TypeSlug string
	// TaskUpdated is tasks.updated_at: when the task last changed, which for a
	// row on this board is when it entered the status that put it here. It is
	// the staleness clock — an `unverified` row updated 80 days ago is sediment,
	// not a queue entry.
	TaskUpdated string

	// --- the session half (zero when the row is a task nobody is holding) ---
	SessionID int64
	// SessionState is any member of sessionstate.Live. 'ended' never reaches
	// here: an ended session has stopped claiming anything.
	SessionState string
	// SessionActivity is sessions.last_activity, falling back to started_at when
	// a session has not yet had a turn. It is the WAITING clock — how long this
	// session has been in its current state, i.e. how long it has been holding
	// something of yours.
	SessionActivity string
}

// HasTask and HasSession report which halves a row carries.
func (r ProjectStatusRow) HasTask() bool    { return r.TaskID != 0 }
func (r ProjectStatusRow) HasSession() bool { return r.SessionID != 0 }

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
// It WALKS UP, which is the whole point: the board is most often run from a
// per-task worktree (.endless/worktrees/e-NNN) or from some subdirectory of a
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

// boardTaskStatuses is the status set a task must be in to earn a board row
// without a session behind it.
//
// AwaitsUser is the ball-is-in-your-court set; `underway` joins it here and
// only here, because an underway task with no live session is an ORPHAN — work
// somebody started and walked away from — which is a genuine attention claim
// that no status of its own describes. An underway task WITH a live session is
// merged onto that session's row instead and never renders as an orphan.
func boardTaskStatuses(all bool) string {
	list := taskstatus.SQLList(taskstatus.AwaitsUser) + ",'" + string(taskstatus.Underway) + "'"
	if all {
		// `ready` is spawnable work: a claim on CAPACITY, not on attention. It
		// is off the default board for the same reason `session status` keeps
		// terminal rows behind --all — including it by default would bury the
		// rows that actually need a person under the ones that need a session.
		list += ",'" + string(taskstatus.Ready) + "'"
	}
	return list
}

// ProjectStatusRows returns the merged attention row set for one project.
//
// Sessions are read first so a task claimed by a live session is folded into
// that session's row rather than emitted twice. Ordering is NOT applied here:
// the board ranks and sorts per group, which is a rendering decision and lives
// with the renderer.
func ProjectStatusRows(projectID int64, all bool) ([]ProjectStatusRow, error) {
	db, err := DB()
	if err != nil {
		return nil, err
	}

	// RefreshLiveness, not livenessReady: the board is a repeating view whose
	// whole premise is which sessions are alive RIGHT NOW, and livenessReady's
	// sync.Once would freeze that observation at the first frame — so a session
	// that ended while the board was open would never leave it, and one that
	// started would never appear. A one-shot render pays the same single
	// observation either way, so there is no flag and no second entry point.
	//
	// A failed observation is not fatal. RefreshLiveness's own contract is that
	// reaching nothing means `unknown`, not `dead`, so a board rendered against a
	// stale snapshot over-includes rather than hiding live work.
	if err = RefreshLiveness(); err != nil {
		return nil, err
	}

	rows, err := projectSessionRows(db, projectID)
	if err != nil {
		return nil, err
	}

	claimed := make(map[int64]bool, len(rows))
	for _, r := range rows {
		if r.HasTask() {
			claimed[r.TaskID] = true
		}
	}

	taskRows, err := projectTaskRows(db, projectID, all)
	if err != nil {
		return nil, err
	}
	for _, r := range taskRows {
		if claimed[r.TaskID] {
			continue
		}
		rows = append(rows, r)
	}
	return rows, nil
}

// projectSessionRows reads every live session in the project, with whatever task
// it claimed joined on.
//
// Three filters, each answering a different question:
//
//   - state — sessionstate.Live, the whole of it. E-1976 shipped a narrower
//     local set that excluded `needs_input`, on the honest reading that nothing
//     transitioned a session INTO that state, so every row carrying it was a
//     session that registered and never had a turn — 34 of them in one project,
//     each last active 25 to 71 days earlier. Hiding them is how they rotted
//     unseen. E-2091 reveals them instead and fixes the writers that made them:
//     the board already caps each rank at ten rows with a footer naming the
//     remainder, which is machinery built for exactly this, and a row on the
//     board is what provides the mechanism to resolve it. The existing rows are
//     surfaced, not migrated — retiring them in the dark is the opposite of what
//     revealing them is for.
//   - liveness != 'dead' — a fresh OBSERVATION, joined the same way
//     ListLiveSessions does it: we reached the session's tmux server and its
//     pane was not there. `unknown` (server unreachable) deliberately stays,
//     because "could not disprove" is not "gone" — the rule E-1898 established
//     and this board has no reason to reinterpret.
//   - hidden — `endless session hide` is how a user says "stop showing me this
//     one", and a board whose whole job is attention triage is the last surface
//     that should ignore it.
//
// The task join is LEFT: a session that has claimed nothing still gets a row,
// because an agent running with no task is exactly the kind of thing a person
// wants to see on a board.
func projectSessionRows(db *sql.DB, projectID int64) ([]ProjectStatusRow, error) {
	rows, err := db.Query(`
		SELECT s.id,
		       s.state,
		       COALESCE(NULLIF(s.last_activity, ''), s.started_at, '') AS activity,
		       COALESCE(t.id, 0),
		       COALESCE(t.title, ''),
		       COALESCE(t.status, ''),
		       COALESCE(t.phase, ''),
		       COALESCE(ty.slug, ''),
		       COALESCE(t.updated_at, '')
		  FROM sessions s
		  JOIN session_liveness sl ON sl.session_id = s.id
		  LEFT JOIN live_tasks t ON t.id = s.task_id
		  LEFT JOIN task_types ty ON ty.id = t.type_id
		 WHERE s.project_id = ?
		   AND s.state IN (`+sessionstate.SQLList(sessionstate.Live)+`)
		   AND sl.liveness != 'dead'
		   AND s.hidden = 0`, projectID)
	if err != nil {
		return nil, fmt.Errorf("project status sessions: %w", err)
	}
	defer rows.Close()

	var out []ProjectStatusRow
	for rows.Next() {
		r := ProjectStatusRow{ProjectID: projectID}
		if err = rows.Scan(
			&r.SessionID, &r.SessionState, &r.SessionActivity,
			&r.TaskID, &r.Title, &r.Status, &r.Phase, &r.TypeSlug, &r.TaskUpdated,
		); err != nil {
			return nil, fmt.Errorf("project status sessions: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// projectTaskRows reads the project's attention-claiming tasks. Rows whose task
// a live session already holds are dropped by the caller, not here — this query
// has no business knowing about sessions, and the anti-join it would need
// re-derives the set the caller is already holding.
func projectTaskRows(db *sql.DB, projectID int64, all bool) ([]ProjectStatusRow, error) {
	query := `
		SELECT t.id, t.title, t.status, t.phase,
		       COALESCE(ty.slug, ''), COALESCE(t.updated_at, '')
		  FROM live_tasks t
		  LEFT JOIN task_types ty ON ty.id = t.type_id
		 WHERE t.project_id = ?
		   AND t.status IN (` + boardTaskStatuses(all) + `)`
	rows, err := db.Query(query, projectID)
	if err != nil {
		return nil, fmt.Errorf("project status tasks: %w", err)
	}
	defer rows.Close()

	var out []ProjectStatusRow
	for rows.Next() {
		r := ProjectStatusRow{ProjectID: projectID}
		if err = rows.Scan(
			&r.TaskID, &r.Title, &r.Status, &r.Phase, &r.TypeSlug, &r.TaskUpdated,
		); err != nil {
			return nil, fmt.Errorf("project status tasks: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SanitizeTmuxName folds a project name into something tmux will accept as part
// of a session name.
//
// tmux forbids '.' and ':' in a session name (both are target separators) and
// treats a leading '-' as a flag, so every character outside [A-Za-z0-9_-] is
// folded to '-'. PRODUCT: project names are user-chosen and arrive with spaces,
// dots and slashes in them; this must not be a rule the user has to know.
//
// A name that folds away to nothing yields "project", so a board stays
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
// --project-id path, which names the board's project without going through the
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
