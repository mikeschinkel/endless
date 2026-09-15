package monitor

import (
	"database/sql"
	"encoding/json"
	"time"
)

// RecordActivity logs an activity event for a project.
func RecordActivity(projectID int64, source, workingDir string, sessionCtx map[string]string) error {
	db, err := DB()
	if err != nil {
		return err
	}

	var ctxJSON *string
	if len(sessionCtx) > 0 {
		b, err := json.Marshal(sessionCtx)
		if err == nil {
			s := string(b)
			ctxJSON = &s
		}
	}

	now := time.Now().UTC().Format("2006-01-02T15:04:05")
	_, err = db.Exec(
		"INSERT INTO activity (project_id, source, working_dir, session_context, created_at) "+
			"VALUES (?, ?, ?, ?, ?)",
		projectID, source, workingDir, ctxJSON, now,
	)
	return err
}

// IdleGap is the span above which two consecutive activity rows are read as
// "the user walked away" rather than "the user paused to think".
//
// Activity is recorded per project+source on a 2s (claude) / 5s (prompt)
// throttle, so a working session leaves rows continuously; anything past five
// minutes of silence is someone who is not at the keyboard.
const IdleGap = 5 * time.Minute

// ActiveSecondsSince returns how much ACTIVE time has accumulated since `since`
// — wall-clock with the idle stretches removed.
//
// Wall-clock is the wrong ruler for any elapsed time reported to a user: an hour
// that passes overnight has shown them nothing, so "an hour ago" names a stretch
// they were never present for. This sums only the gaps between consecutive
// activity rows that are shorter than IdleGap, which pauses the clock whenever
// the machine is unattended.
//
// Deliberately NOT scoped to a project: the question is whether the user was at
// the computer, not which repo they were in.
func ActiveSecondsSince(since time.Time) (active time.Duration, err error) {
	var db *sql.DB
	var rows *sql.Rows
	var stamp string
	var at time.Time
	var prev time.Time
	var gap time.Duration
	var closeErr error

	db, err = DB()
	if err != nil {
		goto end
	}

	rows, err = db.Query(
		"SELECT created_at FROM activity WHERE created_at > ? ORDER BY created_at",
		since.UTC().Format("2006-01-02T15:04:05"),
	)
	if err != nil {
		goto end
	}

	for rows.Next() {
		err = rows.Scan(&stamp)
		if err != nil {
			break
		}
		at, err = time.Parse("2006-01-02T15:04:05", stamp)
		if err != nil {
			// A malformed row must not abort the tally; skip it and keep the
			// previous anchor so the surrounding gap stays measurable.
			err = nil
			continue
		}
		if !prev.IsZero() {
			gap = at.Sub(prev)
			if gap > 0 && gap <= IdleGap {
				active += gap
			}
		}
		prev = at
	}
	if err == nil {
		err = rows.Err()
	}

	closeErr = rows.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		active = 0
		goto end
	}

	// Count the open segment too: the user is active RIGHT NOW if the newest
	// row is recent, and without this the trailing stretch since the last
	// throttled write never accrues.
	if !prev.IsZero() {
		gap = time.Now().UTC().Sub(prev)
		if gap > 0 && gap <= IdleGap {
			active += gap
		}
	}

end:
	return active, err
}

// ShouldThrottle returns true if the last activity for this project+source
// was less than intervalSec seconds ago.
func ShouldThrottle(projectID int64, source string, intervalSec int) (bool, error) {
	db, err := DB()
	if err != nil {
		return false, err
	}

	var lastRun string
	err = db.QueryRow(
		"SELECT created_at FROM activity "+
			"WHERE project_id = ? AND source = ? "+
			"ORDER BY created_at DESC LIMIT 1",
		projectID, source,
	).Scan(&lastRun)
	if err != nil {
		// No previous run
		return false, nil
	}

	t, err := time.Parse("2006-01-02T15:04:05", lastRun)
	if err != nil {
		return false, nil
	}

	return time.Since(t).Seconds() < float64(intervalSec), nil
}
