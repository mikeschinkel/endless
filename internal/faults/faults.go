// Package faults records classified, clearable diagnostics — the machine-local
// answer to "something went wrong, and the user should find out".
//
// The package is named `faults` ONLY because `errors` collides with the stdlib
// package name. Every user-facing surface it backs says "errors": the `errors`
// table, `endless errors list|show|clear`, and docs/errors.md.
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
// # Raiser attribution
//
// A fault also says WHO raised it: the task and the session that were active
// (E-2268). That is what lets an incident be routed back to the session that
// caused it, and what lets `errors list` say who caused what.
//
// Like the project, the raiser is resolved by a func injected at Bind — the
// RaiserResolver — so this package learns nothing about sessions, worktrees or
// harnesses. A producer that knows the one task its fault is about sets
// Fault.TaskID (and Fault.SessionID if it knows one); those win over anything
// resolved. A fault about several tasks names them in Fields and leaves both 0.
//
// The incident row carries the LATEST raiser; errors_sources carries every
// distinct one; each detail line carries its own.
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
	Summary     string         // SHORT descriptive text, shown in lists and the fault row
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

	// TaskID and SessionID name who raised this fault, for the producers that
	// know (E-2268): a job whose fault is about one task's branch or worktree
	// knows that task, and a hook handler knows the session its payload names.
	//
	// Leave them 0 and the bound RaiserResolver supplies them from the process
	// instead. A producer whose fault is about SEVERAL tasks leaves both 0 and
	// names the tasks in Fields: one of them would be a guess.
	TaskID    int64
	SessionID int64
}

// Raiser is who raised a fault: an Endless task id and an Endless session id
// (sessions.id). Either is 0 when not known.
type Raiser struct {
	TaskID    int64
	SessionID int64
}

// RaiserResolver answers "which task and session raised this fault?" for the
// same reason ProjectResolver exists: this package cannot.
//
// It is called with whatever the producer named explicitly — zero fields mean
// "you decide" — and returns the raiser to record. An explicit field must come
// back unchanged; the resolver fills only the zero ones.
//
// Returning the zero Raiser is a legitimate answer. An implementation must be
// read-only, for ProjectResolver's reason: it runs inside Record.
type RaiserResolver func(explicit Raiser) (resolved Raiser)

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
	raiserFunc  RaiserResolver
)

