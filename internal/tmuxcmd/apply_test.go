package tmuxcmd

import (
	"strings"
	"testing"
)

// TestBuildApplySteps_StatusFormatRoutesThroughTmuxVerb pins the
// dispatcher routing: after E-1367 collapsed the per-binary tools into
// `endless-go <sub>`, the rendered status-format[1] must invoke
// `<binPath> tmux status-line ...` — not the bare `<binPath>
// status-line ...` shape that worked when the binary was endless-tmux.
//
// Regression: when E-1367 landed, this string still emitted "status-line"
// at the top level, so tmux printed
// `endless-go: unknown subcommand "status-line"` into the second status
// row instead of the live task line.
func TestBuildApplySteps_StatusFormatRoutesThroughTmuxVerb(t *testing.T) {
	steps := buildApplySteps("/usr/local/bin/endless-go", "e", 2)
	statusFmt, ok := findOptionValue(steps, "status-format[1]")
	if !ok {
		t.Fatalf("no status-format[1] set-option step in: %v", steps)
	}
	if !strings.Contains(statusFmt, "endless-go tmux status-line") {
		t.Errorf("status-format[1] = %q\n  want substring %q", statusFmt, "endless-go tmux status-line")
	}
	if strings.Contains(statusFmt, "endless-go status-line ") {
		t.Errorf("status-format[1] still uses the pre-E-1367 form (bare `status-line`): %q", statusFmt)
	}
}

// TestBuildApplySteps_MenuBindingsRouteThroughTmuxVerb pins the same
// rule for the hotkey and right-click bindings: both must shell out
// through `<binPath> tmux show-menu ...` so the dispatcher resolves the
// subcommand.
func TestBuildApplySteps_MenuBindingsRouteThroughTmuxVerb(t *testing.T) {
	steps := buildApplySteps("/usr/local/bin/endless-go", "e", 2)
	var bindings []string
	for _, step := range steps {
		if len(step) >= 1 && step[0] == "bind-key" {
			bindings = append(bindings, strings.Join(step, " "))
		}
	}
	if len(bindings) == 0 {
		t.Fatalf("no bind-key steps in: %v", steps)
	}
	for _, b := range bindings {
		if !strings.Contains(b, "show-menu") {
			continue
		}
		if !strings.Contains(b, "tmux show-menu") {
			t.Errorf("bind-key step missing `tmux` verb before show-menu: %q", b)
		}
	}
}

// findOptionValue returns the value passed to `set-option -g <name>` in
// the apply steps, or false if no such step exists.
func findOptionValue(steps [][]string, name string) (string, bool) {
	for _, step := range steps {
		if len(step) >= 4 && step[0] == "set-option" && step[1] == "-g" && step[2] == name {
			return step[3], true
		}
	}
	return "", false
}
