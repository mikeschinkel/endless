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
// The one state that matters is a binary built inside a task worktree whose
// database context is the main database: candidate, unreviewed job code pointed
// at the developer's actual database — exactly the pollution the per-worktree
// sandbox (E-1281) exists to prevent. Since ED-1601 monitor.DB() refuses that
// pairing outright, so a runner that tried would only fail; this reports it as
// the reason instead of as a scheduling fault.
//
// It asks what the EXECUTABLE is, not where cwd is. Until E-2020 the test was
// "cwd in a self-dev worktree and pinned to a real DB", a proxy for "candidate
// code" that also suppressed the INSTALLED binary's monitor in a worktree pane
// — installed code, which may run jobs against main like any other.
//
// This guard lives on the TRIGGER rather than on the DB context. E-698
// originally implemented it by making session-status skip its main pin inside a
// worktree, on the theory that one self_dev rule beats a per-command exception.
// That broke the dashboard outright: session/pane state is machine-scoped and
// is read by a single-database join against tasks, so the view has to read main.
// Suppressing the runner achieves the same protection and costs nothing visible,
// because a suppressed runner with an empty registry does exactly what an
// unsuppressed one does — nothing.
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
	if monitor.WorktreeBuildOnMainDB() {
		suppressed = true
		reason = "worktree-built binary aimed at the main database " +
			"(a worktree build never opens main, ED-1601; " +
			"pass --db sandbox to run jobs against this worktree's sandbox)"
		goto end
	}

end:
	return suppressed, reason
}
