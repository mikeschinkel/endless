package monitorvis

import (
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
)

// Coalescing wakes, retrying a refused connection, and transient WARN-0033
// (E-2589).
//
// # The burst this answers
//
// Every hook event — client-attached, client-detached, client-session-changed,
// session-window-changed — runs its own `run-shell -b endless-go monitor-wake`.
// A run of events piles concurrent wakes onto the tmux server, and a server
// busy for a moment refuses whatever exceeds its listen backlog: on macOS
// kern.ipc.somaxconn is 128, and a server frozen for one second refused 123 of
// 250 concurrent clients. tmux prints "no server running on <socket>" for ANY
// refused connection, not only for an absent server. WARN-0033 collected 184
// such occurrences in two one-minute bursts from a server that ran without a
// break throughout.
//
// Three changes, each closing part of it:
//
//   - Wakes coalesce. Each wake only asks "what is on screen now", so two
//     running at once answer the same question twice. A wake takes a
//     non-blocking lock beside the server's socket; one that finds it held
//     leaves a rerun flag and exits, and the holder runs once more for every
//     flag it finds, so the last event's answer is always the one applied.
//   - A refused connection — "no server running" while the socket file
//     exists — is retried with jittered backoff for about two seconds before
//     it counts as a failure.
//   - A failure that survives both is a transient occurrence, not a fault:
//     WARN-0033 is raised only once VisibilityThreshold of them come with no
//     success between.

// VisibilityThreshold is how many visibility queries in a row may fail before
// WARN-0033 is raised.
const VisibilityThreshold = 3

// refusedBudget bounds how long a refused connection is retried.
const refusedBudget = 2 * time.Second

// The first and largest pause between retries of a refused connection, before
// jitter.
const (
	refusedFirstDelay = 25 * time.Millisecond
	refusedMaxDelay   = 400 * time.Millisecond
)

// The seams tests steer: the transient store, the clock's sleep, and jitter.
var (
	recordTransient  = faults.RecordTransient
	resolveTransient = faults.ResolveTransient
	sleep            = time.Sleep
	jitter           = func(d time.Duration) time.Duration {
		return d/2 + time.Duration(rand.Int64N(int64(d)+1))
	}
)

// wakeFiles names the files that coordinate one tmux server's wakes. They sit
// beside its socket — the server's runtime directory, private to its user —
// hidden, and regular files, so nothing that scans for sockets there sees them.
type wakeFiles struct {
	lock    string // flock'd by the running wake
	rerun   string // exists while a wake has arrived that no run has answered
	failing string // exists while the server's transient run may be open
}

func wakeFilesFor(socket string) (f wakeFiles) {
	base := filepath.Join(filepath.Dir(socket), ".endless-wake-"+filepath.Base(socket))
	return wakeFiles{lock: base + ".lock", rerun: base + ".rerun", failing: base + ".failing"}
}

// coalesce runs wake so that one runs at a time per server, and a wake that
// arrives while one runs makes the running one go again instead of running
// beside it.
//
// The arriving wake announces itself BEFORE trying the lock. So a wake that
// loses the lock has already left its flag, and the holder — which checks for
// the flag after each run and again after unlocking — cannot miss it. A wake
// that finds its own flag already consumed was answered by a run that started
// after it arrived, and has nothing left to do.
//
// When the coordination files cannot be used at all — an unwritable directory
// — the wake simply runs, as it did before.
func coalesce(f wakeFiles, wake func()) {
	if touch(f.rerun) != nil {
		wake()
		return
	}
	for {
		unlock, held, err := tryLock(f.lock)
		if err != nil {
			wake()
			return
		}
		if held {
			return
		}
		for exists(f.rerun) {
			_ = os.Remove(f.rerun)
			wake()
		}
		unlock()
		if !exists(f.rerun) {
			return
		}
	}
}

// tryLock takes an exclusive non-blocking flock on path. held reports that
// another process holds it; err that the lock could not be tried at all.
func tryLock(path string) (unlock func(), held bool, err error) {
	var file *os.File

	file, err = os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	err = syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) {
		_ = file.Close()
		return nil, true, nil
	}
	if err != nil {
		_ = file.Close()
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
		_ = file.Close()
	}, false, nil
}

func touch(path string) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	return file.Close()
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// runTmuxRetrying is runTmux, retried with jittered backoff for up to
// refusedBudget while the server refuses the connection.
func runTmuxRetrying(args ...string) (out string, err error) {
	deadline := time.Now().Add(refusedBudget)
	delay := refusedFirstDelay
	for {
		out, err = runTmux(args...)
		if err == nil || !refused(err) {
			return out, err
		}
		pause := jitter(delay)
		if time.Now().Add(pause).After(deadline) {
			return out, err
		}
		sleep(pause)
		delay = min(2*delay, refusedMaxDelay)
	}
}

// refused reports whether err is the server refusing a connection rather than
// being gone: tmux says "no server running" for both, and only an existing
// socket file tells them apart.
func refused(err error) bool {
	socket := serverKey()
	return socket != "" && strings.Contains(err.Error(), "no server running") && exists(socket)
}

// visibilityRun is this server's transient run of failed visibility queries.
func visibilityRun() faults.TransientRun {
	return faults.TransientRun{
		Source:      "monitorvis",
		Fingerprint: "monitor-visibility:" + serverKey(),
		Threshold:   VisibilityThreshold,
	}
}

// visibilityFailed counts one failed visibility query on the server's run, and
// raises f (WARN-0033) once the run reaches VisibilityThreshold. The failing
// marker tells a later success there may be a run to close.
func visibilityFailed(f faults.Fault) {
	if socket := serverKey(); socket != "" {
		_ = touch(wakeFilesFor(socket).failing)
	}
	recordTransient(visibilityRun(), f)
}

// visibilitySucceeded closes the server's run after failures. It costs a stat
// and nothing else when there were none, which is every window switch on a
// healthy server: a wake must not open the database just to find nothing to
// close.
func visibilitySucceeded() {
	socket := serverKey()
	if socket == "" {
		return
	}
	marker := wakeFilesFor(socket).failing
	if !exists(marker) {
		return
	}
	run := visibilityRun()
	resolveTransient(run.Source, run.Fingerprint)
	_ = os.Remove(marker)
}
