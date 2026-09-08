// Package faults records classified, clearable diagnostics — the machine-local
// answer to "something went wrong, and the user should find out".
//
// The package is named `faults` ONLY because `errors` collides with the stdlib
// package name. Every user-facing surface it backs says "errors": the `errors`
// table, `endless errors show|clear`, and docs/errors.md.
//
// # Ownership
//
// This package is deliberately NOT owned by the job runner (E-698), which is
// merely its first client. The hook, `worktree land`, and the reap sweep report
// through the same channel as they are converted (E-1884). For that reason it
// imports nothing from the rest of Endless — a dependency on internal/monitor
// here would become an import cycle the moment monitor reports a fault. The DB
// accessor, the log directory and the project resolver are injected once at
// process start via Bind.
//
// # Storage split
//
// The `errors` table is the INDEX: code, source, fingerprint, short summary,
// occurrence counts, and clear state. It is bounded by distinct-fingerprint
// count, not by failure count.
//
// The full detail of every occurrence is appended to <logDir>/errors.jsonl —
// modeled on internal/monitor/usermachinelog.go. Nothing is lost to diagnosis,
// the table cannot explode, and the file is ConfigDir-routed so a self-dev
// sandbox gets its own. It is not the shareable ledger and is never replayed.
//
// Faults emit NO db-ledger events: they are machine-local observation, not
// shareable project history.
//
// # Incident model
//
// At most one OPEN row exists per (project, source, code, fingerprint), enforced
// by a partial unique index. Repeats bump `occurrences` in place. Clearing closes
// the row, which becomes immutable history; a recurrence after clearing opens a
// NEW row rather than resurrecting the old one, so a regression that returns is
// visibly distinct from one that never left.
//
// # Project attribution
//
// One Endless database holds every project on the machine, so a fault is not
// fully identified until it says which project it happened in (E-1960). A
// producer that already knows sets Fault.ProjectID; everything else is resolved
// from the process's working directory by the ProjectResolver injected at Bind,
// which is the same answer for every producer that runs where the work does.
//
// Resolution can legitimately come up empty — the background job runner failing
// to open the database belongs to the machine, not to whatever directory the
// tick ran in — and NULL is then recorded rather than a guess. Because such a
// fault would otherwise be reportable on no surface at all, every project-scoped
// read includes the unattributed incidents alongside the project's own; see
// ProjectScope.
//
// # Contract
//
// Record is best-effort and NEVER fails: it is called from paths (the session
// monitor's render tick, hooks) where surfacing a logging failure would be worse
// than losing the log line. It also never recurses — a failure to record a fault
// does not record a fault about failing to record.
package faults

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"sync"

	"github.com/mikeschinkel/go-doterr"
)

// Fault is one occurrence of something going wrong, as reported by a caller.
//
// Only Code, Source and Summary are required. Fingerprint defaults to a hash of
// Summary, which is the right grouping when the summary is already stable (e.g.
// "job \"evaluate\" failed"); pass it explicitly when the summary embeds
// varying detail that would otherwise split one incident into many.
type Fault struct {
	Code        Code           // catalog entry; supplies the ID and severity
	Source      string         // subsystem that raised it, e.g. "job:evaluate"
	Fingerprint string         // grouping key; derived from Summary when empty
	Summary     string         // SHORT descriptive text, shown in lists and the badge
	Detail      string         // LONG capture; goes to the JSONL log, never the DB
	Fields      map[string]any // structured context; goes to the JSONL log

	// ProjectID names the project this fault happened in, for the producers that
	// know it — a probe told which project's worktree to inspect attributes the
	// failure to THAT project, not to the directory the render tick ran in, which
	// is a different project whenever a machine-wide view is the caller.
	//
	// Leave it 0 and the bound ProjectResolver supplies the process's ambient
	// project instead. That is the right answer for every producer that runs
	// where the work does, which is most of them, so this field is the exception
	// and not the convention.
	ProjectID int64
}

