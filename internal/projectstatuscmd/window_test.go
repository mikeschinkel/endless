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
	Session:    "demo",
	Dir:        "/tmp/demo",
	MonitorCmd: []string{"endless", "project", "monitor", "demo"},
}

// TestNewSessionArgsIsADetachedShell pins two things at once, and the second is
// the one that bites.
//
// Detached (-d): creating the monitor must never yank the user out of what they
// are doing.
//
// And NO COMMAND: the session's first pane is the user's shell, because the
// SHELL is created first and the monitor inserted above it. Creating the monitor
// first and splitting a shell off it races the monitor's own self-resize — E-1851
// learned that in the spawn layout, and this launcher shipped with it backwards
// (E-1976). The symptom is a window with no second pane at all.
func TestNewSessionArgsIsADetachedShell(t *testing.T) {
	got := newSessionArgs(demoLayout)
	if !strings.Contains(joined(got), "new-session -d -s demo") {
		t.Errorf("new-session is not detached or misnames the session: %v", got)
	}
	if !strings.Contains(joined(got), "-c /tmp/demo") {
		t.Errorf("new-session does not start in the project dir: %v", got)
	}
	if indexOf(got, "--") >= 0 {
		t.Errorf("new-session names a command; the first pane must be the user's shell: %v", got)
	}
	if !strings.Contains(joined(got), "-P -F #{pane_id}") {
		t.Errorf("new-session does not report the shell's pane id: %v", got)
	}
}

func TestNewSessionArgsOmitsAnEmptyDir(t *testing.T) {
	got := newSessionArgs(windowLayout{Session: "s"})
	if strings.Contains(joined(got), "-c ") {
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

// TestEveryWindowIsNamedProjects: both ways a launch gets a window name it
// `projects`, and -n is what keeps tmux from renaming it to the shell.
func TestEveryWindowIsNamedProjects(t *testing.T) {
	for name, args := range map[string][]string{
		"new-session": newSessionArgs(demoLayout),
		"new-window":  newWindowArgs(demoLayout),
	} {
		if i := indexOf(args, "-n"); i < 0 || args[i+1] != "projects" {
			t.Errorf("%s does not name its window projects: %v", name, args)
		}
		if indexOf(args, "-P") < 0 {
			t.Errorf("%s does not report the shell pane's id: %v", name, args)
		}
	}
}

// TestNewWindowTargetsTheSessionExactly: `=demo:` is the session itself, matched
// exactly, so `demo` never resolves to `demo-legacy`.
func TestNewWindowTargetsTheSessionExactly(t *testing.T) {
	if !strings.Contains(joined(newWindowArgs(demoLayout)), "-t =demo:") {
		t.Errorf("new-window does not target the session exactly: %v", newWindowArgs(demoLayout))
	}
}

// TestLauncherUsesItsOwnServer: every command the launcher runs goes to the
// monitor's server, never the user's default one.
func TestLauncherUsesItsOwnServer(t *testing.T) {
	got := tmuxArgs([]string{"has-session", "-t", "=demo"})
	if joined(got[:2]) != "-L endless" {
		t.Errorf("launcher command is not on the endless server: %v", got)
	}
	if attachHint("demo") != "tmux -L endless attach -t demo" {
		t.Errorf("attach hint = %q", attachHint("demo"))
	}
}
// TestSessionTargetsAreExact pins the `=` prefix. Without it tmux matches a
// session name as a PREFIX, so a project whose name is a prefix of another's
// (`h2pp` inside `h2pp-legacy`) resolves to the wrong monitor, and the launcher
// attaches to — or declares already existing — a session that is not its own.
func TestSessionTargetsAreExact(t *testing.T) {
	for name, args := range map[string][]string{
		"has-session":    hasSessionArgs("e-demo-monitor"),
		"attach-session": attachArgs("e-demo-monitor"),
	} {
		if !strings.Contains(joined(args), "=e-demo-monitor") {
			t.Errorf("%s targets the session by prefix, not exactly: %v", name, args)
		}
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
