package faults

import (
	"database/sql"
	"strings"

	"github.com/mikeschinkel/go-doterr"
)

// Incident is one row of the `errors` table: a distinct fault, with the
// occurrence count that says whether it happened once or four hundred times.
//
// ClearedAt / ClearedBy are empty on an open incident. A cleared incident is
// immutable history — a recurrence opens a NEW incident rather than reopening
// this one, so a fault that came back is visibly distinct from one that never
// left.
type Incident struct {
	ID          int64
	ProjectID   int64  // 0 when no project could be attributed
	Project     string // project name; "" when ProjectID is 0
	Code        string
	Severity    Severity
	Source      string
	Fingerprint string
	Summary     string
	Occurrences int64
	FirstSeenAt string
	LastSeenAt  string
	ClearedAt   string
	ClearedBy   string
}

// ProjectScope selects which projects' incidents a read or a clear covers
// (E-1960). One Endless database holds every project on the machine, so every
// surface over this store has to say which of them it means.
//
// AllProjects — the zero value — covers the whole machine. Any other value
// covers that project PLUS the incidents no project could be attributed to.
//
// That second half is the part worth stating out loud. An unattributed fault is
// not a defective record to be filtered away; it is the background job runner
// failing to open the database, or the tmux status bar unable to resolve a pane
// — machine-level failures that belong to no project by construction. Dropping
// them from every project-scoped view would leave them reportable on no view at
// all, since a project-scoped view is what a user standing in a project sees.
// So they ride along with whichever project is being asked about, and the reader
// tells them apart by the empty project name.
type ProjectScope int64

// AllProjects is the machine-wide scope: every project's incidents, plus the
// unattributed ones.
const AllProjects ProjectScope = 0

// clause renders the scope as a SQL condition over the aliased errors table,
// with the bind arguments it needs. AllProjects renders nothing at all rather
// than a tautology, so the machine-wide query is byte-identical to the one this
// package issued before scoping existed.
func (s ProjectScope) clause(alias string) (cond string, args []any) {
	if s == AllProjects {
		goto end
	}
	cond = "(" + alias + ".project_id = ? OR " + alias + ".project_id IS NULL)"
	args = []any{int64(s)}

end:
	return cond, args
}

// Title returns the catalog title for this incident's code, falling back to the
// code id itself when the catalog does not know it — which happens when an older
// binary reads a row a newer one wrote.
func (i Incident) Title() (title string) {
	code, ok := LookupCode(i.Code)
	if !ok {
		title = i.Code
		goto end
	}
	title = code.Title

end:
	return title
}

// Overview is the aggregate the session-status badge renders: how many open
// incidents exist, at what severities, and the most recent one's text.
type Overview struct {
	Counts map[Severity]int // open incident count per severity
	Total  int              // open incidents across all severities
	Max    Severity         // highest severity present; "" when Total is 0
	Latest *Incident        // most recently seen open incident; nil when none
}

// Open returns the aggregate over currently-open (uncleared) incidents within
// scope.
//
// Callers on a render path must treat any error as "show no badge" rather than
// as a failure: a missing table (a binary pinned schema-passive onto a DB it
// does not own) or a locked DB must never take down the view.
//
// A caller that applies its own display policy — the session-status badge
// suppresses stale warnings (E-1950) — should call List and Summarize instead,
// so the aggregate is computed over the incidents it actually intends to show.
func Open(scope ProjectScope) (overview Overview, err error) {
	var incidents []Incident

	incidents, err = List(scope, false, 0)
	if err != nil {
		overview.Counts = make(map[Severity]int, 2)
		goto end
	}
	overview = Summarize(incidents)

end:
	return overview, err
}

// Summarize aggregates a set of incidents into the shape the badge renders.
//
// Split out from Open so a caller that filters the set first — by staleness, by
// severity — gets counts consistent with what it displays rather than with what
// the table holds. Latest is the most recently seen of the incidents PASSED IN,
// which requires them to be ordered last_seen_at DESC; both List and Open
// produce that order.
func Summarize(incidents []Incident) (overview Overview) {
	var incident Incident

	overview.Counts = make(map[Severity]int, 2)

	for _, incident = range incidents {
		overview.Counts[incident.Severity]++
		overview.Total++
		if incident.Severity.Rank() > overview.Max.Rank() {
			overview.Max = incident.Severity
		}
	}
	if len(incidents) > 0 {
		// Copied into a local so Latest does not alias the caller's slice.
		incident = incidents[0]
		overview.Latest = &incident
	}

	return overview
}

// List returns incidents within scope, most recently seen first. When
// includeCleared is false only open incidents are returned. A limit of 0 means
// no limit.
func List(scope ProjectScope, includeCleared bool, limit int) (incidents []Incident, err error) {
	var db *sql.DB
	var conds []string
	var cond string
	var args []any

	db, err = database()
	if err != nil {
		goto end
	}

	if !includeCleared {
		conds = append(conds, `e.cleared_at IS NULL`)
	}
	cond, args = scope.clause("e")
	if cond != "" {
		conds = append(conds, cond)
	}

	incidents, err = query(db, whereOf(conds), limit, args...)

end:
	return incidents, err
}

// whereOf assembles a WHERE clause from conditions, or the empty string when
// there are none — an unfiltered query must carry no WHERE at all rather than a
// `WHERE 1=1` that reads like a forgotten predicate.
func whereOf(conds []string) (where string) {
	if len(conds) == 0 {
		goto end
	}
	where = `WHERE ` + strings.Join(conds, ` AND `)

end:
	return where
}