// Bind wires the database accessor, the detail-log directory, the project
// resolver and the raiser resolver. It is called once at process start (cmd/endless-go/main.go) and by
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
// raiser may be nil for the same reason, with the same degradation: a fault then
// names only the task and session its producer set.
func Bind(db func() (*sql.DB, error), logDir func() string, project ProjectResolver, raiser RaiserResolver) {
	bindMu.Lock()
	dbAccessor = db
	logDirFunc = logDir
	projectFunc = project
	raiserFunc = raiser
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

// resolveRaiser turns a producer's explicit raiser into the one Record persists.
// With no resolver bound it passes the explicit fields straight through.
//
// Explicit fields are re-applied over the resolver's answer, so a resolver that
// forgot the contract still cannot override what a producer said it knew.
func resolveRaiser(explicit Raiser) (raiser Raiser) {
	var fn RaiserResolver

	bindMu.RLock()
	fn = raiserFunc
	bindMu.RUnlock()

	raiser = explicit
	if fn == nil {
		goto end
	}
	raiser = fn(explicit)
	if explicit.TaskID != 0 {
		raiser.TaskID = explicit.TaskID
	}
	if explicit.SessionID != 0 {
		raiser.SessionID = explicit.SessionID
	}

end:
	return raiser
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
	var raiser Raiser

	f = f.normalized()
	if f.Summary == "" {
		goto end
	}

	// Resolved once and threaded to every half, so the row and its detail lines
	// can never disagree about which project the incident belongs to. It runs
	// BEFORE the database handle is taken (E-1887) so an unindexed line still
	// carries the project name whenever the resolver can supply one; a resolver
	// that needs the same broken database answers (0, "") and the line says
	// nothing rather than guessing.
	projectID, projectName = resolveProject(f.ProjectID)
	raiser = resolveRaiser(Raiser{TaskID: f.TaskID, SessionID: f.SessionID})

	db, err = database()
	if err != nil {
		goto unindexed
	}

	id, occurrence, err = upsertIncident(db, f, projectID, raiser)
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
		id, occurrence, err = upsertIncident(db, f, projectID, raiser)
	}
	if err != nil {
		goto unindexed
	}

	appendDetail(f, &id, occurrence, projectName, raiser, "")
	goto end

unindexed:
	// The index write did not happen, so this occurrence has no id and no
	// occurrence number — both are assigned BY that write. It still goes to
	// disk (E-1887).
	//
	// Until this branch existed, a fault raised BECAUSE the database was
	// unreachable was written nowhere at all: database() failing and
	// upsertIncident failing both returned before the log was touched. That is
	// the one failure mode where losing the report costs most, because the
	// surfaces that would otherwise carry it are reading the same broken
	// database.
	//
	// No synthetic id is minted — see the Detail doc. err is recorded as the
	// reason, which is the whole diagnosis when what failed is the fault store
	// itself, and is then dropped: Record has no channel to report on.
	appendDetail(f, nil, 0, projectName, raiser, err.Error())

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
// a listing readable.
func fingerprintOf(summary string) (fp string) {
	sum := sha256.Sum256([]byte(summary))
	fp = hex.EncodeToString(sum[:])[:16]
	return fp
}

// upsertIncident bumps the open incident for this project and fingerprint, or
// opens one, and records the raiser against it — both in one transaction, so an
// incident never exists without the errors_sources row that says who raised it.
//
// The incident upsert is a single statement, so two processes racing the same
// fingerprint cannot both insert: the partial unique index turns the loser into
// the DO UPDATE branch. RETURNING gives back the row id and the post-bump
// occurrence number, which the JSONL line needs to tie detail back to the
// incident.
//
// The ON CONFLICT target names COALESCE(project_id, 0) — not project_id —
// because that is the expression idx_errors_open_uniq indexes, and SQLite
// matches an upsert to an expression index by the expression itself. The
// COALESCE is not cosmetic: NULLs are DISTINCT in a unique index, so targeting
// the bare column would stop deduplicating every unattributed fault and let the
// tmux status bar, which re-execs per pane every two seconds, insert a fresh row
// per tick instead of bumping one. errors_sources folds its NULLs the same way.
//
// The DO UPDATE deliberately does not touch project_id: the conflicting row was
// matched BY project, so it already holds this one. It DOES rewrite task_id and
// session_id, unattributed or not: they name the latest raiser (E-2268).
func upsertIncident(db *sql.DB, f Fault, projectID int64, raiser Raiser) (id int64, occurrence int64, err error) {
	var tx *sql.Tx

	tx, err = db.Begin()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrRecording, ErrQuery, err)
		goto end
	}

	err = tx.QueryRow(
		`INSERT INTO errors
		     (project_id, code, severity, source, fingerprint, summary, occurrences,
		      first_seen_at, last_seen_at, task_id, session_id)
		 VALUES (?, ?, ?, ?, ?, ?, 1,
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'),
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'), ?, ?)
		 ON CONFLICT (COALESCE(project_id, 0), source, code, fingerprint)
		     WHERE cleared_at IS NULL
		 DO UPDATE SET occurrences  = occurrences + 1,
		               last_seen_at = strftime('%Y-%m-%dT%H:%M:%S', 'now'),
		               summary      = excluded.summary,
		               task_id      = excluded.task_id,
		               session_id   = excluded.session_id
		 RETURNING id, occurrences`,
		nullableID(projectID),
		f.Code.ID, string(f.Code.Severity), f.Source, f.Fingerprint, f.Summary,
		nullableID(raiser.TaskID), nullableID(raiser.SessionID),
	).Scan(&id, &occurrence)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrRecording, ErrQuery, err)
		goto rollback
	}

	_, err = tx.Exec(
		`INSERT INTO errors_sources
		     (error_id, session_id, task_id, occurrences, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, 1,
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'),
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		 ON CONFLICT (error_id, COALESCE(session_id, 0), COALESCE(task_id, 0))
		 DO UPDATE SET occurrences  = occurrences + 1,
		               last_seen_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')`,
		id, nullableID(raiser.SessionID), nullableID(raiser.TaskID),
	)
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrRecording, ErrQuery, err)
		goto rollback
	}

	err = tx.Commit()
	if err != nil {
		err = doterr.NewErr(ErrFaults, ErrRecording, ErrQuery, err)
	}
	goto end

rollback:
	// The rollback's own error is dropped: the statement error is the
	// diagnosis, and Record goes on to log the occurrence unindexed.
	_ = tx.Rollback()
	id, occurrence = 0, 0

end:
	return id, occurrence, err
}

// nullableID renders an unresolved id (0) as SQL NULL rather than as the
// integer 0, which would name a project, task or session that cannot exist. The
// sentinel 0 lives only inside the index expressions, never in a column.
func nullableID(id int64) (value any) {
	if id != 0 {
		value = id
	}
	return value
}
