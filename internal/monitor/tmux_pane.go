package monitor

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// tmux_pane.go holds the pane-GEOMETRY side of the tmux integration — reading a
// pane's window height and resizing a pane — as opposed to tmux_lookup.go's
// pane→session/task RESOLUTION. It exists so `session monitor` can size its own
// pane to the frame it renders (E-1851) without growing its own tmux plumbing:
// every tmux argv in this package is built by a pure function here, so the
// future multiplexer-driver seam (E-1085) has one place to hoist.

// paneWindowHeightArgs builds `tmux display-message -p -t <pane> #{window_height}`.
func paneWindowHeightArgs(pane string) []string {
	return []string{"display-message", "-p", "-t", pane, "#{window_height}"}
}

// paneWindowPanesArgs builds `tmux display-message -p -t <pane> #{window_panes}`.
func paneWindowPanesArgs(pane string) []string {
	return []string{"display-message", "-p", "-t", pane, "#{window_panes}"}
}

// resizePaneHeightArgs builds `tmux resize-pane -t <pane> -y <rows>`.
func resizePaneHeightArgs(pane string, rows int) []string {
	return []string{"resize-pane", "-t", pane, "-y", strconv.Itoa(rows)}
}

// PaneWindowHeight returns the height in rows of the tmux WINDOW containing
// pane, or 0 when it can't be determined (empty pane id, not in tmux, tmux
// errors, unparsable output). Callers treat 0 as "no cap known" rather than an
// error — a missing height must never be fatal to a read command.
func PaneWindowHeight(pane string) int {
	if pane == "" {
		return 0
	}
	out, err := exec.Command("tmux", paneWindowHeightArgs(pane)...).Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ResizePaneHeight sets pane's height to rows. Errors are returned, not printed:
// resizing is always best-effort decoration (a single-pane window legitimately
// refuses, and a monitor that spewed tmux stderr every repaint would be worse
// than one that stays the wrong size).
func ResizePaneHeight(pane string, rows int) error {
	if pane == "" {
		return fmt.Errorf("resize pane: no pane id")
	}
	args := resizePaneHeightArgs(pane, rows)
	if err := exec.Command("tmux", args...).Run(); err != nil {
		return fmt.Errorf("tmux %v: %w", args, err)
	}
	return nil
}

// PaneWindowPanes returns how many panes share the tmux WINDOW containing pane,
// or 0 when it can't be determined (empty pane id, not in tmux, tmux errors,
// unparsable output). Callers treat 0 as "unknown" rather than an error, on the
// same rule as PaneWindowHeight.
//
// A live view uses this to know whether it is sharing its window. A view that
// reserves part of the window for a companion pane is reserving it for nothing
// when it is the only pane there (E-1976).
func PaneWindowPanes(pane string) int {
	if pane == "" {
		return 0
	}
	out, err := exec.Command("tmux", paneWindowPanesArgs(pane)...).Output()
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}
