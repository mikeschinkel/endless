package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// runShowMenu invokes `tmux display-menu` with a title and items
// resolved at click time — so the menu title includes the current
// task ID (e.g. "Endless [E-NNN]") and item actions reference that
// task without re-applying.
//
// Two positions are supported via --position:
//   - center: -x C -y C  (centered on the focused pane; default for
//     the prefix+e hotkey binding)
//   - mouse:  -x M -y M  (anchored at the mouse click; default for
//     the right-click bindings)
//
// Invoked from bindings via `run-shell '<binPath> show-menu
// --pane=#{pane_id} --position=center'`.
func runShowMenu(args []string) {
	fs := flag.NewFlagSet("show-menu", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	paneArg := fs.String("pane", "", "Tmux pane ID (overrides TMUX_PANE env)")
	position := fs.String("position", "center", "Menu position: center | mouse")
	mouseX := fs.String("mouse-x", "", "Numeric x coordinate (mouse position; required when position=mouse)")
	mouseY := fs.String("mouse-y", "", "Numeric y coordinate (mouse position; required when position=mouse)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	pane := *paneArg
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}

	info, err := monitor.GetActiveTaskForPane(pane)
	if err != nil && !errors.Is(err, monitor.ErrNoActiveTask) {
		fmt.Fprintf(os.Stderr, "endless-tmux show-menu: %v\n", err)
		os.Exit(1)
	}

	binPath, _ := os.Executable()
	if binPath == "" {
		binPath = "endless-tmux"
	}

	title := buildMenuTitle(info)
	items := buildMenuItems(binPath, info)
	tmuxArgs := buildDisplayMenuArgs(title, *position, *mouseX, *mouseY, items)

	cmd := exec.Command("tmux", tmuxArgs...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "endless-tmux show-menu: tmux display-menu failed: %v\n", err)
		os.Exit(1)
	}
}

// buildMenuTitle returns the centered title with the resolved task ID
// embedded. When no active task exists, the title is just "Endless"
// (the menu still opens so the user can refresh, etc.).
func buildMenuTitle(info *monitor.ActiveTaskInfo) string {
	if info == nil {
		return "#[align=centre]Endless"
	}
	return fmt.Sprintf("#[align=centre]Endless [E-%d]", info.TaskID)
}

// buildMenuItems returns the menu items for the current task. When no
// task is active, the task-dependent items are dimmed (prefixed with
// "-" per tmux's display-menu convention).
func buildMenuItems(binPath string, info *monitor.ActiveTaskInfo) []menuItem {
	// active-id call is preserved so item commands resolve at click
	// time too — that way, if the user claimed a different task
	// between opening the menu and selecting an item, the item still
	// targets the latest active task.
	taskRef := fmt.Sprintf("$(%s active-id --pane=#{pane_id})", binPath)

	items := []menuItem{
		{"Task Details", "d", fmt.Sprintf(
			`run-shell "tmux display-popup -E 'endless task show %s | less'"`, taskRef)},
		{"Mark verify", "v", fmt.Sprintf(
			`run-shell "endless task update %s --status verify"`, taskRef)},
		{"Task tree", "t",
			`run-shell "tmux display-popup -E 'endless task list --tree | less'"`},
		{"Session Activity", "a",
			`run-shell "tmux display-popup -w 80% -h 80% -E 'endless session activity --pane=#{pane_id} | less'"`},
		{}, // separator
		{"Refresh", "r", "refresh-client -S"},
		rowToggleItem(),
	}

	// Dim task-dependent items when there is no active task.
	if info == nil {
		for i := range items {
			switch items[i].Label {
			case "Task Details", "Mark verify":
				items[i].Label = "-" + items[i].Label
			}
		}
	}
	return items
}

// rowToggleItem returns a menu entry that flips the Endless status row
// on/off, with the label adapting to the current state. When the second
// status row is visible ("status 2"), the item reads "Hide Endless row"
// and the action sets status to 1; when hidden, the item reads "Show
// Endless row" and sets status back to 2. A refresh-client -S follows
// the toggle so the change is visible immediately.
//
// Tmux's `status` option is server-scoped, so this affects every client
// attached to the server — there is no per-window granularity. That's
// noted in the task plan; the auto-collapse-when-empty case is E-1259.
func rowToggleItem() menuItem {
	if statusRowVisible() {
		return menuItem{
			Label: "Hide Endless row", Key: "H",
			Action: "set-option -g status 1 ; refresh-client -S",
		}
	}
	return menuItem{
		Label: "Show Endless row", Key: "H",
		Action: "set-option -g status 2 ; refresh-client -S",
	}
}

// statusRowVisible returns true when tmux's `status` option is "2" or
// higher (i.e., the second status row — Endless's row — is being drawn).
// Best-effort: returns true on any tmux error, since the apply path
// always sets status to 2.
func statusRowVisible() bool {
	out, err := exec.Command("tmux", "show-options", "-gv", "status").Output()
	if err != nil {
		return true
	}
	val := strings.TrimSpace(string(out))
	// `status N` for any N >= 2 means row 1 (the Endless row) is shown.
	// `status on` (boolean form) is the historical equivalent of 1 — no
	// row 1. `status off` means no status at all.
	return val != "1" && val != "on" && val != "off"
}

// buildDisplayMenuArgs translates a position keyword into tmux's
// -x/-y flags and appends the items.
//
// For position="mouse":
//
//   - X uses the captured numeric mouseX. display-menu is invoked from
//     a fresh tmux process inside run-shell where the original mouse-
//     event context is gone — the `M` shorthand resolves to top-left.
//     Numeric coordinates resolved at binding time work.
//
//   - Y always uses `S` (the line adjacent to the status bar),
//     regardless of click row. Using the numeric mouse_y backfires:
//     tmux treats `-y N` as "place the menu top at row N"; when the
//     click is on the status bar near the screen bottom, the menu
//     can't extend downward, and tmux auto-flips it to the top of the
//     screen rather than shifting upward. `-y S` keeps it adjacent to
//     the status line where the click actually was. mouseY remains on
//     the function signature for potential future use.
//
// If mouseX is empty, fall back to `S` so the menu at least appears
// adjacent to the status line rather than at top-left.
func buildDisplayMenuArgs(title, position, mouseX, mouseY string, items []menuItem) []string {
	_ = mouseY // see comment above
	x, y := "C", "C"
	if position == "mouse" {
		x = mouseX
		if x == "" {
			x = "S"
		}
		y = "S"
	}
	args := []string{"display-menu", "-T", title, "-x", x, "-y", y}
	for _, it := range items {
		if it.Label == "" {
			args = append(args, "")
			continue
		}
		args = append(args, it.Label, it.Key, it.Action)
	}
	return args
}
