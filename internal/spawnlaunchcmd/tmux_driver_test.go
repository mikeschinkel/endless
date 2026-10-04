package spawnlaunchcmd

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestNewWindowArgs_WithCwd pins the normal new-window command: the position
// flag and -t <window>, -c <cwd>, -n <name>, the -P -F pane-id readback, then `--` and the window
// command passed through literally.
func TestNewWindowArgs_WithCwd(t *testing.T) {
	got := newWindowArgs(placementFor(PlaceFirst, "$3", ""), "/wt/e-1705", "endless_deliver[E-1705]", false,
		[]string{"/bin/endless-go", "spawn-launch", "--spec", "/tmp/spec.json"})
	want := []string{
		"new-window", "-b", "-t", "$3:{start}", "-c", "/wt/e-1705",
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
	got := newWindowArgs(placementFor(PlaceLast, "$0", ""), "", "win", false, []string{"/bin/claude", "attach", "abcd1234"})
	want := []string{
		"new-window", "-a", "-t", "$0:{end}", "-n", "win", "-P", "-F", "#{pane_id}", "--",
		"/bin/claude", "attach", "abcd1234",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestNewWindowArgs_AlwaysTargeted is E-2125's regression, stated as the
// property rather than as one argv: whatever else varies, `-t <target>` is
// present and immediately follows the verb's position flag (E-2234). An untargeted new-window lands in
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
		{"cwd and command", "$3:{start}", "/wt/e-1", "E-1", []string{"claude"}},
		{"no cwd", "$12:{end}", "", "E-2", []string{"claude"}},
		{"no command", "@4", "/wt/e-3", "E-3", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := newWindowArgs(windowPlacement{"-b", tc.target}, tc.cwd, tc.win, false, tc.cmd)
			if len(got) < 4 || got[0] != "new-window" || got[1] != "-b" || got[2] != "-t" || got[3] != tc.target {
				t.Fatalf("args = %q, want it to open with new-window -b -t %q", got, tc.target)
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
	got := MonitorCommand()
	if len(got) != 3 {
		t.Fatalf("MonitorCommand() = %q, want 3 elements", got)
	}
	if got[1] != "session" || got[2] != "monitor" {
		t.Fatalf("MonitorCommand() verbs = %q, want [session monitor]", got[1:])
	}
	if got[0] != "endless" && filepath.Base(got[0]) != "endless" {
		t.Fatalf("MonitorCommand() binary = %q, want endless (bare or resolved)", got[0])
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

// TestNewWindowArgs_DetachedOnlyWhenAsked pins E-1814's focus rule: a window
// opened detached (auto-spawn, or `--no-refocus` since E-2234) carries -d so it
// never takes the user's focus, and a plain manual spawn does not, because the
// person who asked is looking for it.
func TestNewWindowArgs_DetachedOnlyWhenAsked(t *testing.T) {
	cmd := []string{"/bin/endless-go", "spawn-launch", "--spec", "/tmp/s.json"}
	place := placementFor(PlaceFirst, "$3", "")

	auto := newWindowArgs(place, "/wt/e-9", "E-9", true, cmd)
	want := []string{
		"new-window", "-d", "-b", "-t", "$3:{start}", "-c", "/wt/e-9",
		"-n", "E-9", "-P", "-F", "#{pane_id}", "--",
		"/bin/endless-go", "spawn-launch", "--spec", "/tmp/s.json",
	}
	if !reflect.DeepEqual(auto, want) {
		t.Fatalf("auto args = %q, want %q", auto, want)
	}

	manual := newWindowArgs(place, "/wt/e-9", "E-9", false, cmd)
	for _, a := range manual {
		if a == "-d" {
			t.Fatalf("manual spawn carries -d: %q", manual)
		}
	}
}

// TestResolveTarget_ExplicitSessionWins: --target-session names the session
// outright, needs no $TMUX_PANE, and is rendered as a window in that session.
func TestResolveTarget_ExplicitSessionWins(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	want := windowPlacement{"-b", "$7:{start}"}
	for _, in := range []string{"$7", "$7:"} {
		got, err := resolveTarget(in, "", PlaceFirst)
		if err != nil {
			t.Fatalf("resolveTarget(%q): %v", in, err)
		}
		if got != want {
			t.Errorf("resolveTarget(%q) = %v, want %v", in, got, want)
		}
	}
	// With no explicit session it is still the spawner's, which refuses
	// without a pane rather than guessing (E-2125).
	if _, err := resolveTarget("", "", PlaceFirst); err == nil {
		t.Error("resolveTarget(\"\") with no $TMUX_PANE: want an error")
	}
}

// TestWindowOptionCommands_AutoSpawned: the auto-spawn marker is appended only
// for an auto-spawned window, after the three every spawn sets.
func TestWindowOptionCommands_AutoSpawned(t *testing.T) {
	spec := LaunchSpec{SpawnedBy: "pid-1", TaskID: "9", ProjectID: "3", AutoSpawned: true}
	got := windowOptionCommands("%42", spec)
	if len(got) != 4 {
		t.Fatalf("commands = %q, want 4", got)
	}
	want := []string{"set-option", "-w", "-t", "%42", AutoSpawnedOption, "1"}
	if !reflect.DeepEqual(got[3], want) {
		t.Fatalf("last command = %q, want %q", got[3], want)
	}
	if AutoSpawnedOption != "@endless_auto_spawned" {
		t.Errorf("AutoSpawnedOption = %q", AutoSpawnedOption)
	}
}
