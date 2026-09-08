package spawnlaunchcmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// tmux_driver.go isolates every tmux invocation behind pure argv builders plus
// one thin exec wrapper, so the future multiplexer-driver refactor (Herdr / a
// built-in multiplexer, relates E-1085) can add backends without touching the
// spec/argv/exec logic in spawn_window.go and spawn_launch.go.

// newWindowArgs builds `tmux new-window -c <cwd> -n <name> -- <cmd...>`. The
// `--` terminates tmux flag parsing so the window command and its args are
// passed through literally (no shell re-quoting of cmd elements). When cwd is
// empty the `-c` flag is omitted and the window inherits the caller's cwd.
func newWindowArgs(cwd, windowName string, cmd []string) []string {
	args := []string{"new-window"}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, "-n", windowName, "--")
	return append(args, cmd...)
}

// setOptionArgs builds `tmux set-option -w -t <target> <key> <value>` for one
// window option.
func setOptionArgs(target, key, value string) []string {
	return []string{"set-option", "-w", "-t", target, key, value}
}

// splitWindowArgs builds
//
//	tmux split-window {-h|-v} [-b] -t <target> [-c <cwd>] [-l <length>] \
//	     -P -F #{pane_id} [-- <cmd...>]
//
// horizontal picks a left/right split (-h) over a top/bottom one (-v); before
// (-b) puts the NEW pane above/left of target instead of below/right. length is
// the new pane's size — columns for -h, rows for -v — and is omitted when <= 0,
// which leaves tmux's even 50/50 split. An empty cmd omits the `--` entirely so
// the pane runs tmux's default command (the user's interactive $SHELL); a
// non-empty cmd is passed through literally after `--`, no shell re-quoting.
//
// `-P -F #{pane_id}` is always present so the caller reads back the created
// pane's id. Ids, not indexes: pane indexes depend on the user's pane-base-index
// and shift as panes are added, so index targeting would silently address the
// wrong pane on a 1-based configuration.
func splitWindowArgs(target string, horizontal, before bool, cwd string, length int, cmd []string) []string {
	args := []string{"split-window"}
	if horizontal {
		args = append(args, "-h")
	} else {
		args = append(args, "-v")
	}
	if before {
		args = append(args, "-b")
	}
	args = append(args, "-t", target)
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	if length > 0 {
		args = append(args, "-l", strconv.Itoa(length))
	}
	args = append(args, "-P", "-F", "#{pane_id}")
	if len(cmd) > 0 {
		args = append(args, "--")
		args = append(args, cmd...)
	}
	return args
}

// selectPaneArgs builds `tmux select-pane -t <target>` — used to hand focus back
// to the claude pane once the layout is assembled.
func selectPaneArgs(target string) []string {
	return []string{"select-pane", "-t", target}
}

// panePaneIDArgs builds `tmux display-message -p -t <target> #{pane_id}`, which
// resolves a window target to its ACTIVE pane's id. Called once right after
// new-window, while the window's only pane is the claude pane.
func panePaneIDArgs(target string) []string {
	return []string{"display-message", "-p", "-t", target, "#{pane_id}"}
}

// windowOptionCommands returns the ordered set-option arg lists that publish the
// @endless_* window options SessionStart reads to bind the spawned session to
// its task. Kept pure so tests can assert the exact command list.
func windowOptionCommands(target string, spec LaunchSpec) [][]string {
	return [][]string{
		setOptionArgs(target, "@endless_spawned_by", spec.SpawnedBy),
		setOptionArgs(target, "@endless_task_id", spec.TaskID),
		setOptionArgs(target, "@endless_project_id", spec.ProjectID),
	}
}

// tmuxRun / tmuxRunOut are the indirection the layout builder calls through.
// The argv builders above are pure and tested directly; the builder that
// SEQUENCES them is not, and since E-2106 three verbs share it — spawn, both
// resume paths, and a shell `task claim` — so the order and the pane it threads
// between splits are worth pinning without a live tmux server.
var (
	tmuxRun    = runTmux
	tmuxRunOut = runTmuxOut
)

// runTmux execs one tmux command, surfacing its stderr on failure.
func runTmux(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux %v: %w", args, err)
	}
	return nil
}

// runTmuxOut execs one tmux command and returns its trimmed stdout, for the
// queries whose ANSWER is the point (pane ids). stderr still goes to ours so a
// failure is visible; stdout is captured rather than inherited so tmux's `-P`
// pane-id echo never leaks into the spawner's terminal.
func runTmuxOut(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}
