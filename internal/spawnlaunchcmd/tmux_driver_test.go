package spawnlaunchcmd

import (
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
