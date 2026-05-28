package tmuxcmd

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
//
// Menu bindings delegate to `endless-tmux show-menu` so the menu's
// title and items can include the live task ID (resolved at click
// time, not at apply time) and so changing the menu doesn't require
// re-running apply.
func buildApplySteps(binPath, hotkey string, statusInterval int) [][]string {
	statusFmt := fmt.Sprintf("#[align=centre]#(%s status-line --pane=#{pane_id})", binPath)

	// Hotkey binding: no mouse context needed; single-quoted is fine.
	hotkeyMenu := fmt.Sprintf("run-shell '%s show-menu --pane=#{pane_id} --position=center'", binPath)
	// Mouse binding: must capture mouse_x/mouse_y at binding time —
	// tmux substitutes #{mouse_x}/#{mouse_y} inline, then run-shell
	// invokes our binary with the resolved coordinates. We cannot rely
	// on `display-menu -x M -y M` later because the mouse-event context
	// is gone once we shell out. Outer arg is DOUBLE-quoted so tmux
	// processes #{...} substitutions; single quotes would pass the
	// literal "#{mouse_x}" text through.
	mouseMenu := fmt.Sprintf(
		`run-shell "%s show-menu --pane=#{pane_id} --position=mouse --mouse-x=#{mouse_x} --mouse-y=#{mouse_y}"`,
		binPath)

	return [][]string{
		// 1. Enable second status line.
		{"set-option", "-g", "status", "2"},
		// 2. Wire status-format[1] to the printer.
		{"set-option", "-g", "status-format[1]", statusFmt},
		// 3. Tighten refresh cadence.
		{"set-option", "-g", "status-interval", fmt.Sprintf("%d", statusInterval)},
		// 4. Hotkey-triggered popup menu — centered on the focused pane.
		{"bind-key", hotkey, hotkeyMenu},
		// 5a. Right-click popup menu on status-right region of row 0.
		// Note: tmux does not provide per-region mouse events scoped
		// to status-format[N>0], so the menu is anchored to row 0's
		// status-right. Tracked in E-1247.
		{"bind-key", "-n", "MouseDown3StatusRight", mouseMenu},
		// 5b. Alt+right-click variant, for consistency with the user's
		// existing M-MouseDown3Status* bindings.
		{"bind-key", "-n", "M-MouseDown3StatusRight", mouseMenu},
	}
}

// menuItem is one entry in a display-menu. A zero-value item produces
// a separator. Otherwise label, hotkey, and the command tmux runs on
// activation are passed verbatim to `tmux display-menu`. Used by
// show_menu.go to construct the menu argv.
type menuItem struct {
	Label  string
	Key    string
	Action string
}

func runTmux(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
