package spawnlaunchcmd

import (
	"path/filepath"
	"reflect"
	"testing"
)

// TestNewWindowArgs_WithCwd pins the normal new-window command: -c <cwd>,
// -n <name>, then `--` and the window command passed through literally.
func TestNewWindowArgs_WithCwd(t *testing.T) {
	got := newWindowArgs("/wt/e-1705", "endless_deliver[E-1705]",
		[]string{"/bin/endless-go", "spawn-launch", "--spec", "/tmp/spec.json"})
	want := []string{
		"new-window", "-c", "/wt/e-1705", "-n", "endless_deliver[E-1705]", "--",
		"/bin/endless-go", "spawn-launch", "--spec", "/tmp/spec.json",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestNewWindowArgs_NoCwdOmitsFlag pins that an empty cwd drops -c so the window
// inherits the caller's directory (used by the attach path).
func TestNewWindowArgs_NoCwdOmitsFlag(t *testing.T) {
	got := newWindowArgs("", "win", []string{"/bin/claude", "attach", "abcd1234"})
	want := []string{"new-window", "-n", "win", "--", "/bin/claude", "attach", "abcd1234"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
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

// TestPanePaneIDArgs pins the window→active-pane-id lookup that anchors the
// layout on a stable pane ID rather than a base-index-dependent pane index.
func TestPanePaneIDArgs(t *testing.T) {
	got := panePaneIDArgs("endless_deliver[E-1851]")
	want := []string{
		"display-message", "-p", "-t", "endless_deliver[E-1851]", "#{pane_id}",
	}
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
