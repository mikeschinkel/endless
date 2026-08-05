package monitor

import (
	"reflect"
	"testing"
)

// TestPaneWindowHeightArgs pins the window-height query the monitor's self-fit
// reads its cap from (E-1851). #{window_height}, not #{pane_height}: the cap is
// a fraction of the whole window, so it must not shrink as the pane does.
func TestPaneWindowHeightArgs(t *testing.T) {
	got := paneWindowHeightArgs("%12")
	want := []string{"display-message", "-p", "-t", "%12", "#{window_height}"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestResizePaneHeightArgs pins the resize command the monitor issues against
// its own pane.
func TestResizePaneHeightArgs(t *testing.T) {
	got := resizePaneHeightArgs("%12", 9)
	want := []string{"resize-pane", "-t", "%12", "-y", "9"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("args = %q, want %q", got, want)
	}
}

// TestPaneWindowHeight_NoPane pins that an empty pane id (not running under
// tmux) reports "unknown" as 0 rather than erroring or shelling out.
func TestPaneWindowHeight_NoPane(t *testing.T) {
	if got := PaneWindowHeight(""); got != 0 {
		t.Fatalf("PaneWindowHeight(\"\") = %d, want 0", got)
	}
}

// TestResizePaneHeight_NoPane pins that resizing without a pane id fails rather
// than running `tmux resize-pane -t ""` against whatever pane is focused.
func TestResizePaneHeight_NoPane(t *testing.T) {
	if err := ResizePaneHeight("", 5); err == nil {
		t.Fatal("ResizePaneHeight(\"\", 5) = nil, want error")
	}
}
