package spawnlaunchcmd

import (
	"fmt"
	"os"
	"os/exec"
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
