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

// newWindowArgs builds
//
//	tmux new-window -t <target> [-c <cwd>] -n <name> -P -F #{pane_id} -- <cmd...>
//
// target is REQUIRED and names the session the window is created in (E-2125).
// Without it tmux picks the "current" session, which off a command line means
// the most recently active one on the server — so a spawn asked for in one
// session materialized in whichever session the person happened to be looking
// at, with nothing on the window saying where it came from. The session that
// asked for the window is the session that gets it; see spawnerSession.
//
// The `--` terminates tmux flag parsing so the window command and its args are
// passed through literally (no shell re-quoting of cmd elements). When cwd is
// empty the `-c` flag is omitted and the window inherits the caller's cwd.
//
// `-P -F #{pane_id}` makes new-window report the created window's only pane, so
// the layout builder anchors on that id instead of looking the window back up
// by name. A name lookup is the same unqualified-target bug one call later: two
// sessions can each hold a window called `E-1705`, and `-t E-1705` would answer
// with whichever the server considered current.
func newWindowArgs(target, cwd, windowName string, cmd []string) []string {
	args := []string{"new-window", "-t", target}
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, "-n", windowName, "-P", "-F", "#{pane_id}", "--")
	return append(args, cmd...)
}

// sessionIDArgs builds `tmux display-message -p -t <pane> #{session_id}`, which
// resolves a pane to the id of the session holding it.
func sessionIDArgs(pane string) []string {
	return []string{"display-message", "-p", "-t", pane, "#{session_id}"}
}

// spawnerSession returns the new-window target: the session that owns the pane
// this process is running in, as a session id (`$3`) suffixed with `:` so tmux
// reads it as "this session, next free index" rather than as a window name.
//
// Session ids are used rather than names because a name can be changed or
// duplicated across a rename while a `$N` id is fixed for the session's life.
// The id is read from the pane rather than from $TMUX's third field, which
// records the session a client was attached to when the process started and
// goes stale the moment the pane is moved (`tmux move-window`, `break-pane`).
//
// An unresolvable target is an error, never a silent fall back to an untargeted
// new-window: landing in an unknown session is the defect, so a spawn that
// cannot say where it belongs refuses instead of guessing.
func spawnerSession() (string, error) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return "", fmt.Errorf(
			"cannot tell which tmux session to open the window in: " +
				"$TMUX_PANE is unset. Run this from inside a tmux pane")
	}
	id, err := tmuxRunOut(sessionIDArgs(pane)...)
	if err != nil {
		return "", fmt.Errorf("resolve session of pane %s: %w", pane, err)
	}
	if id == "" {
		return "", fmt.Errorf("pane %s reported no session id", pane)
	}
	return id + ":", nil
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