// ProjectResolver answers "which project is this fault attributed to?" — a
// question this package cannot answer itself, because it imports nothing from
// the rest of Endless (see the ownership note above).
//
// It is called with the producer's explicit Fault.ProjectID, or 0 when the
// producer named none, and returns the id and NAME to record: the id qualifies
// the `errors` row, the name goes in the JSONL detail line, where a reader has
// no database to join against.
//
// Returning (0, "") is a legitimate answer, not a failure — see the package doc.
// An implementation must be read-only and must never mint a project row: it runs
// inside Record, on paths where a diagnostic side effect registering a project
// would be a considerably worse bug than the one being reported.
type ProjectResolver func(id int64) (projectID int64, name string)

var (
	bindMu      sync.RWMutex
	dbAccessor  func() (*sql.DB, error)
	logDirFunc  func() string
	projectFunc ProjectResolver
)

// Bind wires the database accessor, the detail-log directory and the project
// resolver. It is called once at process start (cmd/endless-go/main.go) and by
// test helpers.
//
// Until it is called, Record is a silent no-op and the read paths return
// ErrNotBound. That is deliberate: a process that never touches faults must not
// be forced to construct a DB handle, and Record's never-fail contract forbids
// exploding on an unbound package.
//
// project may be nil, which degrades to "attribute a fault to whatever project
// its producer named, and to none otherwise". Recording still works; it just
// stops being ambient. A test that does not care about attribution passes nil.
func Bind(db func() (*sql.DB, error), logDir func() string, project ProjectResolver) {
	bindMu.Lock()
	dbAccessor = db
	logDirFunc = logDir
	projectFunc = project
	bindMu.Unlock()
}

// Bound reports whether Bind has been called. Used by tests and by the verify
// suite to prove the wiring exists rather than inferring it from silence.
func Bound() (bound bool) {
	bindMu.RLock()
	bound = dbAccessor != nil
	bindMu.RUnlock()
	return bound
}

// database returns the bound DB handle, or ErrNotBound when Bind was never
// called.
func database() (db *sql.DB, err error) {
	var accessor func() (*sql.DB, error)

	bindMu.RLock()
	accessor = dbAccessor
	bindMu.RUnlock()

	if accessor == nil {
		err = doterr.NewErr(ErrFaults, ErrNotBound)
		goto end
	}
	db, err = accessor()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrDatabase, err)
		goto end
	}

end:
	return db, err
}

// logDir returns the bound detail-log directory, or "" when unbound.
func logDir() (dir string) {
	var fn func() string

	bindMu.RLock()
	fn = logDirFunc
	bindMu.RUnlock()

	if fn == nil {
		goto end
	}
	dir = fn()

end:
	return dir
}

// resolveProject turns a producer's explicit project id — or 0, meaning "you
// decide" — into the (id, name) pair Record persists.
//
// With no resolver bound it passes the explicit id straight through and leaves
// the name empty: an unbound process can still record what its producers knew,
// it just cannot discover anything they did not say.
func resolveProject(explicit int64) (id int64, name string) {
	var fn ProjectResolver

	bindMu.RLock()
	fn = projectFunc
	bindMu.RUnlock()

	if fn == nil {
		id = explicit
		goto end
	}
	id, name = fn(explicit)

end:
	return id, name
}

