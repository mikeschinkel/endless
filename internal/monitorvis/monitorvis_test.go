package monitorvis

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/upid"
)

// fakeTmux answers runTmux from a func and records every invocation.
type fakeTmux struct {
	calls  [][]string
	answer func(args []string) (string, error)
}

func useFakeTmux(t *testing.T, answer func(args []string) (string, error)) *fakeTmux {
	t.Helper()
	f := &fakeTmux{answer: answer}
	prev := runTmux
	runTmux = func(args ...string) (string, error) {
		f.calls = append(f.calls, args)
		return f.answer(args)
	}
	t.Cleanup(func() { runTmux = prev })
	return f
}

func useFaultCapture(t *testing.T) *[]faults.Fault {
	t.Helper()
	var got []faults.Fault
	prev := record
	record = func(f faults.Fault) { got = append(got, f) }
	t.Cleanup(func() { record = prev })
	return &got
}

// installs returns the hooks a fake tmux was asked to append to.
func (f *fakeTmux) installs() (hooks []string) {
	for _, c := range f.calls {
		if len(c) == 4 && c[0] == "set-hook" && c[1] == "-ga" {
			hooks = append(hooks, c[2])
		}
	}
	return hooks
}

// liveBin is a binary path that exists — this test binary — so hooks naming
// it are not stale.
var liveBin = os.Args[0]

func allHooksOutput(extra string) string {
	var b strings.Builder
	for _, hook := range HookNames {
		b.WriteString(hook + "[0] run-shell -b \"" + liveBin + " monitor-wake\"\n")
	}
	return b.String() + extra
}

func TestPollArgs_IsOneInvocation(t *testing.T) {
	args := pollArgs("%7")
	want := []string{
		"display-message", "-p", "-t", "%7", "self #{window_id}", ";",
		"list-clients", "-F", "client #{client_session} #{window_id}",
		";", "show-hooks", "-g", "session-window-changed",
		";", "show-hooks", "-g", "client-session-changed",
		";", "show-hooks", "-g", "client-attached",
		";", "show-hooks", "-g", "client-detached",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("pollArgs =\n%q\nwant\n%q", args, want)
	}
}

func TestInstallHookArgs_AppendsRatherThanReplaces(t *testing.T) {
	got := installHookArgs("client-attached", "/opt/bin/endless-go")
	want := []string{"set-hook", "-ga", "client-attached", "run-shell -b '/opt/bin/endless-go monitor-wake'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("installHookArgs = %q, want %q", got, want)
	}
}

// TestParsePoll pins visibility and hook recognition, including our hook
// sitting among a user's own on the same name.
func TestParsePoll(t *testing.T) {
	out := strings.Join([]string{
		"self @3",
		"client main @1",
		"client work @3",
		"session-window-changed[0] run-shell \"~/bin/my-own-thing\"",
		"session-window-changed[1] run-shell -b \"" + liveBin + " monitor-wake\"",
		"client-session-changed",
		"client-attached[0] run-shell -b \"/elsewhere/gone/endless-go monitor-wake\"",
		"client-detached[4] run-shell \"echo monitor-wakeful\"",
		"",
	}, "\n")
	s := parsePoll(out)
	if s.self != "@3" {
		t.Errorf("self = %q, want @3", s.self)
	}
	if !s.visible["@1"] || !s.visible["@3"] || s.visible["@2"] {
		t.Errorf("visible = %v, want @1 and @3 only", s.visible)
	}
	// client-attached's only wake hook names a binary that is gone, so it is
	// stale and the hook counts as missing.
	if got, want := s.missingHooks(), []string{"client-session-changed", "client-attached", "client-detached"}; !reflect.DeepEqual(got, want) {
		t.Errorf("missingHooks = %v, want %v", got, want)
	}
	if got := s.entries["session-window-changed"]; !reflect.DeepEqual(got, []string{"session-window-changed[1]"}) {
		t.Errorf("entries = %v, want only our [1]", got)
	}
	if !reflect.DeepEqual(s.stale, []string{"client-attached[0]"}) {
		t.Errorf("stale = %v, want client-attached[0]", s.stale)
	}
}

