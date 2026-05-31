package tmuxcmd

import (
	"strings"
	"testing"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// TestBuildMenuTitle_IncludesTaskIDOrFallsBack pins the two title
// shapes: with an active task, "[E-N]" is embedded; without one, the
// title still opens but reads only "Endless" so the menu remains usable
// for refresh/show-row.
func TestBuildMenuTitle_IncludesTaskIDOrFallsBack(t *testing.T) {
	if got := buildMenuTitle(nil); !strings.Contains(got, "Endless") || strings.Contains(got, "E-") {
		t.Errorf("nil info title = %q, want plain Endless with no task ref", got)
	}
	info := &monitor.ActiveTaskInfo{TaskID: 42}
	if got := buildMenuTitle(info); !strings.Contains(got, "[E-42]") {
		t.Errorf("active title = %q, missing [E-42]", got)
	}
}

// TestBuildMenuItems_NoActiveTaskDimsTaskItems pins the dim-when-no-task
// rule: items whose actions depend on a current task ("Task Details",
// "Mark verify") are prefixed with "-" to gray them out, while
// task-independent items (Refresh, row toggle) are left unchanged.
func TestBuildMenuItems_NoActiveTaskDimsTaskItems(t *testing.T) {
	got := buildMenuItems("/usr/local/bin/endless-go", nil)
	wantDimmed := map[string]bool{"-Task Details": false, "-Mark verify": false}
	for _, it := range got {
		if _, ok := wantDimmed[it.Label]; ok {
			wantDimmed[it.Label] = true
		}
		// Refresh should never be dimmed.
		if it.Label == "-Refresh" {
			t.Errorf("Refresh dimmed when no task; should always be active")
		}
	}
	for label, seen := range wantDimmed {
		if !seen {
			t.Errorf("expected dimmed item %q in items, not found", label)
		}
	}
}

// TestBuildMenuItems_ActiveTaskUndimmed pins the active-task case:
// task-dependent items keep their original labels (no leading "-") and
// the action strings embed the active-id resolution call.
func TestBuildMenuItems_ActiveTaskUndimmed(t *testing.T) {
	info := &monitor.ActiveTaskInfo{TaskID: 99}
	got := buildMenuItems("/usr/local/bin/endless-go", info)
	seenDetails := false
	for _, it := range got {
		if it.Label == "Task Details" {
			seenDetails = true
			if !strings.Contains(it.Action, "active-id") {
				t.Errorf("Task Details action %q missing active-id resolution", it.Action)
			}
		}
		if strings.HasPrefix(it.Label, "-") {
			t.Errorf("item %q dimmed even with active task", it.Label)
		}
	}
	if !seenDetails {
		t.Error("Task Details item missing from menu")
	}
}

// TestBuildDisplayMenuArgs_PositionCenter pins the default placement:
// position=center uses -x C -y C regardless of mouse coordinates.
func TestBuildDisplayMenuArgs_PositionCenter(t *testing.T) {
	args := buildDisplayMenuArgs("title", "center", "20", "30", nil)
	if !argsContainsPair(args, "-x", "C") {
		t.Errorf("center position missing -x C: %v", args)
	}
	if !argsContainsPair(args, "-y", "C") {
		t.Errorf("center position missing -y C: %v", args)
	}
}

// TestBuildDisplayMenuArgs_PositionMouseUsesXAndStatusLine pins the
// mouse-positioning rule from the comment block: x uses the captured
// numeric, y is always "S" (status-line adjacent) even when mouseY is
// supplied.
func TestBuildDisplayMenuArgs_PositionMouseUsesXAndStatusLine(t *testing.T) {
	args := buildDisplayMenuArgs("title", "mouse", "37", "9", nil)
	if !argsContainsPair(args, "-x", "37") {
		t.Errorf("mouse position missing -x 37: %v", args)
	}
	if !argsContainsPair(args, "-y", "S") {
		t.Errorf("mouse position missing -y S (always status-line adjacent): %v", args)
	}
}

// TestBuildDisplayMenuArgs_PositionMouseFallsBackOnEmptyX pins the
// fallback to "S" when mouseX is empty (rather than top-left).
func TestBuildDisplayMenuArgs_PositionMouseFallsBackOnEmptyX(t *testing.T) {
	args := buildDisplayMenuArgs("title", "mouse", "", "", nil)
	if !argsContainsPair(args, "-x", "S") {
		t.Errorf("mouse position with empty x should fall back to S: %v", args)
	}
}

// TestBuildDisplayMenuArgs_AppendsItems pins the item-rendering shape:
// each non-separator item contributes three argv entries (label, key,
// action) in order; separators contribute one empty string.
func TestBuildDisplayMenuArgs_AppendsItems(t *testing.T) {
	items := []menuItem{
		{Label: "A", Key: "a", Action: "act-a"},
		{}, // separator
		{Label: "B", Key: "b", Action: "act-b"},
	}
	args := buildDisplayMenuArgs("t", "center", "", "", items)
	// Look for "A", "a", "act-a" appearing in order.
	idxA := indexOf(args, "A")
	idxAKey := indexOf(args, "a")
	idxAAct := indexOf(args, "act-a")
	if idxA < 0 || idxAKey != idxA+1 || idxAAct != idxA+2 {
		t.Errorf("A triple not contiguous in args: %v", args)
	}
}

func argsContainsPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}
