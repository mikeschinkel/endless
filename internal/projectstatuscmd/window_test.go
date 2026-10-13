package projectstatuscmd

import (
	"strings"
	"testing"
)

// The tmux argv builders are pure functions precisely so the layout can be
// pinned without a tmux server: the shape of these commands is where a launcher
// silently does the wrong thing (targets the wrong pane, attaches instead of
// switching, steals focus), and none of that is observable from a test that has
// to spawn a real session.

func joined(args []string) string { return strings.Join(args, " ") }

var demoLayout = windowLayout{
	Session:    "console",
	Window:     "demo",
	Dir:        "/tmp/demo",
	MonitorCmd: []string{"endless", "project", "monitor", "demo"},
}

// TestTheFirstWindowIsAShell pins what the console's new-session is given
// when the project window is its first: the window's name and directory, and
// NO command. The session's first pane is the user's shell, because the SHELL
// is created first and the monitor inserted above it. Creating the monitor
// first and splitting a shell off it races the monitor's own self-resize —
// E-1851 learned that in the spawn layout, and this launcher shipped with it
// backwards (E-1976). The symptom is a window with no second pane at all.
func TestTheFirstWindowIsAShell(t *testing.T) {
	got := newSessionExtra(demoLayout)
	if joined(got) != "-n demo -c /tmp/demo" {
		t.Errorf("new-session extra = %v, want the window name and dir only", got)
	}
	if got := newSessionExtra(windowLayout{Window: "demo"}); joined(got) != "-n demo" {
		t.Errorf("an empty dir still produced a -c flag: %v", got)
	}
}

// TestSplitMonitorArgsInsertsAboveTheShell is the ordering rule, stated as an
// assertion so it cannot quietly revert: -b puts the new pane ABOVE its target,
// and the target is the shell's PANE ID rather than the session (which would
// resolve to whatever pane happened to be active).
func TestSplitMonitorArgsInsertsAboveTheShell(t *testing.T) {
	got := splitMonitorArgs(demoLayout, "%42")
	if !strings.Contains(joined(got), "split-window -v -b -t %42") {
		t.Errorf("the monitor is not inserted above the shell pane: %v", got)
	}
	// `--` terminates tmux flag parsing so the monitor command reaches execvp
	// literally, with no shell re-quoting of its arguments.
	if i := indexOf(got, "--"); i < 0 || joined(got[i+1:]) != "endless project monitor demo" {
		t.Errorf("the monitor command is not passed literally after --: %v", got)
	}
	// -l is the monitor's starting height. The monitor never resizes its pane
	// after that (E-2156), so without it the layout would start at tmux's even
	// split and stay there until the user dragged it.
	if i := indexOf(got, "-l"); i < 0 || got[i+1] != "65%" {
		t.Errorf("the split does not set the monitor's starting height: %v", got)
	}
}

// TestFocusReturnsToTheShell: tmux makes a new split active, so without this the
// monitor — which is read, never typed in — would hold the cursor.
func TestFocusReturnsToTheShell(t *testing.T) {
	if joined(selectPaneArgs("%42")) != "select-pane -t %42" {
		t.Errorf("focus is not handed back to a named pane: %v", selectPaneArgs("%42"))
	}
}

// TestTheWindowIsNamedForItsProject: the window carries the project's name,
// folded to a legal tmux name, and -n keeps tmux from renaming it to the shell.
func TestTheWindowIsNamedForItsProject(t *testing.T) {
	args := newWindowArgs(layoutFor("my.project", "/tmp/p"))
	if i := indexOf(args, "-n"); i < 0 || args[i+1] != "my-project" {
		t.Errorf("new-window does not name its window for the project: %v", args)
	}
	if indexOf(args, "-P") < 0 {
		t.Errorf("new-window does not report the shell pane's id: %v", args)
	}
}

// TestNewWindowTargetsTheConsoleExactly: `=console:` is the session itself,
// matched exactly.
func TestNewWindowTargetsTheConsoleExactly(t *testing.T) {
	if !strings.Contains(joined(newWindowArgs(demoLayout)), "-t =console:") {
		t.Errorf("new-window does not target the console exactly: %v", newWindowArgs(demoLayout))
	}
}

// TestMonitorCommandNamesTheProject: the pane must not rely on cwd resolution.
// Its directory is the main checkout today, but a launcher that depends on that
// coincidence breaks the moment the layout's home moves — and a monitor showing
// the wrong project is worse than one that fails to open.
func TestMonitorCommandNamesTheProject(t *testing.T) {
	got := monitorCommand("gomion")
	if len(got) < 4 || got[len(got)-1] != "gomion" {
		t.Fatalf("monitor command does not name its project: %v", got)
	}
	if joined(got[len(got)-3:]) != "project monitor gomion" {
		t.Errorf("monitor command is not `project monitor <name>`: %v", got)
	}
}

func indexOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}
