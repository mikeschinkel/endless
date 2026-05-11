package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// runApply issues a batch of tmux commands against the running server
// to wire up the Endless second status line plus its hotkey and
// right-click popup menus. Idempotent — re-running overwrites the same
// options/bindings without duplication. No file I/O.
//
// Reverses when the tmux server exits.
func runApply(args []string) {
	fs := flag.NewFlagSet("apply", flag.ExitOnError)
	binary := fs.String("binary", "", "Override path to endless-tmux binary (default: argv[0])")
	prefixKey := fs.String("hotkey", "e", "Prefix-table key to bind for the popup menu")
	interval := fs.Int("status-interval", 2, "tmux status-interval (seconds) for status-line refresh")
	fs.Parse(args)

	if os.Getenv("TMUX") == "" {
		fmt.Fprintln(os.Stderr, "endless-tmux apply: not inside a tmux session ($TMUX is empty)")
		os.Exit(1)
	}

	binPath := *binary
	if binPath == "" {
		// argv[0] is what tmux will need to re-invoke us for status-line.
		// Prefer the absolute path so the tmux config doesn't depend on
		// $PATH resolution at refresh time.
		exe, err := os.Executable()
		if err == nil {
			binPath = exe
		} else {
			binPath = "endless-tmux"
		}
	}

	steps := buildApplySteps(binPath, *prefixKey, *interval)
	for _, step := range steps {
		if err := runTmux(step...); err != nil {
			fmt.Fprintf(os.Stderr, "endless-tmux apply: %v\n  args: %s\n", err, strings.Join(step, " "))
			os.Exit(1)
		}
	}

	// Force an immediate redraw so the user sees the change without
	// waiting up to status-interval seconds.
	_ = runTmux("refresh-client", "-S")

	fmt.Println("endless-tmux: status line + menus applied to running tmux server")
}

// buildApplySteps returns the ordered list of tmux argv slices to apply.
// Each entry is the argv to pass to the `tmux` binary.
//
// status-format[1] passes the rendered pane's ID via tmux's #{pane_id}
// format substitution because tmux does NOT propagate TMUX_PANE to
// commands invoked via #() — relying on the env var alone causes the
// binary to see an empty pane and render the placeholder dot.
func buildApplySteps(binPath, hotkey string, statusInterval int) [][]string {
	statusFmt := fmt.Sprintf("#[align=centre]#(%s status-line --pane=#{pane_id})", binPath)

	// Menu items are shared between the hotkey-triggered menu and the
	// right-click menu. The bracketed letters mirror Mike's idiom in
	// ~/.init/tmux/conf.d/70-mouse-menus.conf.
	//
	// Active task ID is resolved at click time via the local Go binary
	// (binPath active-id) so the menu always reflects the current task,
	// not whatever was active when `apply` ran.
	menuTitle := "#[align=centre]Endless"
	menuItems := []menuItem{
		{"Detail", "d", fmt.Sprintf(
			`run-shell "endless task show $(%s active-id) | tmux display-popup -E -"`, binPath)},
		{"Mark verify", "v", fmt.Sprintf(
			`run-shell "endless task update $(%s active-id) --status verify"`, binPath)},
		{"Task tree", "t",
			`run-shell "endless task list --tree | tmux display-popup -E -"`},
		{}, // separator
		{"Refresh", "r", "refresh-client -S"},
	}
	displayMenu := buildDisplayMenu(menuTitle, menuItems)

	return [][]string{
		// 1. Enable second status line.
		{"set-option", "-g", "status", "2"},
		// 2. Wire status-format[1] to the printer.
		{"set-option", "-g", "status-format[1]", statusFmt},
		// 3. Tighten refresh cadence.
		{"set-option", "-g", "status-interval", fmt.Sprintf("%d", statusInterval)},
		// 4. Hotkey-triggered popup menu (prefix + hotkey).
		append([]string{"bind-key", hotkey}, displayMenu...),
		// 5a. Right-click popup menu on status-right region.
		// Note: tmux does not provide per-region mouse events scoped
		// to status-format[N>0], so the menu is anchored to row 0's
		// status-right. Documented as a known limitation.
		append([]string{"bind-key", "-n", "MouseDown3StatusRight"}, displayMenu...),
		// 5b. Alt+right-click variant, for consistency with the user's
		// existing M-MouseDown3Status* bindings.
		append([]string{"bind-key", "-n", "M-MouseDown3StatusRight"}, displayMenu...),
	}
}

// menuItem is one entry in a display-menu. A zero-value item produces
// a separator. Otherwise label, hotkey, and the command tmux runs on
// activation are passed verbatim to `tmux display-menu`.
type menuItem struct {
	Label  string
	Key    string
	Action string
}

// buildDisplayMenu constructs the argv suffix for `tmux bind-key <key>
// display-menu ...`. Zero-value items produce separators.
func buildDisplayMenu(title string, items []menuItem) []string {
	args := []string{"display-menu", "-T", title, "-x", "M", "-y", "S"}
	for _, it := range items {
		if it.Label == "" {
			args = append(args, "")
			continue
		}
		args = append(args, it.Label, it.Key, it.Action)
	}
	return args
}

func runTmux(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
