package liveview

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
)

// Restarting a live view onto its replaced binary (E-2193).
//
// A monitor runs for days and keeps the binary it started with. After E-1993
// retired the triage-sufficiency job, twelve monitors started before the land
// kept firing it — calling the removed `endless triage run` and raising
// WARN-0001 — until each pane was restarted by hand. The monitor is also what
// fires background jobs, so a stale one is not just a stale picture: it runs
// jobs the new install may have retired.
//
// So Loop watches the file it was started from. When that file is replaced and
// the replacement has held still for two ticks, the loop probes it and re-execs
// it in place — same pane, same PID, so the Python launcher waiting on the
// process never notices. When the replacement cannot start, the old binary keeps
// rendering, stops firing jobs for good, says so in the frame, and records
// WARN-0019.

// startArgs is this process's argv as the kernel handed it over, captured before
// main runs. main strips its --db flags out of os.Args (monitor.ConsumeDBFlags)
// before any subcommand is dispatched, so os.Args at loop time is NOT the
// command line that started the view — re-exec'ing it would silently drop
// `--db main`. Package initialisation runs before main, which is what makes this
// the original.
var startArgs = append([]string(nil), os.Args...)

// startDir is the working directory at the same moment, for resolving a
// relative argv[0].
var startDir, _ = os.Getwd()

// probeTimeout bounds the replacement's trial run. The probe is `--help`, which
// touches no database; a binary that cannot print its usage in this long is not
// one to hand the pane to.
const probeTimeout = 5 * time.Second

// binaryIdentity is what "the same file" means here: the file a path resolves
// to, and its device, inode, size and mtime. An install that rewrites the file,
// renames a new one over it, or repoints a symlink at another file all change
// at least one of them.
type binaryIdentity struct {
	resolved string
	dev      uint64
	ino      uint64
	size     int64
	mtime    time.Time
}

// identityOf resolves path through any symlinks and stats what it lands on.
//
// Resolved afresh on every call, never once at startup: an install may repoint
// the symlink (`/usr/local/bin/endless-go` → a checkout's `bin/`) and leave the
// file it used to point at untouched, and comparing against the file resolved at
// startup would miss exactly that.
func identityOf(path string) (id binaryIdentity, err error) {
	var fi os.FileInfo
	var st *syscall.Stat_t
	var ok bool

	id.resolved, err = filepath.EvalSymlinks(path)
	if err != nil {
		goto end
	}
	fi, err = os.Stat(id.resolved)
	if err != nil {
		goto end
	}
	id.size = fi.Size()
	id.mtime = fi.ModTime()
	st, ok = fi.Sys().(*syscall.Stat_t)
	if ok {
		id.dev = uint64(st.Dev)
		id.ino = uint64(st.Ino)
	}

end:
	return id, err
}

// watchedPath is the path this process was started through — the one an
// install replaces, and the one a re-exec runs again.
//
// argv[0] first, because it is the name the launcher used: on Linux
// os.Executable reads /proc/self/exe, which is already resolved past any
// symlink, so watching it would never see an install that repoints one. argv[0]
// is trusted only when it names the file actually running — anyone can exec a
// process with an arbitrary argv[0] — and os.Executable is the fallback.
//
// Neither is a name this package chose. Nothing here spells `endless-go`, so the
// check survives E-1063 renaming the binary to `endless`.
func watchedPath() (path string, err error) {
	var exe, candidate string
	var exeID, candID binaryIdentity

	exe, err = os.Executable()
	if err != nil {
		goto end
	}
	path = exe
	if len(startArgs) == 0 {
		goto end
	}
	candidate, err = exec.LookPath(startArgs[0])
	if err != nil {
		err = nil
		goto end
	}
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(startDir, candidate)
	}
	exeID, err = identityOf(exe)
	if err != nil {
		err = nil
		goto end
	}
	candID, err = identityOf(candidate)
	if err != nil {
		err = nil
		goto end
	}
	if candID == exeID {
		path = candidate
	}

end:
	return path, err
}

// restarter is the per-loop state of the watch. Its seams (probe, exec, record,
// identify) are what let the tests drive every branch without replacing the
// test binary.
type restarter struct {
	path  string // the watched path; see watchedPath
	start binaryIdentity

	pending    binaryIdentity // a changed identity seen on the previous tick
	hasPending bool
	failedOn   binaryIdentity // the replacement that last failed to start
	hasFailed  bool

	// notice is the one line the frame carries once a restart has failed. Its
	// being non-empty is also what stops job firing, for the rest of the
	// process's life.
	notice string

	identify func(path string) (binaryIdentity, error)
	probe    func(path string) error
	exec     func(path string, argv, env []string) error
	record   func(faults.Fault)
	// beforeExec hands the terminal back before the process image is replaced.
	// afterFailedExec takes it again when the exec did not happen.
	beforeExec      func()
	afterFailedExec func()
}

