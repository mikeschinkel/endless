package liveview

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/pprof"
	"syscall"
	"time"

	"github.com/mikeschinkel/endless/internal/jobs"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// The idle-cost diagnostic (E-2228). A live view runs for days in every tmux
// window, so what one idle tick costs is multiplied by every pane open. This is
// the instrument for measuring it, off unless asked for:
//
//	ENDLESS_MONITOR_PROFILE=<dir> endless-go session-status --monitor
//
// writes two files into <dir>:
//
//   - cpu.pprof — a runtime/pprof CPU profile of the whole run, flushed on a
//     clean exit (Ctrl-C). Read it with `go tool pprof`.
//   - ticks.log — one line per tick: the tick's wall time, this process's own
//     CPU and its children's CPU spent during it, and how many git and tmux
//     children and job-scheduling writes it issued.
//
// ENDLESS_NO_JOBS (internal/jobs) is the matching switch for ablating the job
// trigger. Wake-ups and threads are the kernel's to count: `top -l 10 -stats
// pid,cpu,idlew,th -pid <pid>`.
const profileEnv = "ENDLESS_MONITOR_PROFILE"

// tickProfiler is the diagnostic's state for one Loop. A nil *tickProfiler is
// the disabled diagnostic, and every method is a no-op on it, so Loop calls
// them unconditionally.
type tickProfiler struct {
	log   *os.File
	cpu   *os.File
	n     int
	start time.Time
	last  tickCounters
}

// tickCounters is one reading of everything a tick line diffs.
type tickCounters struct {
	self, children time.Duration
	git, tmux      int64
	jobWrites      int64
}

func readTickCounters() (c tickCounters) {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) == nil {
		c.self = rusageCPU(ru)
	}
	if syscall.Getrusage(syscall.RUSAGE_CHILDREN, &ru) == nil {
		c.children = rusageCPU(ru)
	}
	c.git, c.tmux = monitor.ExecCounts()
	c.jobWrites = jobs.WriteStatements()
	return c
}

func rusageCPU(ru syscall.Rusage) time.Duration {
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano())
}

// startProfiler starts the diagnostic when ENDLESS_MONITOR_PROFILE names a
// directory, and returns nil otherwise. A directory that cannot be used is an
// error: the person asked for a profile, and a view that ran without one would
// leave them reading a profile that was never written.
func startProfiler() (p *tickProfiler, err error) {
	dir := os.Getenv(profileEnv)
	if dir == "" {
		return nil, nil
	}
	p = &tickProfiler{}
	if err = os.MkdirAll(dir, 0o755); err != nil {
		goto fail
	}
	p.log, err = os.Create(filepath.Join(dir, "ticks.log"))
	if err != nil {
		goto fail
	}
	p.cpu, err = os.Create(filepath.Join(dir, "cpu.pprof"))
	if err != nil {
		goto fail
	}
	if err = pprof.StartCPUProfile(p.cpu); err != nil {
		goto fail
	}
	p.last = readTickCounters()
	return p, nil

fail:
	p.close()
	return nil, err
}

// begin marks the start of a tick.
func (p *tickProfiler) begin() {
	if p == nil {
		return
	}
	p.start = time.Now()
}

// end writes the line for the tick begin opened.
func (p *tickProfiler) end() {
	if p == nil {
		return
	}
	p.n++
	now := readTickCounters()
	fmt.Fprintf(p.log, "tick=%d wall=%s self_cpu=%s child_cpu=%s git=%d tmux=%d job_writes=%d\n",
		p.n,
		time.Since(p.start).Round(time.Microsecond),
		(now.self - p.last.self).Round(time.Microsecond),
		(now.children - p.last.children).Round(time.Microsecond),
		now.git-p.last.git,
		now.tmux-p.last.tmux,
		now.jobWrites-p.last.jobWrites,
	)
	p.last = now
}

// stop flushes the CPU profile. Called on Loop's clean exits.
func (p *tickProfiler) stop() {
	if p == nil {
		return
	}
	pprof.StopCPUProfile()
	p.close()
}

func (p *tickProfiler) close() {
	if p.cpu != nil {
		p.cpu.Close()
	}
	if p.log != nil {
		p.log.Close()
	}
}
