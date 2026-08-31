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
	// MonitorCmd is the argv of the board's pane.
	MonitorCmd []string
	// Project is the project the board is for, stamped on the session so a later
	// launch can tell whether an existing session is showing THIS project.
	Project string
}

// newSessionArgs builds `tmux new-session -d -s <name> -c <dir>` — with NO
// command, so the session's first pane is the user's own shell.
//
// The SHELL is created first and the board inserted ABOVE it, not the other way
// round. This is E-1851's rule, learned in spawnlaunchcmd.buildSpawnLayout and
// restated here because getting it backwards is invisible until it bites: the
// board shrinks its own pane to its frame on first paint
// (liveview.FitPaneToFrame), so creating the board first and then splitting a
// shell off it races that shrink. The shell then gets whatever few rows survived
// — or, in a short window, the split fails outright and there is no second pane
// at all. Splitting off a shell cannot race anything, because a shell does not
// resize itself.
//
// Detached (-d): creating the session must never yank the user out of what they
// are doing. Attaching or switching is a separate, explicit step.
//
// `-P -F #{pane_id}` reports the shell's pane id, which is what focus is handed
// back to once the board is in place.
func newSessionArgs(l windowLayout) []string {
	args := []string{"new-session", "-d", "-s", l.Session}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	return append(args, "-P", "-F", "#{pane_id}")
}

// splitBoardArgs inserts the board ABOVE the shell pane (-b), running the
// monitor command.
//
// No `-l` height: the board sizes its own pane on first paint, from a budget it
// computes against the window (liveview.DetectRows), so a height guessed here
// would be overwritten a moment later — and guessing one is what E-1851 removed
// from the spawn layout for the same reason. tmux's even split is the starting
// point and the board settles from there.
//
// Targets the shell by PANE ID rather than by index: pane indexes depend on the
// user's pane-base-index and shift as panes are added, so index targeting
// silently addresses the wrong pane on a 1-based configuration.
func splitBoardArgs(l windowLayout, shellPane string) []string {
	args := []string{"split-window", "-v", "-b", "-t", shellPane}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	args = append(args, "-P", "-F", "#{pane_id}", "--")
	return append(args, l.MonitorCmd...)
}

// selectPaneArgs hands focus to a pane. After the board is inserted it is the
// active pane (tmux selects a new split), and the board is read, not typed in —
// so focus goes back to the shell explicitly rather than by luck.
func selectPaneArgs(pane string) []string {
	return []string{"select-pane", "-t", pane}
}

// projectOptionArgs stamps the project a monitor session was built for onto the
// session itself, and reads it back.
//
// Without it a second project's launch finds a session by the right name and
// switches to it — showing that project's user the FIRST project's board, with
// nothing on screen to say so except a legend they had no reason to re-read.
// A session option rather than parsing the pane's command line: the option is
// what the launcher wrote, the command line is a coincidence of how the pane was
// started. `@endless_*` matches the window options spawn-launch already sets.
//
// NOTE the target has NO `=` prefix, unlike every other builder in this file.
// tmux's option commands do not accept the exact-match form and fail outright
// with `no such session: =e-monitor` — which is how this shipped broken: the
// argv-shape test agreed with the other builders and never asked tmux whether it
// would take them. Losing the exact match costs nothing here, because these are
// only ever called for a session `has-session -t =<name>` has already resolved
// exactly, so tmux's prefix fallback has nothing left to reach for.
//
// Reading an option that was never set is an ERROR in tmux ("invalid option"),
// not an empty string. sessionNameFor treats both alike as "unstamped".
func setProjectOptionArgs(session, project string) []string {
	return []string{"set-option", "-t", session, "@endless_project", project}
}

func getProjectOptionArgs(session string) []string {
	return []string{"show-options", "-v", "-t", session, "@endless_project"}
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
		Session:    sessionNameFor(name),
		Dir:        dir,
		MonitorCmd: monitorCommand(name),
		Project:    name,
	}

	created := false
	if !tmuxOK(hasSessionArgs(layout.Session)) {
		shellPane, serr := tmuxOut(newSessionArgs(layout))
		if serr != nil {
			fmt.Fprintf(os.Stderr, "project-window: creating the monitor session: %v\n", serr)
			os.Exit(1)
		}
		// The board pane is best-effort: a shell with no board beside it is a
		// degraded but working window, and refusing to open one over a failed
		// split would trade the whole feature for half of it.
		if err = tmuxRun(splitBoardArgs(layout, shellPane)); err != nil {
			fmt.Fprintf(os.Stderr, "project-window: board pane: %v\n", err)
		} else if err = tmuxRun(selectPaneArgs(shellPane)); err != nil {
			// Focus belongs on the shell: the board is read, the shell is typed
			// in. Cosmetic if it fails — the user presses a pane key.
			fmt.Fprintf(os.Stderr, "project-window: focus shell: %v\n", err)
		}
		// Stamp the project LAST, so a session that failed to build is not
		// claimed by a project whose board never started.
		if err = tmuxRun(setProjectOptionArgs(layout.Session, layout.Project)); err != nil {
			fmt.Fprintf(os.Stderr, "project-window: stamping project: %v\n", err)
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

// sessionNameFor picks the tmux session name for one project's board.
//
// The plain name is monitor.MonitorSessionName — `e-monitor`, short enough to
// survive the truncation a tmux status line applies to a session name (the
// project-qualified `endless-monitor` it replaced arrived on the tab as
// `endless-m`). It carries no project, because in the overwhelmingly common case
// there is one board and the board's own legend already names its project.
//
// The qualified form exists for the case that name cannot serve: a session by
// that name already exists AND was built for a DIFFERENT project. Reusing it
// there would switch the user to another project's board and say nothing —
// a wrong answer, not a collision. So the second project gets
// `e-monitor-<project>` and both stay reachable.
//
// Reads the stamp the launcher wrote (setProjectOptionArgs), not the pane's
// command line: the stamp is what this code recorded, the command line is a
// coincidence of how the pane happened to start. An unstamped session — one
// predating the stamp, or one the user made by hand under this name — reads as
// empty and is treated as ours, which is the forgiving direction: it hands the
// user the session they already had rather than quietly opening a second one
// beside it.
func sessionNameFor(project string) string {
	base := monitor.MonitorSessionName
	if !tmuxOK(hasSessionArgs(base)) {
		return base
	}
	owner, err := tmuxOut(getProjectOptionArgs(base))
	if err != nil || owner == "" || owner == project {
		return base
	}
	return base + "-" + monitor.SanitizeTmuxName(project)
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

// tmuxOut runs a tmux command and returns its trimmed stdout — for the argv
// builders that ask tmux a question (a created pane's id, a session's stamp).
func tmuxOut(args []string) (string, error) {
	out, err := exec.Command("tmux", args...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}
