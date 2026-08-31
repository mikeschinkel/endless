package projectstatuscmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// The dedicated monitor session (E-1976, per E-1815's topology).
//
// The board gets its OWN tmux session with two panes: the monitor on top, and a
// bare interactive shell beneath it for running `endless` commands against what
// the monitor shows. Its own session rather than a window in the user's, because
// it is a standing surface rather than a piece of work — and because the user's
// windows are where auto-spawned sessions land (E-1814), which a review-batch
// session would take out of sight.
//
// The bare shell is `agentenv` `unknown`: the CLI fails open there and the hook
// no-ops, which is the desired behaviour — it is a place to type, not a place an
// agent runs.
//
// Focus lands on the SHELL, not the monitor. The monitor is read; the shell is
// used. Nothing is ever typed into a pane running a redraw loop.

// windowLayout is every tmux fact the launcher needs, in one place so the argv
// builders below stay pure and testable.
type windowLayout struct {
	// Session is the tmux session name (monitor.ProjectSessionName).
	Session string
	// Dir is the working directory both panes start in — the project's main
	// checkout, because both are observation surfaces onto the main database and
	// the Python CLI routes its DB from cwd.
	Dir string
	// MonitorCmd is the argv of the top pane.
	MonitorCmd []string
	// ShellHeight is the bottom pane's height in rows. The monitor shrinks itself
	// to its frame on first paint, so this is only the starting split.
	ShellHeight int
}

// shellPaneRows is the bottom pane's initial height. The monitor re-fits itself
// on its first repaint and hands back whatever it does not need, so this only
// has to be big enough to type in before that happens.
const shellPaneRows = 12

// newSessionArgs builds `tmux new-session -d -s <name> -c <dir> -- <cmd...>`.
// Detached (-d): creating the session must never yank the user out of what they
// are doing. Attaching or switching is a separate, explicit step.
func newSessionArgs(l windowLayout) []string {
	args := []string{"new-session", "-d", "-s", l.Session}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	args = append(args, "--")
	return append(args, l.MonitorCmd...)
}

// splitShellArgs builds the shell pane: a vertical split below the monitor,
// running the user's default shell (no `--`, so tmux runs its default command).
//
// Targets the session by name rather than by pane index: pane indexes depend on
// the user's pane-base-index and shift as panes are added, so index targeting
// silently addresses the wrong pane on a 1-based configuration.
func splitShellArgs(l windowLayout) []string {
	args := []string{"split-window", "-v", "-t", l.Session}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	if l.ShellHeight > 0 {
		args = append(args, "-l", fmt.Sprint(l.ShellHeight))
	}
	return append(args, "-P", "-F", "#{pane_id}")
}

// hasSessionArgs builds the existence check that makes the launcher idempotent.
func hasSessionArgs(session string) []string {
	return []string{"has-session", "-t", "=" + session}
}

// switchClientArgs moves the ATTACHED client to the monitor session — the right
// move when the caller is already inside tmux, where `attach` would refuse
// (nested) and is not what was wanted anyway.
func switchClientArgs(session string) []string {
	return []string{"switch-client", "-t", "=" + session}
}

// attachArgs builds the attach used when the caller is NOT inside tmux.
func attachArgs(session string) []string {
	return []string{"attach-session", "-t", "=" + session}
}

// monitorCommand is the argv run in the monitor pane. `endless project monitor
// <project>` is the verb a user would type; it is resolved to an absolute path
// when possible so the pane does not depend on tmux's PATH matching the
// launcher's, and left bare (letting tmux's execvp report the failure in-pane)
// when the CLI is not on PATH at all.
//
// The project is named EXPLICITLY rather than left to cwd resolution. The pane's
// directory is the main checkout today, but a launcher that depends on that
// coincidence breaks the moment the layout's home changes, and a monitor showing
// the wrong project is worse than one that fails to open.
func monitorCommand(project string) []string {
	bin, err := exec.LookPath("endless")
	if err != nil {
		bin = "endless"
	}
	return []string{bin, "project", "monitor", project}
}

func runWindow(args []string) {
	fs := flag.NewFlagSet("project-window", flag.ContinueOnError)
	project := fs.String("project", "", "project name (default: the project enclosing the working directory)")
	noSwitch := fs.Bool("no-switch", false, "create the session but leave the caller where they are")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		fmt.Fprintln(os.Stderr,
			"project-window: tmux is not installed. Run `endless project monitor` "+
				"directly in a terminal instead — the dedicated two-pane session needs tmux.")
		os.Exit(1)
	}

	projectID, name := resolveProject(options{project: *project})
	dir, err := monitor.ProjectPath(projectID)
	if err != nil {
		// A missing or unreadable path must not stop the board from opening: the
		// monitor names its project explicitly and does not depend on cwd. The
		// panes just start wherever the launcher was run.
		dir = ""
	}

	layout := windowLayout{
		Session:     monitor.ProjectSessionName(name),
		Dir:         dir,
		MonitorCmd:  monitorCommand(name),
		ShellHeight: shellPaneRows,
	}

	created := false
	if !tmuxOK(hasSessionArgs(layout.Session)) {
		if err = tmuxRun(newSessionArgs(layout)); err != nil {
			fmt.Fprintf(os.Stderr, "project-window: creating the monitor session: %v\n", err)
			os.Exit(1)
		}
		// The shell pane is best-effort: a monitor with no shell beside it is
		// still a working board, and refusing to open one over a failed split
		// would trade the whole feature for half of it.
		if err = tmuxRun(splitShellArgs(layout)); err != nil {
			fmt.Fprintf(os.Stderr, "project-window: shell pane: %v\n", err)
		}
		created = true
	}

	if *noSwitch {
		reportWindow(layout.Session, created, false)
		return
	}

	if monitor.InTmux() {
		if err = tmuxRun(switchClientArgs(layout.Session)); err != nil {
			fmt.Fprintf(os.Stderr, "project-window: switching to %s: %v\n", layout.Session, err)
			os.Exit(1)
		}
		reportWindow(layout.Session, created, true)
		return
	}

	// Outside tmux the caller's terminal becomes the client, so the attach runs
	// in the foreground with this process's stdio and blocks until the user
	// detaches. Deliberately a child rather than a syscall.Exec: the launcher is
	// reached through a Python wrapper that owns the exit status, and replacing
	// the process would take that reporting path away for no gain — tmux holds
	// the terminal either way.
	cmd := exec.Command("tmux", attachArgs(layout.Session)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err = cmd.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "project-window: attaching to %s: %v\n", layout.Session, err)
		os.Exit(1)
	}
}

// reportWindow says what happened, on stderr so it never lands in a caller's
// captured stdout. Idempotence is only useful if the user can tell which of the
// two things occurred.
func reportWindow(session string, created, switched bool) {
	verb := "reusing"
	if created {
		verb = "created"
	}
	line := fmt.Sprintf("• %s tmux session %q", verb, session)
	if !switched {
		line += fmt.Sprintf("  (attach with: tmux attach -t %s)", session)
	}
	fmt.Fprintln(os.Stderr, line)
}

func tmuxRun(args []string) error {
	cmd := exec.Command("tmux", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return err
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

// tmuxOK reports whether a tmux command succeeded, discarding its output. Used
// for has-session, where the exit status IS the answer.
func tmuxOK(args []string) bool {
	return exec.Command("tmux", args...).Run() == nil
}
