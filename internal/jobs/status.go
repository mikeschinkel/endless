package jobs

import (
	"database/sql"
	"sort"
	"time"

	"github.com/mikeschinkel/go-doterr"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// Status is a registered job joined to its scheduling row — what `jobs list`
// renders. Registered is false for a row whose job is no longer in the registry
// (an inert leftover); Scheduled is false for a registered job that has never
// run and so has no row yet.
type Status struct {
	Name       string
	Registered bool
	Scheduled  bool
	Schedule   Schedule
	NextDueAt  string
	LastRunAt  string
	LastOkAt   string
	LastError  string
	LeaseOwner string
	RunCount   int64
	FailCount  int64
}

// Statuses returns every registered job and every scheduling row, merged and
// ordered by name.
func Statuses() (statuses []Status, err error) {
	var db *sql.DB
	var rows map[string]Status
	var job Job
	var status Status
	var ok bool
	var names []string
	var name string

	db, err = monitor.DB()
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, err)
		goto end
	}

	rows, err = scheduleRows(db)
	if err != nil {
		goto end
	}

	for _, job = range Registered() {
		status, ok = rows[job.Name()]
		if !ok {
			status = Status{Name: job.Name()}
		}
		status.Registered = true
		status.Scheduled = ok
		status.Schedule = job.Schedule()
		rows[job.Name()] = status
	}

	names = make([]string, 0, len(rows))
	for name = range rows {
		names = append(names, name)
	}
	sort.Strings(names)

	statuses = make([]Status, 0, len(names))
	for _, name = range names {
		statuses = append(statuses, rows[name])
	}

end:
	return statuses, err
}

// Retry clears a job's backoff and makes it due immediately: the "I have fixed
// the underlying problem, try again now" verb.
//
// Deliberately separate from `errors clear`, which only means "I have seen
// this". Fusing the two would let acknowledging a message silently re-arm a job
// that is still broken — and, worse, would make a user who merely tidied their
// error list unknowingly restart an expensive failing job.
func Retry(name string) (err error) {
	var db *sql.DB
	var result sql.Result
	var affected int64
	var ok bool

	_, ok = Lookup(name)
	if !ok {
		err = doterr.NewErr(ErrJobs, ErrUnknownJob, "name", name)
		goto end
	}

	db, err = monitor.DB()
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, err)
		goto end
	}

	err = ensureRow(db, name)
	if err != nil {
		goto end
	}

	result, err = db.Exec(
		`UPDATE jobs
		    SET next_due_at = `+sqlNow+`,
		        fail_count  = 0,
		        last_error  = NULL,
		        updated_at  = `+sqlNow+`
		  WHERE name = ?`,
		name,
	)
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, ErrQuery, "name", name, err)
		goto end
	}
	affected, err = result.RowsAffected()
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, "name", name, err)
		goto end
	}
	if affected == 0 {
		err = doterr.NewErr(ErrJobs, ErrUnknownJob, "name", name)
	}

end:
	return err
}

// scheduleRows reads every jobs row, keyed by name.
func scheduleRows(db *sql.DB) (rows map[string]Status, err error) {
	var result *sql.Rows
	var status Status
	var nextDue, lastRun, lastOk, lastErr, owner sql.NullString
	var closeErr error

	rows = make(map[string]Status)

	result, err = db.Query(
		`SELECT name, next_due_at, last_run_at, last_ok_at, last_error,
		        lease_owner, run_count, fail_count
		   FROM jobs`,
	)
	if err != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, ErrQuery, err)
		goto end
	}

	for result.Next() {
		status = Status{}
		err = result.Scan(
			&status.Name, &nextDue, &lastRun, &lastOk, &lastErr,
			&owner, &status.RunCount, &status.FailCount,
		)
		if err != nil {
			err = doterr.NewErr(ErrJobs, ErrDatabase, err)
			break
		}
		status.Scheduled = true
		status.NextDueAt = nextDue.String
		status.LastRunAt = lastRun.String
		status.LastOkAt = lastOk.String
		status.LastError = lastErr.String
		status.LeaseOwner = owner.String
		rows[status.Name] = status
	}
	if err == nil {
		err = result.Err()
		if err != nil {
			err = doterr.NewErr(ErrJobs, ErrDatabase, ErrQuery, err)
		}
	}

	closeErr = result.Close()
	if err == nil && closeErr != nil {
		err = doterr.NewErr(ErrJobs, ErrDatabase, closeErr)
	}
	if err != nil {
		rows = nil
	}

end:
	return rows, err
}

// DueIn returns how long until a status is next due, relative to now. It is
// negative when the job is already overdue, and zero when the row carries no
// parsable timestamp.
//
// Parsing is in UTC to match the SQL writer: schema.sql stores
// strftime(..., 'now'), which is UTC without a zone suffix.
func (s Status) DueIn() (d time.Duration) {
	var due time.Time
	var err error

	if s.NextDueAt == "" {
		goto end
	}
	due, err = time.ParseInLocation("2006-01-02T15:04:05", s.NextDueAt, time.UTC)
	if err != nil {
		goto end
	}
	d = time.Until(due)

end:
	return d
}