// TestGateStart_InstallsQuietly pins that the first monitor on a server
// installs every hook without raising anything, and tags its pane.
func TestGateStart_InstallsQuietly(t *testing.T) {
	raised := useFaultCapture(t)
	f := useFakeTmux(t, func(args []string) (string, error) {
		if args[0] == "display-message" {
			return "self @2\nclient main @2\n", nil
		}
		return "", nil
	})
	g := &Gate{pane: "%5", bin: "/opt/bin/endless-go"}
	if !g.Start() {
		t.Error("a monitor in a client's current window started hidden")
	}
	if got := f.installs(); !reflect.DeepEqual(got, HookNames) {
		t.Errorf("installed %v, want every hook", got)
	}
	if len(*raised) != 0 {
		t.Errorf("a first install raised %v", *raised)
	}
	if self, err := upid.Self(); err == nil {
		if f.calls[0][0] != "set-option" || f.calls[0][len(f.calls[0])-1] != self.String() {
			t.Errorf("first call = %q, want the pane tagged with this UPID", f.calls[0])
		}
	}
}

// TestGateCheck_ReinstallsMissingHooksAndRecords pins the poll's repair: a hook
// gone since startup is put back and raises WARN-0034; hooks present add
// nothing, which is what makes the install idempotent.
func TestGateCheck_ReinstallsMissingHooksAndRecords(t *testing.T) {
	raised := useFaultCapture(t)
	hooks := allHooksOutput("")
	f := useFakeTmux(t, func(args []string) (string, error) {
		if args[0] == "display-message" {
			return "self @2\nclient main @1\n" + hooks, nil
		}
		return "", nil
	})
	g := &Gate{pane: "%5", bin: "/opt/bin/endless-go"}

	if g.Check() {
		t.Error("a monitor in no client's window polled visible")
	}
	if len(f.installs()) != 0 || len(*raised) != 0 {
		t.Fatalf("hooks already present: installed %v, raised %v", f.installs(), *raised)
	}

	hooks = strings.Replace(hooks, "client-detached[0] run-shell -b \""+liveBin+" monitor-wake\"\n", "client-detached\n", 1)
	g.Check()
	if got := f.installs(); !reflect.DeepEqual(got, []string{"client-detached"}) {
		t.Errorf("reinstalled %v, want only client-detached", got)
	}
	if len(*raised) != 1 || (*raised)[0].Code.ID != "WARN-0034" {
		t.Errorf("raised %+v, want one WARN-0034", *raised)
	}
}

// TestGateCheck_RemovesDuplicateHooks pins the cleanup for monitors started
// together: every entry of ours after the lowest index is unset.
func TestGateCheck_RemovesDuplicateHooks(t *testing.T) {
	useFaultCapture(t)
	hooks := allHooksOutput("session-window-changed[3] run-shell -b \"" + liveBin + " monitor-wake\"\n")
	f := useFakeTmux(t, func(args []string) (string, error) {
		if args[0] == "display-message" {
			return "self @2\n" + hooks, nil
		}
		return "", nil
	})
	(&Gate{pane: "%5", bin: "/opt/bin/endless-go"}).Check()
	var unset [][]string
	for _, c := range f.calls {
		if c[0] == "set-hook" && c[1] == "-gu" {
			unset = append(unset, c)
		}
	}
	if want := [][]string{{"set-hook", "-gu", "session-window-changed[3]"}}; !reflect.DeepEqual(unset, want) {
		t.Errorf("unset %q, want %q", unset, want)
	}
}

// TestGateCheck_TmuxFailureFailsVisible pins the failure direction: a monitor
// that cannot ask draws, and the failure is recorded.
func TestGateCheck_TmuxFailureFailsVisible(t *testing.T) {
	raised := useFaultCapture(t)
	useFakeTmux(t, func(args []string) (string, error) {
		return "", errors.New("no server running")
	})
	if !(&Gate{pane: "%5", bin: "x"}).Check() {
		t.Error("a monitor whose poll failed went hidden")
	}
	if len(*raised) != 1 || (*raised)[0].Code.ID != "WARN-0033" {
		t.Errorf("raised %+v, want one WARN-0033", *raised)
	}
}

