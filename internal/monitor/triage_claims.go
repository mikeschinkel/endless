package monitor

import (
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// E-1859 (reopened) — the per-task triage claim.
//
// Triage runs two ways and only one of them holds a lock. `task add` spawns a
// detached `triage run --task N`; the E-698 job runner separately sweeps the
// queue. internal/jobs already arbitrates sweep-vs-sweep with a CAS lease on the
// JOB row, but the inline child never enters the runner, so nothing stops a
// sweep from selecting a task an inline child is already mid-call on. Both then
// pay for the same model call. The status re-read before the write keeps the
// LEDGER correct; it cannot refund the spend, because it happens after the call.
//
// So the claim is taken before the model call, by both paths, against the same
// table.
//
// The mechanism is the jobs lease's, deliberately reused rather than reinvented:
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
	// sqlNow / sqlNowOffset read the clock inside SQLite. Same literals as
	// internal/jobs uses; duplicated rather than exported because they are a
	// SQL detail of each package, not a shared contract.
	sqlNow       = `strftime('%Y-%m-%dT%H:%M:%S', 'now')`
	sqlNowOffset = `strftime('%Y-%m-%dT%H:%M:%S', 'now', ?)`
)

var (
	triageOwnerOnce  sync.Once
	triageOwnerValue string
)

// TriageClaimOwner returns a stable per-process identity for claims. Host, pid
// and a start-time nonce, so a recycled pid cannot be mistaken for the process
// that took an earlier claim.
func TriageClaimOwner() (owner string) {
	triageOwnerOnce.Do(func() {
		host, err := os.Hostname()
		if err != nil {
			host = "unknown"
		}
		triageOwnerValue = host + ":" +
			strconv.Itoa(os.Getpid()) + ":" +
			strconv.FormatInt(time.Now().UnixNano(), 36)
	})
	return triageOwnerValue
}

// ClaimTriage attempts to claim taskID for ttl. claimed is false when another
// process holds a live claim — an ordinary outcome, not an error, and the
// caller's cue to skip the task rather than pay for a duplicate model call.
//
// A lapsed claim is overwritten in place, which is what makes a dead claimant
// self-healing.
func ClaimTriage(taskID int64, owner string, ttl time.Duration) (claimed bool, err error) {
	db, err := DB()
	if err != nil {
		return false, err
	}
	return claimTriage(db, taskID, owner, ttl)
}

func claimTriage(db *sql.DB, taskID int64, owner string, ttl time.Duration) (bool, error) {
	// One statement, so the WHERE clause is the arbitration. The INSERT wins an
	// unclaimed task; the DO UPDATE wins one whose claim has lapsed; a live
	// claim held by anyone (including this owner, re-entering) matches neither
	// and reports zero rows.
	res, err := db.Exec(
		`INSERT INTO triage_claims (task_id, owner, claimed_at, expires_at)
		 VALUES (?, ?, `+sqlNow+`, `+sqlNowOffset+`)
		 ON CONFLICT(task_id) DO UPDATE SET
		     owner      = excluded.owner,
		     claimed_at = excluded.claimed_at,
		     expires_at = excluded.expires_at
		   WHERE triage_claims.expires_at <= `+sqlNow,
		taskID, owner, triageSecondsOffset(ttl),
	)
	if err != nil {
		return false, fmt.Errorf("claim triage for E-%d: %w", taskID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim triage for E-%d: %w", taskID, err)
	}
	return n == 1, nil
}

// ReleaseTriage drops this owner's claim on taskID. Releasing a claim that has
// already lapsed and been taken by someone else is a no-op, never a steal —
// that is what the owner predicate is for.
func ReleaseTriage(taskID int64, owner string) (err error) {
	db, err := DB()
	if err != nil {
		return err
	}
	return releaseTriage(db, taskID, owner)
}

func releaseTriage(db *sql.DB, taskID int64, owner string) error {
	_, err := db.Exec(
		"DELETE FROM triage_claims WHERE task_id = ? AND owner = ?",
		taskID, owner,
	)
	if err != nil {
		return fmt.Errorf("release triage claim for E-%d: %w", taskID, err)
	}
	return nil
}

// triageSecondsOffset renders a duration as a SQLite modifier like "+120
// seconds". Sub-second durations floor to 0, which yields an already-expired
// claim rather than a negative offset.
func triageSecondsOffset(d time.Duration) (offset string) {
	secs := int64(d / time.Second)
	if secs < 0 {
		secs = 0
	}
	return "+" + strconv.FormatInt(secs, 10) + " seconds"
}