// Record persists one fault occurrence: it upserts the open incident in the
// `errors` table and appends the full detail to the JSONL log.
//
// It NEVER returns an error and never panics. Every failure — unbound package,
// missing table, locked database, unwritable log — is swallowed, because every
// caller is on a path where a diagnostic failure must not become a user-visible
// failure. It also does not recurse: a failure here records nothing.
func Record(f Fault) {
	var db *sql.DB
	var err error
	var id int64
	var occurrence int64
	var projectID int64
	var projectName string

	f = f.normalized()
	if f.Summary == "" {
		goto end
	}

	db, err = database()
	if err != nil {
		goto end
	}

	// Resolved once and threaded to both halves, so the row and its detail lines
	// can never disagree about which project the incident belongs to.
	projectID, projectName = resolveProject(f.ProjectID)

	id, occurrence, err = upsertIncident(db, f, projectID)
	if err != nil && projectID != 0 {
		// Retry unattributed. project_id is a foreign key, so an id naming a
		// project that no longer exists — a row deleted between the resolve and
		// the write, a producer holding a stale id — makes SQLite reject the
		// INSERT, and the report would vanish along with the attribution.
		//
		// Attribution is a REFINEMENT of a fault report; the report is the
		// payload. Losing the refinement costs a filter; losing the payload is
		// the failure mode this whole package exists to prevent, and it would be
		// silent, because Record cannot tell anyone it dropped something.
		projectID, projectName = 0, ""
		id, occurrence, err = upsertIncident(db, f, projectID)
	}
	if err != nil {
		goto end
	}

	appendDetail(f, id, occurrence, projectName)

end:
	return
}

// normalized fills the defaults Record allows callers to omit. Returns a copy;
// the caller's Fault is untouched.
func (f Fault) normalized() (out Fault) {
	out = f
	out.Summary = strings.TrimSpace(out.Summary)
	if out.Summary == "" {
		out.Summary = out.Code.Title
	}
	if out.Source == "" {
		out.Source = "unknown"
	}
	if out.Fingerprint == "" {
		out.Fingerprint = fingerprintOf(out.Summary)
	}
	return out
}

// fingerprintOf derives a stable grouping key from a summary. Truncated to 16
// hex characters: collision risk is irrelevant here (a collision merely groups
// two distinct incidents, which clearing separates again) and short keys keep
// `errors show` readable.
func fingerprintOf(summary string) (fp string) {
	sum := sha256.Sum256([]byte(summary))
	fp = hex.EncodeToString(sum[:])[:16]
	return fp
}

// upsertIncident bumps the open incident for this project and fingerprint, or
// opens one.
//
// A single statement, so two processes racing the same fingerprint cannot both
// insert: the partial unique index turns the loser into the DO UPDATE branch.
// RETURNING gives back the row id and the post-bump occurrence number, which the
// JSONL line needs to tie detail back to the incident.
//
// The ON CONFLICT target names COALESCE(project_id, 0) — not project_id —
// because that is the expression idx_errors_open_uniq indexes, and SQLite
// matches an upsert to an expression index by the expression itself. The
// COALESCE is not cosmetic: NULLs are DISTINCT in a unique index, so targeting
// the bare column would stop deduplicating every unattributed fault and let the
// tmux status bar, which re-execs per pane every two seconds, insert a fresh row
// per tick instead of bumping one.
//
// The DO UPDATE deliberately does not touch project_id: the conflicting row was
// matched BY project, so it already holds this one.
func upsertIncident(db *sql.DB, f Fault, projectID int64) (id int64, occurrence int64, err error) {
	err = db.QueryRow(
		`INSERT INTO errors
		     (project_id, code, severity, source, fingerprint, summary, occurrences,
		      first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, ?, ?, 1,
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'),
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		 ON CONFLICT (COALESCE(project_id, 0), source, code, fingerprint)
		     WHERE cleared_at IS NULL
		 DO UPDATE SET occurrences  = occurrences + 1,
		               last_seen_at = strftime('%Y-%m-%dT%H:%M:%S', 'now'),
		               summary      = excluded.summary
		 RETURNING id, occurrences`,
		nullableProjectID(projectID),
		f.Code.ID, string(f.Code.Severity), f.Source, f.Fingerprint, f.Summary,
	).Scan(&id, &occurrence)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrRecording, ErrQuery, err)
		goto end
	}

end:
	return id, occurrence, err
}

// nullableProjectID renders an unresolved project (0) as SQL NULL rather than as
// the integer 0, which would be a foreign key to a project that cannot exist.
// The sentinel 0 lives only inside the index expression, never in the column.
func nullableProjectID(id int64) (value any) {
	if id != 0 {
		value = id
	}
	return value
}
