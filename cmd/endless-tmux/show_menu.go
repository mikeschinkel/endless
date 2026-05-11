package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"

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
		{}, // separator
		{"Refresh", "r", "refresh-client -S"},
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

// buildDisplayMenuArgs translates a position keyword into tmux's
// -x/-y flags and appends the items.
//
// For position="mouse", the caller MUST pass numeric mouseX / mouseY
// captured by the binding via #{mouse_x} / #{mouse_y} format
// substitutions. We can't use the `M` shorthand here because
// display-menu is invoked from a fresh tmux process inside run-shell,
// where the original mouse event context is gone — `M` would resolve
// to top-left. Numeric coordinates resolved at binding time work.
//
// If mouseX or mouseY are empty (e.g. position=mouse called without
// the binding capturing the coordinates), fall back to "S" so the
// menu at least appears adjacent to the status line rather than
// at top-left.
func buildDisplayMenuArgs(title, position, mouseX, mouseY string, items []menuItem) []string {
	x, y := "C", "C"
	if position == "mouse" {
		x = mouseX
		if x == "" {
			x = "S"
		}
		y = mouseY
		if y == "" {
			y = "S"
		}
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
