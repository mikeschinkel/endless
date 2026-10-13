package liveview

import (
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeVisibility answers Start and Check from fields a test sets, and records
// every call Loop makes.
type fakeVisibility struct {
	start  bool
	check  atomic.Bool
	checks atomic.Int64
	closed atomic.Int64

	mu     sync.Mutex
	missed []bool
}

func (f *fakeVisibility) Start() bool { return f.start }

func (f *fakeVisibility) Check() bool {
	f.checks.Add(1)
	return f.check.Load()
}

func (f *fakeVisibility) Missed(visible bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missed = append(f.missed, visible)
}

func (f *fakeVisibility) Close() { f.closed.Add(1) }

func (f *fakeVisibility) missedCalls() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.missed...)
}

// gatedLoop runs Loop on a goroutine with a fast tick, a fake gate and
// injected signal channels, and counts renders.
type gatedLoop struct {
	t       *testing.T
	vis     *fakeVisibility
	wake    chan os.Signal
	sigs    chan os.Signal
	renders atomic.Int64
	done    chan struct{}
}

func startGatedLoop(t *testing.T, vis *fakeVisibility, safetyNet time.Duration) *gatedLoop {
	t.Helper()
	t.Setenv(profileEnv, "")
	g := &gatedLoop{
		t:    t,
		vis:  vis,
		wake: make(chan os.Signal, 4),
		sigs: make(chan os.Signal, 1),
		done: make(chan struct{}),
	}
	cfg := LoopConfig{
		Render: func(w io.Writer, cols int, color bool) (int, error) {
			// A frame that differs every time, so every render paints.
			n := g.renders.Add(1)
			_, err := io.WriteString(w, time.Duration(n).String()+"\n")
			return 1, err
		},
		FallbackCols: 80,
		Out:          io.Discard,
		Tick:         5 * time.Millisecond,
		Visibility:   vis,
		SafetyNet:    safetyNet,
		wake:         g.wake,
		sigs:         g.sigs,
		Fatal:        func(err error) { t.Errorf("Loop fatal: %v", err) },
	}
	go func() {
		defer close(g.done)
		Loop(cfg)
	}()
	t.Cleanup(g.stop)
	return g
}

// stop ends the loop the way Ctrl-C does and waits for it.
func (g *gatedLoop) stop() {
	select {
	case <-g.done:
		return
	default:
	}
	g.sigs <- os.Interrupt
	select {
	case <-g.done:
	case <-time.After(5 * time.Second):
		g.t.Fatal("Loop did not return after SIGINT")
	}
}

// waitFor polls ok for up to two seconds.
func (g *gatedLoop) waitFor(what string, ok func() bool) {
	g.t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	g.t.Fatalf("timed out waiting for %s (renders=%d)", what, g.renders.Load())
}

// steady reports whether no render happens over a span many ticks long.
func (g *gatedLoop) steady() bool {
	before := g.renders.Load()
	time.Sleep(100 * time.Millisecond)
	return g.renders.Load() == before
}

// TestLoop_HiddenDoesNoWork pins ED-1606's core rule: a view that starts hidden
// renders nothing — so it queries nothing, runs no git and fires no job, all of
// which happen only inside a render — until it is told it is visible.
func TestLoop_HiddenDoesNoWork(t *testing.T) {
	vis := &fakeVisibility{start: false}
	g := startGatedLoop(t, vis, time.Hour)
	time.Sleep(100 * time.Millisecond) // twenty ticks' worth
	if n := g.renders.Load(); n != 0 {
		t.Fatalf("a hidden view rendered %d frames, want 0", n)
	}
}

// TestLoop_SignalsFlipVisibility pins the hook path: SIGUSR1 repaints at once
// and resumes the tick, SIGUSR2 stops it, and the exit clears the gate.
func TestLoop_SignalsFlipVisibility(t *testing.T) {
	vis := &fakeVisibility{start: false}
	g := startGatedLoop(t, vis, time.Hour)

	g.wake <- syscall.SIGUSR1
	g.waitFor("the repaint on becoming visible", func() bool { return g.renders.Load() >= 1 })
	g.waitFor("the tick to resume", func() bool { return g.renders.Load() >= 5 })

	g.wake <- syscall.SIGUSR2
	g.waitFor("the view to stop rendering once hidden", g.steady)

	g.wake <- syscall.SIGUSR1
	before := g.renders.Load()
	g.waitFor("the repaint on returning", func() bool { return g.renders.Load() > before })

	g.stop()
	if n := vis.closed.Load(); n != 1 {
		t.Errorf("gate closed %d times on exit, want 1", n)
	}
	if missed := vis.missedCalls(); len(missed) != 0 {
		t.Errorf("signals alone were logged as missed hooks: %v", missed)
	}
}

// TestLoop_SafetyNetCorrectsAMissedHook pins the safety net: a poll that
// disagrees with the signalled state flips it, in either direction, and logs
// the disagreement.
func TestLoop_SafetyNetCorrectsAMissedHook(t *testing.T) {
	vis := &fakeVisibility{start: false}
	g := startGatedLoop(t, vis, 20*time.Millisecond)
	if !g.steady() {
		t.Fatal("a hidden view whose polls agree rendered")
	}
	if vis.checks.Load() == 0 {
		t.Fatal("the safety net never polled")
	}

	// The SIGUSR1 that should have come did not; the poll notices.
	vis.check.Store(true)
	g.waitFor("the poll to make the view visible", func() bool { return g.renders.Load() >= 3 })

	// The SIGUSR2 that should have come did not either.
	vis.check.Store(false)
	g.waitFor("the poll to hide the view", g.steady)

	missed := vis.missedCalls()
	if len(missed) < 2 || !missed[0] || missed[len(missed)-1] {
		t.Errorf("missed-hook log = %v, want a true then a false", missed)
	}
}

// TestLoop_UngatedAlwaysRenders pins that a view with no gate — outside tmux,
// and every caller that does not ask — renders as it always has.
func TestLoop_UngatedAlwaysRenders(t *testing.T) {
	t.Setenv(profileEnv, "")
	var renders atomic.Int64
	sigs := make(chan os.Signal, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(LoopConfig{
			Render: func(w io.Writer, cols int, color bool) (int, error) {
				renders.Add(1)
				return 0, nil
			},
			Out:  io.Discard,
			Tick: 5 * time.Millisecond,
			sigs: sigs,
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for renders.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	sigs <- os.Interrupt
	<-done
	if renders.Load() < 3 {
		t.Fatalf("an ungated view rendered %d frames, want it ticking", renders.Load())
	}
}

// TestLoop_FatalClosesTheGateFirst pins that a render failure clears the pane
// tag before Fatal, which may end the process before any defer runs.
func TestLoop_FatalClosesTheGateFirst(t *testing.T) {
	t.Setenv(profileEnv, "")
	vis := &fakeVisibility{start: true}
	var closedAtFatal int64 = -1
	done := make(chan struct{})
	go func() {
		defer close(done)
		Loop(LoopConfig{
			Render: func(w io.Writer, cols int, color bool) (int, error) {
				return 0, errors.New("query failed")
			},
			Out:        io.Discard,
			Tick:       5 * time.Millisecond,
			Visibility: vis,
			wake:       make(chan os.Signal, 1),
			sigs:       make(chan os.Signal, 1),
			Fatal:      func(error) { closedAtFatal = vis.closed.Load() },
		})
	}()
	<-done
	if closedAtFatal != 1 {
		t.Fatalf("gate closed %d times when Fatal ran, want 1", closedAtFatal)
	}
}
