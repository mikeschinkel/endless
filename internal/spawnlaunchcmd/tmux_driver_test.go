package spawnlaunchcmd

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestNewWindowArgs_WithCwd pins the normal new-window command: -t <session>,
// -c <cwd>, -n <name>, the -P -F pane-id readback, then `--` and the window
// command passed through literally.
func TestNewWindowArgs_WithCwd(t *testing.T) {
	got := newWindowArgs("$3:", "/wt/e-1705", "endless_deliver[E-1705]",
		[]string{"/bin/endless-go", "spawn-launch", "--spec", "/tmp/spec.json"})
	want := []string{
		"new-window", "-t", "$3:", "-c", "/wt/e-1705",
		"-n", "endless_deliver[E-1705]", "-P", "-F", "#{pane_id}", "--",
		"/bin/endless-go", "spawn-launch", "--spec", "/tmp/spec.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestNewWindowArgs_NoCwdOmitsFlag pins that an empty cwd drops -c so the window
// inherits the caller's directory. The target does NOT drop with it: cwd is
// optional, the landing session is not.
func TestNewWindowArgs_NoCwdOmitsFlag(t *testing.T) {
	got := newWindowArgs("$0:", "", "win", []string{"/bin/claude", "attach", "abcd1234"})
	want := []string{
		"new-window", "-t", "$0:", "-n", "win", "-P", "-F", "#{pane_id}", "--",
		"/bin/claude", "attach", "abcd1234",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestNewWindowArgs_AlwaysTargeted is E-2125's regression, stated as the
// property rather than as one argv: whatever else varies, `-t <target>` is
// present and immediately follows the verb. An untargeted new-window lands in
// the server's most recently active session, so a spawn asked for in one
// session appeared in whichever one the operator was looking at.
func TestNewWindowArgs_AlwaysTargeted(t *testing.T) {
	cases := []struct {
		name   string
		target string
		cwd    string
		win    string
		cmd    []string
	}{
		{"cwd and command", "$3:", "/wt/e-1", "E-1", []string{"claude"}},
		{"no cwd", "$12:", "", "E-2", []string{"claude"}},
		{"no command", "$0:", "/wt/e-3", "E-3", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newWindowArgs(tc.target, tc.cwd, tc.win, tc.cmd)
			if len(got) < 3 || got[0] != "new-window" || got[1] != "-t" || got[2] != tc.target {
				t.Fatalf("args = %q, want it to open with new-window -t %q", got, tc.target)
			}
		})
	}
}

// TestSessionIDArgs pins the pane -> session-id lookup that resolves the
// new-window target.
func TestSessionIDArgs(t *testing.T) {
	got := sessionIDArgs("%246")
	want := []string{"display-message", "-p", "-t", "%246", "#{session_id}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestSpawnerSession_RefusesWithoutPane pins that an unresolvable target is an
// error rather than a silent fall back to an untargeted new-window. Guessing is
// the defect; refusing is the fix.
func TestSpawnerSession_RefusesWithoutPane(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	got, err := spawnerSession()
	if err == nil {
		t.Fatalf("spawnerSession() = %q, nil; want an error when $TMUX_PANE is unset", got)
	}
	if got != "" {
		t.Fatalf("spawnerSession() = %q on error, want an empty target", got)
	}
	if !strings.Contains(err.Error(), "TMUX_PANE") {
		t.Fatalf("error = %v, want it to name $TMUX_PANE", err)
	}
}

// TestSplitWindowArgs_ShellPane pins the first split of the 3-pane layout
// (E-1851): a 50/50 left/right split off the claude pane, no -l (tmux's even
// split), no `--` (the pane runs the user's default shell), and -P -F so the
// caller reads back the new pane's id.
func TestSplitWindowArgs_ShellPane(t *testing.T) {
	got := splitWindowArgs("%7", true, false, "/wt/e-1851", 0, nil)
	want := []string{
		"split-window", "-h", "-t", "%7", "-c", "/wt/e-1851",
		"-P", "-F", "#{pane_id}",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestSplitWindowArgs_MonitorPane pins the second split: -b inserts the monitor
// ABOVE the shell pane (the order that can't race the monitor's self-shrink),
// and the command is passed through literally after `--`.
func TestSplitWindowArgs_MonitorPane(t *testing.T) {
	got := splitWindowArgs("%8", false, true, "/wt/e-1851", 0,
		[]string{"/usr/local/bin/endless", "session", "monitor"})
	want := []string{
		"split-window", "-v", "-b", "-t", "%8", "-c", "/wt/e-1851",
		"-P", "-F", "#{pane_id}", "--",
		"/usr/local/bin/endless", "session", "monitor",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestSplitWindowArgs_LengthAndNoCwd pins the two conditional flags from the
// other side: a positive length emits -l, and an empty cwd drops -c.
func TestSplitWindowArgs_LengthAndNoCwd(t *testing.T) {
	got := splitWindowArgs("%9", false, false, "", 12, []string{"top"})
	want := []string{
		"split-window", "-v", "-t", "%9", "-l", "12",
		"-P", "-F", "#{pane_id}", "--", "top",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestSelectPaneArgs pins the focus-return command.
func TestSelectPaneArgs(t *testing.T) {
	got := selectPaneArgs("%7")
	want := []string{"select-pane", "-t", "%7"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestMonitorCommand pins that the monitor pane runs `endless session monitor`,
// whether or not the CLI resolves on PATH (the binary element varies; the verb
// pair does not).
func TestMonitorCommand(t *testing.T) {
	got := monitorCommand()
	if len(got) != 3 {
		t.Fatalf("monitorCommand() = %q, want 3 elements", got)
	}
	if got[1] != "session" || got[2] != "monitor" {
		t.Fatalf("monitorCommand() verbs = %q, want [session monitor]", got[1:])
	}
	if got[0] != "endless" && filepath.Base(got[0]) != "endless" {
		t.Fatalf("monitorCommand() binary = %q, want endless (bare or resolved)", got[0])
	}
}

// TestWindowOptionCommands pins the exact @endless_* set-option command list the
// launcher runs before exec — this is the ordering that removes the old
// send-keys/sleep SessionStart race.
func TestWindowOptionCommands(t *testing.T) {
	spec := LaunchSpec{SpawnedBy: "sess-abc", TaskID: "1705", ProjectID: "3"}
	got := windowOptionCommands("%42", spec)
	want := [][]string{
		{"set-option", "-w", "-t", "%42", "@endless_spawned_by", "sess-abc"},
		{"set-option", "-w", "-t", "%42", "@endless_task_id", "1705"},
		{"set-option", "-w", "-t", "%42", "@endless_project_id", "3"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %q, want %q", got, want)
	}
}
