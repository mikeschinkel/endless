package faults

import (
	"database/sql"
	"strconv"
	"time"
)

// Transient runs (E-2589).
//
// Some failures clear on their own without anyone confirming why: a refused
// tmux connection while the server is briefly busy, a remote that cannot be
// reached for a few minutes, an API error that a retry answers. Raising a fault
// on the first one fills the fault row with warnings the user learns to
// ignore, so a source that knows a failure is usually brief records it here
// instead.
//
// A RUN is the consecutive occurrences of one such failure from one source,
// keyed by (Source, Fingerprint). Each occurrence is logged — a detail line of
// kind "transient" — and counted on the run. Only when the count reaches the
// run's threshold does the failure become a fault, through Record, exactly as
// if the source had called Record itself. A success closes the run, without a
// fault if it never reached the threshold.
//
// The job runner was the first user (E-2260, with its own counter in the jobs
// table); this is that handling made available to sources that have no
// scheduling row to count on — `monitor-wake`, the stopfailure hook — and the
// runner now counts here too, so every source's runs are in one history.
//
// Closed runs stay in transient_runs: the occurrence count, and either how long
// the run took to clear or that it became a fault. The phrase lists that mark a
// failure transient are written by hand today; that history is the evidence a
// later change can classify them from.

// TransientRun names one source's run of transient failures and how many
// occurrences it may have before it becomes a fault.
type TransientRun struct {
	Source      string // the subsystem, as in Fault.Source
	Fingerprint string // what the run is about, e.g. one tmux server or one session
	Threshold   int    // occurrences at which the failure becomes a fault; < 1 means 1
}

// TransientRunRecord is one row of transient_runs.
type TransientRunRecord struct {
	ID          int64
	Source      string
	Fingerprint string
	Code        string
	Threshold   int
	Occurrences int
	FirstSeenAt string
	LastSeenAt  string
	FaultedAt   string // "" when the run never reached its threshold
	ClosedAt    string // "" while the run is open
}

// TimeToClear is how long a closed run lasted from its first occurrence to the
// success that closed it. ok is false for an open run or an unparseable stamp.
func (r TransientRunRecord) TimeToClear() (d time.Duration, ok bool) {
	var first, closed time.Time
	var err error

	if r.ClosedAt == "" {
		goto end
	}
	first, err = time.Parse(sqlStamp, r.FirstSeenAt)
	if err != nil {
		goto end
	}
	closed, err = time.Parse(sqlStamp, r.ClosedAt)
	if err != nil {
		goto end
	}
	d, ok = closed.Sub(first), true

end:
	return d, ok
}

// sqlStamp is the layout every timestamp in the schema is stored in.
const sqlStamp = "2006-01-02T15:04:05"

// RecordTransient records one occurrence of f as part of run, and reports
// whether it became a fault.
//
// Below the threshold the occurrence is logged and counted, and nothing else:
// no `errors` row, no fault row, nothing for a person to clear. At the
// threshold and on every occurrence after it, f is recorded with Record and
// the run is marked faulted.
//
// Like Record it never fails and never panics. When the run cannot be counted
// — no database, a write that failed — the occurrence is recorded as a fault:
// a failure this package cannot count must not become one nobody hears about.
func RecordTransient(run TransientRun, f Fault) (raised bool) {
	var db *sql.DB
	var occurrences int
	var err error

	f = f.normalized()
	if run.Source == "" {
		run.Source = f.Source
	}
	if run.Fingerprint == "" {
		run.Fingerprint = f.Fingerprint
	}
	if run.Threshold < 1 {
		run.Threshold = 1
	}

	db, err = database()
	if err != nil {
		goto raise
	}
	occurrences, err = countTransient(db, run, f.Code)
	if err != nil {
		goto raise
	}
	if occurrences < run.Threshold {
		logTransient(run, f, occurrences)
		goto end
	}
	// Best-effort: the fault below is the report, and a run left unmarked
	// costs only the history's "became a fault" for this one run.
	_, _ = db.Exec(
		`UPDATE transient_runs
		    SET faulted_at = COALESCE(faulted_at, strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		  WHERE source = ? AND fingerprint = ? AND closed_at IS NULL`,
		run.Source, run.Fingerprint,
	)

raise:
	Record(f)
	raised = true

end:
	return raised
}

