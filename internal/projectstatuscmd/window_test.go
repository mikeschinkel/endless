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

// TestNewSessionArgsIsDetached pins the flag the whole design rests on. Creating
// the board must never yank the user out of what they are doing; attaching or
// switching is a separate, explicit step.
func TestNewSessionArgsIsDetached(t *testing.T) {
	got := newSessionArgs(windowLayout{
		Session:    "demo-monitor",
		Dir:        "/tmp/demo",
		MonitorCmd: []string{"endless", "project", "monitor", "demo"},
	})
	if !strings.Contains(joined(got), "new-session -d -s demo-monitor") {
		t.Errorf("new-session is not detached or misnames the session: %v", got)
	}
	if !strings.Contains(joined(got), "-c /tmp/demo") {
		t.Errorf("new-session does not start in the project dir: %v", got)
	}
	// `--` terminates tmux flag parsing so the window command reaches execvp
	// literally, with no shell re-quoting of its arguments.
	if i := indexOf(got, "--"); i < 0 || joined(got[i+1:]) != "endless project monitor demo" {
		t.Errorf("the monitor command is not passed literally after --: %v", got)
	}
}

func TestNewSessionArgsOmitsAnEmptyDir(t *testing.T) {
	got := newSessionArgs(windowLayout{Session: "s", MonitorCmd: []string{"x"}})
	if strings.Contains(joined(got), "-c ") {
		t.Errorf("an empty dir still produced a -c flag: %v", got)
	}
}

// TestSplitShellArgsRunsTheDefaultShell: no `--` and no command, so tmux runs the
// user's own $SHELL. Naming a shell here would override whatever they chose.
func TestSplitShellArgsRunsTheDefaultShell(t *testing.T) {
	got := splitShellArgs(windowLayout{Session: "demo-monitor", Dir: "/tmp/demo", ShellHeight: 12})
	if indexOf(got, "--") >= 0 {
		t.Errorf("the shell pane names a command instead of running the user's shell: %v", got)
	}
	if !strings.Contains(joined(got), "split-window -v -t demo-monitor") {
		t.Errorf("the shell pane is not a vertical split of the session: %v", got)
	}
	if !strings.Contains(joined(got), "-l 12") {
		t.Errorf("the shell pane did not take its starting height: %v", got)
	}
	if !strings.Contains(joined(got), "-P -F #{pane_id}") {
		t.Errorf("the split does not report the created pane's id: %v", got)
	}
}

// TestSessionTargetsAreExact pins the `=` prefix. Without it tmux matches a
// session name as a PREFIX, so `endless-monitor` would also match
// `endless-monitor-2` — and the launcher would attach to, or declare existing,
// somebody else's session.
func TestSessionTargetsAreExact(t *testing.T) {
	for name, args := range map[string][]string{
		"has-session":    hasSessionArgs("demo-monitor"),
		"switch-client":  switchClientArgs("demo-monitor"),
		"attach-session": attachArgs("demo-monitor"),
	} {
		if !strings.Contains(joined(args), "=demo-monitor") {
			t.Errorf("%s targets the session by prefix, not exactly: %v", name, args)
		}
	}
}

// TestMonitorCommandNamesTheProject: the pane must not rely on cwd resolution.
// Its directory is the main checkout today, but a launcher that depends on that
// coincidence breaks the moment the layout's home moves — and a board showing
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
