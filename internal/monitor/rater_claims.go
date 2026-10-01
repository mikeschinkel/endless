package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// E-2203 — the per-task rater claim.
//
// The rater job runs under the E-698 runner, whose CAS lease on the JOB row
// keeps two sweeps apart. A person's `endless rater run` never enters the
// runner, so nothing stops it selecting a task the sweep is already mid-call
// on, and both then pay for the same model call. The re-read before the write
// keeps the LEDGER correct; it cannot refund the spend, because it happens
// after the call. So the claim is taken before the model call, by every path,
// against one table.
//
// This is the retired triage claim (E-1859, removed by E-1993) restored under
// the rater's name. The mechanism is the jobs lease's, reused rather than
// reinvented:
//
//   - CAS: claiming is a single conditional statement whose WHERE clause IS the
//     mutual exclusion. RowsAffected == 1 means this process owns the task.
//   - DB clock: every expiry comparison uses SQLite's clock, never Go's, so N
//     racing processes cannot disagree about whether a claim is live.
//   - Time-boxed, not an OS lock: a claimant that is killed mid-call leaves a
//     row that simply lapses, so there is no cleanup path to get wrong.
//
// The corollary is the same one internal/jobs carries: the TTL must exceed the
// worst-case call, or a slow-but-healthy claimant gets its task re-claimed
// underneath it.

const (
	// claimSQLNow / claimSQLNowOffset read the clock inside SQLite. Same
	// literals as internal/jobs uses; duplicated rather than exported because
	// they are a SQL detail of each package, not a shared contract.
	claimSQLNow       = `strftime('%Y-%m-%dT%H:%M:%S', 'now')`
	claimSQLNowOffset = `strftime('%Y-%m-%dT%H:%M:%S', 'now', ?)`
)

var (
	raterOwnerOnce  sync.Once
	raterOwnerValue string
)

// RaterClaimOwner returns a stable per-process identity for claims. Host, pid
// and a start-time nonce, so a recycled pid cannot be mistaken for the process
// that took an earlier claim.
func RaterClaimOwner() (owner string) {
	raterOwnerOnce.Do(func() {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown"
		}
		raterOwnerValue = host + ":" +
			strconv.Itoa(os.Getpid()) + ":" +
			strconv.FormatInt(time.Now().UnixNano(), 36)
	})
	return raterOwnerValue
}

// ClaimRater attempts to claim taskID for ttl. claimed is false when another
// process holds a live claim — an ordinary outcome, not an error, and the
// caller's cue to skip the task rather than pay for a duplicate model call.
//
// A lapsed claim is overwritten in place, which is what makes a dead claimant
// self-healing.
func ClaimRater(taskID int64, owner string, ttl time.Duration) (claimed bool, err error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	return claimRater(db, taskID, owner, ttl)
}

func claimRater(db *sql.DB, taskID int64, owner string, ttl time.Duration) (bool, error) {
	// One statement, so the WHERE clause is the arbitration. The INSERT wins an
	// unclaimed task; the DO UPDATE wins one whose claim has lapsed; a live
	// claim held by anyone (including this owner, re-entering) matches neither
	// and reports zero rows.
	res, err := db.Exec(
		`INSERT INTO rater_claims (task_id, owner, claimed_at, expires_at)
		 VALUES (?, ?, `+claimSQLNow+`, `+claimSQLNowOffset+`)
		 ON CONFLICT(task_id) DO UPDATE SET
		     owner      = excluded.owner,
		     claimed_at = excluded.claimed_at,
		     expires_at = excluded.expires_at
		   WHERE rater_claims.expires_at <= `+claimSQLNow,
		taskID, owner, claimSecondsOffset(ttl),
	)
	if err != nil {
		return false, fmt.Errorf("claim rating of E-%d: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim rating of E-%d: %w", taskID, err)
	}
	return n == 1, nil
}

// ReleaseRater drops this owner's claim on taskID. Releasing a claim that has
// already lapsed and been taken by someone else is a no-op, never a steal —
// that is what the owner predicate is for.
func ReleaseRater(taskID int64, owner string) (err error) {
	db, err := DB()
	if err != nil {
		return err
	}
	return releaseRater(db, taskID, owner)
}

func releaseRater(db *sql.DB, taskID int64, owner string) error {
	_, err := db.Exec(
		"DELETE FROM rater_claims WHERE task_id = ? AND owner = ?",
		taskID, owner,
	)
	if err != nil {
		return fmt.Errorf("release rater claim for E-%d: %w", taskID, err)
	}
	return nil
}

// claimSecondsOffset renders a duration as a SQLite modifier like "+120
// seconds". Sub-second durations floor to 0, which yields an already-expired
// claim rather than a negative offset.
func claimSecondsOffset(d time.Duration) (offset string) {
	secs := int64(d / time.Second)
	if secs < 0 {
		secs = 0
	}
	return "+" + strconv.FormatInt(secs, 10) + " seconds"
}
