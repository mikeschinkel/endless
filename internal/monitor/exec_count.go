package monitor

import (
	"os/exec"
	"sync/atomic"
)

// Child-process accounting for the live views (E-2228). A `session monitor`
// pane is one long-lived process, and what it costs the machine at idle is
// mostly what it forks: git and tmux children, each a fork of a large Go
// process. These counters are what the ENDLESS_MONITOR_PROFILE diagnostic
// (internal/liveview) reports per tick, so "how many execs does an idle tick
// cost" is a number rather than a guess.
//
// Every git child this package starts goes through runGit, and every tmux child
// through tmuxCommand; those two are the counting points.

var gitExecs, tmuxExecs atomic.Int64

// ExecCounts returns how many git and tmux children this process has started
// through this package since it began. Monotonic: a caller diffs two readings.
func ExecCounts() (git, tmux int64) {
	return gitExecs.Load(), tmuxExecs.Load()
}

// tmuxCommand builds `tmux <args...>` and counts it.
func tmuxCommand(args ...string) *exec.Cmd {
	tmuxExecs.Add(1)
	return exec.Command("tmux", args...)
}