// Get returns one incident by id. ok is false when no such row exists.
func Get(id int64) (incident Incident, ok bool, err error) {
	var db *sql.DB
	var incidents []Incident

	db, err = database()
	if err != nil {
		goto end
	}

	incidents, err = query(db, `WHERE e.id = ?`, 0, id)
	if err != nil {
		goto end
	}
	if len(incidents) == 0 {
		goto end
	}
	incident = incidents[0]
	ok = true

end:
	return incident, ok, err
}

// Clear marks incidents cleared. Clearing NEVER deletes: the row stays as
// history, and a later recurrence of the same fingerprint opens a new incident
// beside it.
//
// With ids empty, every open incident WITHIN SCOPE is cleared — that is the
// whole job of the scope here, because "clear everything" is precisely the call
// that needs a boundary: it must dismiss the set the user was just shown, and
// on a project-scoped surface that is one project's incidents plus the
// unattributed ones, not the whole machine's.
//
// With ids given, the scope is IGNORED and the named rows are cleared wherever
// they live. An id is already an exact selector and the user typed it; refusing
// it because the row belongs to another project would mean `errors clear 7`
// silently doing nothing right after a listing that showed row 7.
//
// `by` records who cleared it and may be empty.
//
// Clearing is explicitly NOT a retry: it means "I have seen this". Resetting a
// failing job's backoff is `jobs retry`, a separate verb, so that acknowledging
// a message cannot silently re-arm a job that is still broken.
func Clear(scope ProjectScope, ids []int64, by string) (cleared int, err error) {
	var db *sql.DB
	var result sql.Result
	var affected int64
	var args []any
	var placeholders []string
	var scopeArgs []any
	var scopeCond string
	var id int64
	var stmt string

	db, err = database()
	if err != nil {
		goto end
	}

	stmt = `UPDATE errors
	           SET cleared_at = strftime('%Y-%m-%dT%H:%M:%S', 'now'),
	               cleared_by = ?
	         WHERE cleared_at IS NULL`
	args = append(args, by)

	if len(ids) > 0 {
		placeholders = make([]string, 0, len(ids))
		for _, id = range ids {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		stmt += ` AND id IN (` + strings.Join(placeholders, ",") + `)`
	} else {
		// Only the no-id form is bounded by the scope — see the doc above. The
		// condition renders against the table name because an UPDATE has no
		// alias to hang one on.
		scopeCond, scopeArgs = scope.clause("errors")
		if scopeCond != "" {
			stmt += ` AND ` + scopeCond
			args = append(args, scopeArgs...)
		}
	}

	result, err = db.Exec(stmt, args...)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrClearing, ErrQuery, err)
		goto end
	}
	affected, err = result.RowsAffected()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrClearing, err)
		goto end
	}
	cleared = int(affected)

end:
	return cleared, err
}

// query runs the shared SELECT with a caller-supplied WHERE clause. Ordering is
// last_seen_at DESC so the newest incident is always first — Open relies on that
// to pick Latest without a second query.
//
// The errors table is aliased `e` and every caller's WHERE must qualify against
// it, because the project name is joined in rather than stored: `errors` holds
// the id, `projects` holds the name, and a reader wants the name. A LEFT join,
// so an unattributed incident (project_id NULL) still comes back — and so does
// one whose project row was deleted out from under a stale id.
func query(db *sql.DB, where string, limit int, args ...any) (incidents []Incident, err error) {
	var rows *sql.Rows
	var incident Incident
	var severity string
	var projectID sql.NullInt64
	var projectName sql.NullString
	var clearedAt sql.NullString
	var clearedBy sql.NullString
	var stmt string
	var closeErr error

	stmt = `SELECT e.id, e.project_id, p.name, e.code, e.severity, e.source,
	               e.fingerprint, e.summary, e.occurrences, e.first_seen_at,
	               e.last_seen_at, e.cleared_at, e.cleared_by
	          FROM errors e
	          LEFT JOIN projects p ON p.id = e.project_id ` + where + `
	         ORDER BY e.last_seen_at DESC, e.id DESC`
	if limit > 0 {
		stmt += ` LIMIT ?`
		args = append(args, limit)
	}

	rows, err = db.Query(stmt, args...)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, err)
		goto end
	}

	for rows.Next() {
		incident = Incident{}
		err = rows.Scan(
			&incident.ID, &projectID, &projectName, &incident.Code, &severity,
			&incident.Source, &incident.Fingerprint, &incident.Summary,
			&incident.Occurrences, &incident.FirstSeenAt, &incident.LastSeenAt,
			&clearedAt, &clearedBy,
		)
		if err != nil {
			err = doterr.NewErr(ErrFaults, ErrScanning, err)
			break
		}
		incident.Severity = Severity(severity)
		incident.ProjectID = projectID.Int64
		incident.Project = projectName.String
		incident.ClearedAt = clearedAt.String
		incident.ClearedBy = clearedBy.String
		incidents = append(incidents, incident)
	}
	if err == nil {
		err = rows.Err()
		if err != nil {
			err = doterr.NewErr(ErrFaults, ErrQuery, err)
		}
	}

	closeErr = rows.Close()
	if err == nil && closeErr != nil {
		err = doterr.NewErr(ErrFaults, ErrQuery, closeErr)
	}
	if err != nil {
		incidents = nil
	}

end:
	return incidents, err
}
