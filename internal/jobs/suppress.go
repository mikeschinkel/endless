package jobs

import (
	"os"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// noJobsEnv force-disables the runner for one process. The escape hatch for a
// developer who wants a monitor running with no background work at all.
const noJobsEnv = "ENDLESS_NO_JOBS"

// Suppressed reports whether this process must not execute jobs, with a reason
// for `jobs list` to display.
//
// The one state that matters is a self_dev WORKTREE pinned to a real database.
// That combination means candidate, unreviewed job code is pointed at the
// developer's actual database — exactly the pollution the per-worktree
// sandbox (E-1281) exists to prevent — and, because E-1818 opens a pinned real
// DB schema-passive, at a database that will not even have the runner's tables.
//
// The cwd test is the right one for the callers that exist. Suppressed() is
// reached only from RunDue — the liveview/status-line tick and `endless jobs
// run` — and from `endless jobs list`; a process triggering jobs from inside a
// worktree is exactly the case this is about. A Claude hook never reaches here
// at all, so E-2166 pointing hooks at the installed binary changes nothing for
// this guard. An earlier revision of this comment claimed E-2166 had made the
// test broader than its rationale; that was wrong, and named a caller that does
// not exist.
//
// This guard lives on the TRIGGER rather than on the DB context. E-698
// originally implemented it by making session-status skip its main pin inside a
// worktree, on the theory that one self_dev rule beats a per-command exception.
// That broke the dashboard outright: session/pane state is machine-scoped and
// is read by a single-database join against tasks, so the view has to read main.
// Suppressing the runner achieves the same protection and costs nothing visible,
// because a suppressed runner with an empty registry does exactly what an
// unsuppressed one does — nothing.
//
// Note the asymmetry this deliberately preserves: from the MAIN checkout the
// monitor is also pinned, but InSelfDevWorktree is false there, so jobs run
// normally. Pinning alone is not the hazard; pinning CANDIDATE code is.
func Suppressed() (suppressed bool) {
	suppressed, _ = suppressedWithReason()
	return suppressed
}

// SuppressionReason returns a human-readable reason when the runner is
// suppressed, or "" when it is free to run.
func SuppressionReason() (reason string) {
	_, reason = suppressedWithReason()
	return reason
}

func suppressedWithReason() (suppressed bool, reason string) {
	if os.Getenv(noJobsEnv) != "" {
		suppressed = true
		reason = noJobsEnv + " is set"
		goto end
	}
	if monitor.InSelfDevWorktree() && monitor.PinnedToRealDB() {
		suppressed = true
		reason = "self-dev worktree pinned to a real database " +
			"(candidate code must not write the main database; " +
			"pass --db sandbox to run jobs against this worktree's sandbox)"
		goto end
	}

end:
	return suppressed, reason
}