// newRestarter watches the running binary. It returns nil — no watch, and a
// loop that behaves exactly as it did before E-2193 — when the running binary
// cannot be located, since there is then nothing to compare against.
func newRestarter(beforeExec, afterFailedExec func()) (r *restarter) {
	var path string
	var start binaryIdentity
	var err error

	path, err = watchedPath()
	if err != nil {
		goto end
	}
	start, err = identityOf(path)
	if err != nil {
		goto end
	}
	r = &restarter{
		path:            path,
		start:           start,
		identify:        identityOf,
		probe:           probeBinary,
		exec:            syscall.Exec,
		record:          faults.Record,
		beforeExec:      beforeExec,
		afterFailedExec: afterFailedExec,
	}

end:
	return r
}

// jobsAllowed reports whether this process may still fire background jobs. A
// nil restarter (no watch) always may.
func (r *restarter) jobsAllowed() bool {
	return r == nil || r.notice == ""
}

// Notice is the line to append to the frame, or "" when there is nothing to say.
func (r *restarter) Notice() string {
	if r == nil {
		return ""
	}
	return r.notice
}

// tick runs one check and reports whether jobs may fire on this tick.
//
// jobsBusy says a job is still running on its goroutine. A ready replacement
// then waits a tick rather than exec'ing over it: an exec ends the process
// mid-job, which leaves the job's lease held and its work half done. No new job
// is fired while it waits, so the wait ends.
//
// On success tick does not return — the process image is replaced.
func (r *restarter) tick(jobsBusy bool) (fire bool) {
	var id binaryIdentity
	var err error

	fire = r.jobsAllowed()
	if r == nil {
		goto end
	}

	id, err = r.identify(r.path)
	if err != nil {
		// Mid-install the path can briefly not exist. Not a replacement yet, and
		// not a failure; the next tick looks again.
		r.hasPending = false
		goto end
	}
	if id == r.start || (r.hasFailed && id == r.failedOn) {
		r.hasPending = false
		goto end
	}
	if !r.hasPending || id != r.pending {
		// Changed, but not yet seen twice: it may be a half-written build.
		r.pending, r.hasPending = id, true
		goto end
	}

	// Changed and stable. Never fire jobs on the tick that decides to exec.
	fire = false
	if jobsBusy {
		goto end
	}
	r.restart(id)

end:
	return fire
}

// restart probes the replacement and execs it. It returns only on failure,
// having recorded it.
func (r *restarter) restart(id binaryIdentity) {
	var err error
	var stage string

	stage = "probe"
	err = r.probe(r.path)
	if err != nil {
		goto failed
	}
	stage = "exec"
	if r.beforeExec != nil {
		r.beforeExec()
	}
	// The watched path, not the file it resolves to: the new process must watch
	// what this one watched, or the NEXT symlink repoint goes unseen.
	err = r.exec(r.path, startArgs, os.Environ())
	if r.afterFailedExec != nil {
		r.afterFailedExec()
	}
	if err == nil {
		// Only a test's exec seam returns nil; the real one never returns on
		// success. Nothing failed, so there is nothing to report.
		goto end
	}

failed:
	r.fail(id, stage, err)

end:
	return
}

// fail stops job firing, sets the notice and records the fault — once per
// replacement, so a 2s tick does not re-record it. A LATER replacement is tried
// afresh: the build after a broken one is usually the fix, and restarting onto
// it is how this process's job firing comes back.
func (r *restarter) fail(id binaryIdentity, stage string, err error) {
	r.failedOn, r.hasFailed = id, true
	r.hasPending = false
	r.notice = fmt.Sprintf("binary replaced but monitor could not restart (%s: %s) — background jobs stopped; restart this monitor",
		stage, firstLine(err.Error()))
	r.record(faults.Fault{
		Code:        faults.ErrCodeMonitorRestartFailed,
		Source:      "liveview",
		Fingerprint: "monitor-restart:" + id.resolved,
		Summary:     "monitor could not restart onto its replaced binary",
		Detail:      err.Error(),
		Fields: map[string]any{
			"stage":    stage,
			"path":     r.path,
			"resolved": id.resolved,
			"pid":      os.Getpid(),
		},
	})
}

// probeBinary runs path with --help under probeTimeout and fails on a non-zero
// exit or a timeout. --help is the cheapest thing every build of the binary
// answers: it touches no database and fires nothing.
func probeBinary(path string) (err error) {
	var stderr bytes.Buffer

	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "--help")
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		err = fmt.Errorf("%s --help did not finish within %s", path, probeTimeout)
		goto end
	}
	if err != nil && stderr.Len() > 0 {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}

end:
	return err
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
