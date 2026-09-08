package spawnlaunchcmd

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// fakeTmux records every tmux invocation the layout builder makes and hands
// back a fresh pane id for each split, mirroring `split-window -P -F
// '#{pane_id}'`.
type fakeTmux struct {
	calls [][]string
	next  int
	// failOn makes the invocation whose first arg matches return an error,
	// so the best-effort branches can be exercised.
	failOn string
}

func (f *fakeTmux) out(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	if f.failOn != "" && args[0] == f.failOn {
		return "", fmt.Errorf("tmux refused")
	}
	if args[0] == "split-window" {
		f.next++
		return fmt.Sprintf("%%new%d", f.next), nil
	}
	if args[0] == "display-message" {
		return "%claude", nil
	}
	return "", nil
}

func (f *fakeTmux) run(args ...string) error {
	f.calls = append(f.calls, args)
	if f.failOn != "" && args[0] == f.failOn {
		return fmt.Errorf("tmux refused")
	}
	return nil
}

func install(t *testing.T, f *fakeTmux) {
	t.Helper()
	origRun, origOut := tmuxRun, tmuxRunOut
	tmuxRun, tmuxRunOut = f.run, f.out
	t.Cleanup(func() { tmuxRun, tmuxRunOut = origRun, origOut })
}

// targets pulls the `-t <pane>` argument out of each recorded call, which is
// what says which pane each step was threaded off.
func targets(calls [][]string) []string {
	var out []string
	for _, c := range calls {
		for i, a := range c {
			if a == "-t" && i+1 < len(c) {
				out = append(out, c[i+1])
				break
			}
		}
	}
	return out
}

func verbs(calls [][]string) []string {
	out := make([]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c[0])
	}
	return out
}

// TestBuildLayoutAround_Sequence pins the three steps and, more importantly,
// what each is threaded off: the SHELL pane is split from the anchor, the
// monitor is split from the SHELL pane (not from the anchor), and focus returns
// to the anchor. Splitting the monitor first would race its own self-shrink and
// leave the shell with whatever rows survived — see buildLayoutAround.
func TestBuildLayoutAround_Sequence(t *testing.T) {
	f := &fakeTmux{}
	install(t, f)

	buildLayoutAround("%claude", "")

	if got, want := verbs(f.calls),
		[]string{"split-window", "split-window", "select-pane"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("verbs = %q, want %q", got, want)
	}
	if got, want := targets(f.calls),
		[]string{"%claude", "%new1", "%claude"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("targets = %q, want %q", got, want)
	}
}

// TestBuildLayoutAround_MonitorRunsInItsPane pins that the second split is the
// one carrying `session monitor`, and the first carries no command at all (a
// bare interactive shell).
func TestBuildLayoutAround_MonitorRunsInItsPane(t *testing.T) {
	f := &fakeTmux{}
	install(t, f)

	buildLayoutAround("%claude", "")

	shell, monitor := f.calls[0], f.calls[1]
	if contains(shell, "--") {
		t.Errorf("shell pane got a command: %q", shell)
	}
	if !strings.Contains(strings.Join(monitor, " "), "session monitor") {
		t.Errorf("monitor pane runs %q", monitor)
	}
}

// TestBuildLayoutAround_FocusReturnsAfterAFailedMonitor pins the fall-through:
// the monitor split is allowed to fail, and a two-pane window still wants focus
// back on the anchor.
func TestBuildLayoutAround_FocusReturnsAfterAFailedMonitor(t *testing.T) {
	f := &failAfterFirstSplit{}
	origRun, origOut := tmuxRun, tmuxRunOut
	tmuxRun, tmuxRunOut = f.run, f.out
	t.Cleanup(func() { tmuxRun, tmuxRunOut = origRun, origOut })

	buildLayoutAround("%claude", "")

	if f.focused != "%claude" {
		t.Fatalf("focus returned to %q, want the anchor after a failed monitor split",
			f.focused)
	}
}

// TestBuildLayoutAround_ShellFailureStopsEarly pins that a failed FIRST split
// returns without trying to split a pane that does not exist.
func TestBuildLayoutAround_ShellFailureStopsEarly(t *testing.T) {
	f := &fakeTmux{failOn: "split-window"}
	install(t, f)

	buildLayoutAround("%claude", "")

	if len(f.calls) != 1 {
		t.Fatalf("calls = %q, want to stop after the failed split", f.calls)
	}
}

// E-2125 removed buildSpawnLayout, and with it the test that pinned it here.
// It asserted that spawn resolved window name -> active pane before splitting,
// which is the defect: a window name is not unique across sessions, so tmux
// answered with whichever window the server considered current. new-window now
// reports the pane it created (`-P -F '#{pane_id}'`, pinned in
// tmux_driver_test.go) and runSpawnWindow hands that id straight to
// buildLayoutAround, so there is no lookup left to test.

// failAfterFirstSplit lets the shell split succeed and refuses the monitor one.
type failAfterFirstSplit struct {
	splits  int
	focused string
}

func (f *failAfterFirstSplit) out(args ...string) (string, error) {
	if args[0] == "split-window" {
		f.splits++
		if f.splits == 2 {
			return "", fmt.Errorf("tmux refused")
		}
		return "%shell", nil
	}
	return "", nil
}

func (f *failAfterFirstSplit) run(args ...string) error {
	if args[0] == "select-pane" {
		f.focused = args[len(args)-1]
	}
	return nil
}

func contains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