// countTransient opens the run or adds one occurrence to it, and returns the
// run's occurrence count including this one.
//
// One statement, for upsertIncident's reason: two processes failing at once
// cannot both open a run, because idx_transient_runs_open turns the loser into
// the DO UPDATE branch.
func countTransient(db *sql.DB, run TransientRun, code Code) (occurrences int, err error) {
	err = db.QueryRow(
		`INSERT INTO transient_runs
		     (source, fingerprint, code, threshold, occurrences, first_seen_at, last_seen_at)
		 VALUES (?, ?, ?, ?, 1,
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'),
		      strftime('%Y-%m-%dT%H:%M:%S', 'now'))
		 ON CONFLICT (source, fingerprint) WHERE closed_at IS NULL
		 DO UPDATE SET occurrences  = occurrences + 1,
		               last_seen_at = strftime('%Y-%m-%dT%H:%M:%S', 'now'),
		               code         = excluded.code,
		               threshold    = excluded.threshold
		 RETURNING occurrences`,
		run.Source, run.Fingerprint, code.ID, run.Threshold,
	).Scan(&occurrences)
	if err != nil {
		err = NewErr(ErrFaults, ErrRecording, ErrQuery, err)
	}
	return occurrences, err
}

// logTransient writes the detail line for an occurrence below the threshold.
// Kind "transient", with no fault id and not log-only: it reached the database
// it was meant to reach, as a count on its run, so the log-only notice must not
// count it.
func logTransient(run TransientRun, f Fault, occurrences int) {
	var fields map[string]any

	fields = make(map[string]any, len(f.Fields)+2)
	for k, v := range f.Fields {
		fields[k] = v
	}
	fields["run_occurrence"] = occurrences
	fields["run_threshold"] = run.Threshold

	writeDetailLine(Detail{
		Kind:        "transient",
		TS:          time.Now().UTC().Format(sqlStamp),
		Code:        f.Code.ID,
		Severity:    string(f.Code.Severity),
		Source:      run.Source,
		Fingerprint: run.Fingerprint,
		Summary: f.Summary + " (transient " + strconv.Itoa(occurrences) + " of " +
			strconv.Itoa(run.Threshold) + " before it is reported)",
		Detail: f.Detail,
		Fields: fields,
	})
}

// ResolveTransient closes the open run for (source, fingerprint), if there is
// one, and reports whether it closed one. A source calls it on success.
//
// It reads before it writes: a success is the common case and almost never has
// a run to close, and a read does not take SQLite's write lock. Best-effort and
// silent like Record — a run left open is closed by the next success.
func ResolveTransient(source, fingerprint string) (closed bool) {
	var db *sql.DB
	var open bool
	var result sql.Result
	var affected int64
	var err error

	db, err = database()
	if err != nil {
		goto end
	}
	err = db.QueryRow(
		`SELECT EXISTS (SELECT 1 FROM transient_runs
		                 WHERE source = ? AND fingerprint = ? AND closed_at IS NULL)`,
		source, fingerprint,
	).Scan(&open)
	if err != nil || !open {
		goto end
	}
	result, err = db.Exec(
		`UPDATE transient_runs
		    SET closed_at = strftime('%Y-%m-%dT%H:%M:%S', 'now')
		  WHERE source = ? AND fingerprint = ? AND closed_at IS NULL`,
		source, fingerprint,
	)
	if err != nil {
		goto end
	}
	affected, err = result.RowsAffected()
	closed = err == nil && affected > 0

end:
	return closed
}

// TransientRuns returns runs newest first, open ones included; limit < 1 means
// all of them.
func TransientRuns(limit int) (runs []TransientRunRecord, err error) {
	var db *sql.DB
	var rows *sql.Rows
	var faulted, closed sql.NullString

	db, err = database()
	if err != nil {
		goto end
	}
	if limit < 1 {
		limit = -1
	}
	rows, err = db.Query(
		`SELECT id, source, fingerprint, code, threshold, occurrences,
		        first_seen_at, last_seen_at, faulted_at, closed_at
		   FROM transient_runs
		  ORDER BY id DESC
		  LIMIT ?`, limit,
	)
	if err != nil {
		err = NewErr(ErrFaults, ErrQuery, err)
		goto end
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r TransientRunRecord
		err = rows.Scan(&r.ID, &r.Source, &r.Fingerprint, &r.Code, &r.Threshold,
			&r.Occurrences, &r.FirstSeenAt, &r.LastSeenAt, &faulted, &closed)
		if err != nil {
			err = NewErr(ErrFaults, ErrQuery, err)
			goto end
		}
		r.FaultedAt, r.ClosedAt = faulted.String, closed.String
		runs = append(runs, r)
	}
	err = rows.Err()
	if err != nil {
		err = NewErr(ErrFaults, ErrQuery, err)
	}

end:
	return runs, err
}