// TestGateCheck_ReplacesStaleHooks pins the repair for a hook left by a binary
// since deleted: it is unset, and ours is installed in its place.
func TestGateCheck_ReplacesStaleHooks(t *testing.T) {
	raised := useFaultCapture(t)
	hooks := strings.Replace(allHooksOutput(""),
		"client-detached[0] run-shell -b \""+liveBin+" monitor-wake\"",
		"client-detached[0] run-shell -b \"/elsewhere/gone/endless-go monitor-wake\"", 1)
	f := useFakeTmux(t, func(args []string) (string, error) {
		if args[0] == "display-message" {
			return "self @2\n" + hooks, nil
		}
		return "", nil
	})
	(&Gate{pane: "%5", bin: liveBin}).Check()
	var unset [][]string
	for _, c := range f.calls {
		if c[0] == "set-hook" && c[1] == "-gu" {
			unset = append(unset, c)
		}
	}
	if want := [][]string{{"set-hook", "-gu", "client-detached[0]"}}; !reflect.DeepEqual(unset, want) {
		t.Errorf("unset %q, want %q", unset, want)
	}
	if got := f.installs(); !reflect.DeepEqual(got, []string{"client-detached"}) {
		t.Errorf("installed %v, want client-detached", got)
	}
	if len(*raised) != 1 || (*raised)[0].Code.ID != "WARN-0034" {
		t.Errorf("raised %+v, want one WARN-0034", *raised)
	}
}

func TestWakeTargets(t *testing.T) {
	out := strings.Join([]string{
		"pane @1 101@9",
		"pane @1 ",
		"pane @2 202@9",
		"pane @3 303@9",
		"pane @3 garbage",
		"client main @1",
		"client work @3",
	}, "\n")
	show, hide := WakeTargets(parseWake(out))
	if want := []upid.UPID{{PID: 101, Start: 9}, {PID: 303, Start: 9}}; !reflect.DeepEqual(show, want) {
		t.Errorf("show = %v, want %v", show, want)
	}
	if want := []upid.UPID{{PID: 202, Start: 9}}; !reflect.DeepEqual(hide, want) {
		t.Errorf("hide = %v, want %v", hide, want)
	}
}

// TestRunWake_SignalsOnlyLiveMonitors pins the hook command end to end over a
// fake tmux: the right signal to each live monitor, nothing to a dead tag.
func TestRunWake_SignalsOnlyLiveMonitors(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	useFaultCapture(t)
	useFakeTmux(t, func(args []string) (string, error) {
		return "pane @1 101@9\npane @2 202@9\npane @2 404@9\nclient main @1\n", nil
	})
	type sent struct {
		pid int
		sig syscall.Signal
	}
	var got []sent
	prevAlive, prevKill := alive, kill
	alive = func(u upid.UPID) bool { return u.PID != 404 }
	kill = func(pid int, sig syscall.Signal) error {
		got = append(got, sent{pid, sig})
		return nil
	}
	t.Cleanup(func() { alive, kill = prevAlive, prevKill })

	RunWake(nil)
	want := []sent{{101, syscall.SIGUSR1}, {202, syscall.SIGUSR2}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("signals = %v, want %v", got, want)
	}
}

// TestRunWake_FailureIsRecordedNotPrinted pins that the hook command never
// fails loudly: a tmux error becomes WARN-0033 and the command returns.
func TestRunWake_FailureIsRecordedNotPrinted(t *testing.T) {
	t.Setenv("TMUX", "/tmp/tmux-1/default,1,0")
	raised := useFaultCapture(t)
	useFakeTmux(t, func(args []string) (string, error) { return "", errors.New("lost server") })
	RunWake(nil)
	if len(*raised) != 1 || (*raised)[0].Code.ID != "WARN-0033" {
		t.Errorf("raised %+v, want one WARN-0033", *raised)
	}
}

func TestRunWake_OutsideTmuxDoesNothing(t *testing.T) {
	t.Setenv("TMUX", "")
	f := useFakeTmux(t, func(args []string) (string, error) { return "", nil })
	RunWake(nil)
	if len(f.calls) != 0 {
		t.Errorf("monitor-wake outside tmux ran %q", f.calls)
	}
}
