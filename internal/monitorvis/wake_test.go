package monitorvis

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
)

// tmuxServerIn points TMUX at a socket file in a fresh directory, so the wake
// files land somewhere private and the socket "exists".
func tmuxServerIn(t *testing.T) (socket string) {
	t.Helper()
	socket = filepath.Join(t.TempDir(), "endless-tasks")
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("socket stand-in: %v", err)
	}
	t.Setenv("TMUX", socket+",1,0")
	return socket
}

func noSleep(t *testing.T) *[]time.Duration {
	t.Helper()
	var slept []time.Duration
	prevSleep, prevJitter := sleep, jitter
	sleep = func(d time.Duration) { slept = append(slept, d) }
	jitter = func(d time.Duration) time.Duration { return d }
	t.Cleanup(func() { sleep, jitter = prevSleep, prevJitter })
	return &slept
}

// P6: of many wakes arriving at once, one runs at a time, and the burst costs
// far fewer runs than wakes.
func TestCoalesce_OneAtATime(t *testing.T) {
	files := wakeFilesFor(tmuxServerIn(t))
	var running, maxRunning, runs atomic.Int32

	wake := func() {
		n := running.Add(1)
		for {
			m := maxRunning.Load()
			if n <= m || maxRunning.CompareAndSwap(m, n) {
				break
			}
		}
		runs.Add(1)
		time.Sleep(20 * time.Millisecond)
		running.Add(-1)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			coalesce(files, wake)
		}()
	}
	close(start)
	wg.Wait()

	if m := maxRunning.Load(); m != 1 {
		t.Errorf("%d wakes ran at once, want 1", m)
	}
	if n := runs.Load(); n < 1 || n > 10 {
		t.Errorf("40 concurrent wakes ran %d times; want at least one and far fewer than 40", n)
	}
	if exists(files.rerun) {
		t.Error("a rerun flag was left unanswered")
	}
}

// P6: a wake that arrives while one runs makes it run once more.
func TestCoalesce_ArrivalDuringARunRerunsOnce(t *testing.T) {
	files := wakeFilesFor(tmuxServerIn(t))
	var runs atomic.Int32
	entered, release := make(chan struct{}), make(chan struct{})

	first := func() {
		if runs.Add(1) == 1 {
			close(entered)
			<-release
		}
	}
	done := make(chan struct{})
	go func() { coalesce(files, first); close(done) }()
	<-entered

	coalesce(files, func() { t.Error("the arriving wake ran beside the holder") })
	close(release)
	<-done

	if n := runs.Load(); n != 2 {
		t.Errorf("runs = %d, want 2: the first, and one rerun for the arrival", n)
	}
}

// P7: a refused connection — "no server running" with the socket present — is
// retried with backoff and succeeds without counting a failure.
func TestRunWake_RetriesARefusedConnection(t *testing.T) {
	tmuxServerIn(t)
	slept := noSleep(t)
	raised := useFaultCapture(t)
	var calls int
	useFakeTmux(t, func(args []string) (string, error) {
		calls++
		if calls < 4 {
			return "", errors.New("exit status 1: no server running on /tmp/x")
		}
		return "client main @1\n", nil
	})

	RunWake(nil)

	if calls != 4 {
		t.Errorf("tmux asked %d times, want 4", calls)
	}
	if len(*slept) != 3 || (*slept)[1] <= (*slept)[0] {
		t.Errorf("pauses = %v, want three, growing", *slept)
	}
	if len(*raised) != 0 {
		t.Errorf("raised %+v after a refusal that cleared", *raised)
	}
}

// P7: the retry gives up within its budget and then counts one failure.
func TestRunWake_RefusalOutlastingTheBudgetCountsOnce(t *testing.T) {
	tmuxServerIn(t)
	noSleep(t)
	raised := useFaultCapture(t)
	prev := jitter
	jitter = func(time.Duration) time.Duration { return refusedBudget } // next pause would pass the deadline
	t.Cleanup(func() { jitter = prev })
	useFakeTmux(t, func(args []string) (string, error) {
		return "", errors.New("no server running on /tmp/x")
	})

	RunWake(nil)

	if len(*raised) != 1 {
		t.Errorf("raised %d, want one counted failure", len(*raised))
	}
}

// A server that is gone — no socket file — is not retried.
func TestRunWake_AbsentServerIsNotRetried(t *testing.T) {
	socket := tmuxServerIn(t)
	_ = os.Remove(socket)
	slept := noSleep(t)
	useFaultCapture(t)
	useFakeTmux(t, func(args []string) (string, error) {
		return "", errors.New("no server running on " + socket)
	})
	RunWake(nil)
	if len(*slept) != 0 {
		t.Errorf("retried an absent server: %v", *slept)
	}
}

// P8, P2: failures count on the server's run, and a success closes it — the
// marker spares a healthy wake the database.
func TestVisibility_FailuresAreTransientAndASuccessClosesTheRun(t *testing.T) {
	socket := tmuxServerIn(t)
	var noted []faults.TransientRun
	var resolved []string
	prevR, prevS := recordTransient, resolveTransient
	recordTransient = func(r faults.TransientRun, _ faults.Fault) bool {
		noted = append(noted, r)
		return false
	}
	resolveTransient = func(source, fp string) bool {
		resolved = append(resolved, source+" "+fp)
		return true
	}
	t.Cleanup(func() { recordTransient, resolveTransient = prevR, prevS })

	visibilitySucceeded()
	if len(resolved) != 0 {
		t.Fatal("a success with no failure before it touched the transient store")
	}

	visibilityFailed(faults.Fault{Code: faults.ErrCodeMonitorVisibilityUnreadable})
	if len(noted) != 1 || noted[0].Threshold != VisibilityThreshold ||
		noted[0].Fingerprint != "monitor-visibility:"+socket {
		t.Fatalf("noted %+v", noted)
	}
	visibilitySucceeded()
	if len(resolved) != 1 || resolved[0] != "monitorvis monitor-visibility:"+socket {
		t.Fatalf("resolved %v", resolved)
	}
	visibilitySucceeded()
	if len(resolved) != 1 {
		t.Error("a second success resolved again")
	}
}
