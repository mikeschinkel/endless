package projectstatuscmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// The monitor's tmux home (E-1976; moved to its own server by E-2156).
//
// `project monitor --tmux` runs on a DEDICATED tmux server, `tmux -L endless`,
// in a session named for the project. A server of its own because the monitor
// and the task sessions want incompatible window layouts: on one server,
// moving between them is a switch-client that rearranges what you were looking
// at, and the monitor's session crowds the task sessions in `tmux ls`. On its
// own server it lives in its own terminal window, and `tmux -L endless ls`
// lists only monitors.
//
// The cost: one tmux client cannot switch to a session on another server. From
// inside tmux the launcher builds the window and prints the attach command for
// another terminal; from a plain terminal it attaches.
//
// Every launch ADDS a window named `projects` — a bare shell with the monitor
// above it — to the project's session, creating the session if needed. Adding
// rather than reusing means a launch always ends with a running monitor, even
// when the last one was closed; a launch too many leaves a duplicate window,
// which the user closes like any other.
//
// The bare shell is `agentenv` `unknown`: the CLI fails open there and the hook
// no-ops, which is the desired behaviour — it is a place to type, not a place an
// agent runs.
//
// Focus lands on the SHELL, not the monitor. The monitor is read; the shell is
// used. Nothing is ever typed into a pane running a redraw loop.

// monitorServer is the tmux socket name (-L) the monitor runs on.
const monitorServer = "endless"

// monitorWindowName names the window each launch adds. Set with -n, which also
// turns tmux's automatic-rename off for it, so the tab keeps the name instead of
// showing whatever the shell is running.
const monitorWindowName = "projects"

// windowLayout is every tmux fact the launcher needs, in one place so the argv
// builders below stay pure and testable.
type windowLayout struct {
	// Session is the tmux session name (sessionNameFor).
	Session string
	// Dir is the working directory both panes start in — the project's main
	// checkout, because both are observation surfaces onto the main database and
	// the Python CLI routes its DB from cwd.
	Dir string
	// MonitorCmd is the argv of the monitor's pane.
	MonitorCmd []string
}

// newSessionArgs builds the session with its first `projects` window: detached
// (-d), so creating it never yanks the user out of what they are doing, and
// with NO command, so the first pane is the user's own shell. The monitor is
// inserted above that shell afterwards (splitMonitorArgs).
//
// `-P -F #{pane_id}` reports the shell's pane id, which focus is handed back to
// once the monitor is in place.
func newSessionArgs(l windowLayout) []string {
	args := []string{"new-session", "-d", "-s", l.Session, "-n", monitorWindowName}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	return append(args, "-P", "-F", "#{pane_id}")
}

// newWindowArgs adds a `projects` window to a session that already exists, its
// first pane a shell, reporting that pane's id. The trailing `:` targets the
// session rather than a window in it, and `=` makes the match exact.
func newWindowArgs(l windowLayout) []string {
	args := []string{"new-window", "-t", "=" + l.Session + ":", "-n", monitorWindowName}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	return append(args, "-P", "-F", "#{pane_id}")
}

// splitMonitorArgs inserts the monitor ABOVE the shell pane (-b), running the
// monitor command.
//
// `-l` sets the monitor's STARTING height (monitorPctOfWindow). It is only a
// start: the monitor never resizes its pane, it fits its frame to whatever
// height the pane has (E-2156), so after this the divider is the user's.
//
// Targets the shell by PANE ID rather than by index: pane indexes depend on the
// user's pane-base-index and shift as panes are added, so index targeting
// silently addresses the wrong pane on a 1-based configuration.
func splitMonitorArgs(l windowLayout, shellPane string) []string {
	args := []string{"split-window", "-v", "-b", "-t", shellPane,
		"-l", fmt.Sprintf("%d%%", monitorPctOfWindow)}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	args = append(args, "-P", "-F", "#{pane_id}", "--")
	return append(args, l.MonitorCmd...)
}

// selectPaneArgs hands focus to a pane. After the monitor is inserted it is the
// active pane (tmux selects a new split), and the monitor is read, not typed
// in — so focus goes back to the shell explicitly rather than by luck.
func selectPaneArgs(pane string) []string {
	return []string{"select-pane", "-t", pane}
}

// hasSessionArgs builds the existence check that decides between creating the
// session and adding a window to it.
func hasSessionArgs(session string) []string {
	return []string{"has-session", "-t", "=" + session}
}

// attachArgs builds the attach used when the caller is NOT inside tmux.
func attachArgs(session string) []string {
	return []string{"attach-session", "-t", "=" + session}
}

// attachHint is the command that reaches the monitor from another terminal.
func attachHint(session string) string {
	return fmt.Sprintf("tmux -L %s attach -t %s", monitorServer, session)
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
	fs := refusal.NewFlags("project-window")
	project := fs.String("project", "", "project name (default: the project enclosing the working directory)")
	noSwitch := fs.Bool("no-switch", false, "build the window but do not attach to it")
	if err := fs.Parse(args); err != nil {
		// Text carries flag's own error line and the flag defaults it appends,
		// which is what stderr held before this was classified.
		refusal.NoReport(err.Error(), "Fix the flag and retry").
			Command("project-window").Text(fs.Output()).Exit(2)
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		// The message names a command, which by the letter of the rule would
		// make it no-report — but `endless project monitor` is a redraw loop in
		// somebody's terminal, not a thing an agent can run in place of the
		// two-pane window it just asked for. Installing tmux, or deciding to
		// watch the monitor by hand instead, is the user's.
		windowReport(
			"project-window: tmux is not installed. Run `endless project monitor` "+
				"directly in a terminal instead — the dedicated two-pane window needs tmux.",
			"whether to install tmux or run the live monitor in their own terminal").
			Exit(1)
	}

	projectID, name := resolveProject(options{project: *project})
	dir, err := monitor.ProjectPath(projectID)
	if err != nil {
		// A missing or unreadable path must not stop the monitor from opening: the
		// monitor names its project explicitly and does not depend on cwd. The
		// panes just start wherever the launcher was run.
		dir = ""
	}

	// The name is a PREFERENCE, read from layered config; a bad template warns
	// and falls back rather than refusing, because a typo in a preference must
	// not be able to stop the monitor from opening.
	sessionName, warn := sessionNameFor(name, sessionNameTemplate(dir))
	if warn != nil {
		refusal.NoReport(fmt.Sprintf("project-window: %v", warn),
			"Continue; the monitor opened under the default session name").
			Command("project-window").Print()
	}

	layout := windowLayout{Session: sessionName, Dir: dir, MonitorCmd: monitorCommand(name)}

	created := !tmuxOK(hasSessionArgs(layout.Session))
	build := newWindowArgs(layout)
	if created {
		build = newSessionArgs(layout)
	}
	shellPane, err := tmuxOut(build)
	if err != nil {
		// tmuxOut captures stdout only, so tmux's own reason is not in this
		// message and there is nothing here to retry differently.
		windowReport(
			fmt.Sprintf("project-window: opening the monitor window: %v", err),
			"whether a tmux server that refused a new window is something they can clear").
			Exit(1)
	}
	// The monitor pane is best-effort: a shell with no monitor beside it is a
	// degraded but working window, and refusing over a failed split would trade
	// the whole feature for half of it.
	if err = tmuxRun(splitMonitorArgs(layout, shellPane)); err != nil {
		refusal.NoReport(fmt.Sprintf("project-window: monitor pane: %v", err),
			"Continue; the window has a shell but no monitor pane").
			Command("project-window").Print()
	} else if err = tmuxRun(selectPaneArgs(shellPane)); err != nil {
		// Focus belongs on the shell: the monitor is read, the shell is typed
		// in. Cosmetic if it fails — the user presses a pane key.
		refusal.NoReport(fmt.Sprintf("project-window: focus shell: %v", err),
			"Continue; the focus failure is cosmetic").
			Command("project-window").Print()
	}

	// One tmux client cannot switch to a session on another server, so from
	// inside tmux the window is built and the user is told where it is.
	if *noSwitch || monitor.InTmux() {
		reportWindow(layout.Session, created, false)
		return
	}

	// Outside tmux the caller's terminal becomes the client, so the attach runs
	// in the foreground with this process's stdio and blocks until the user
	// detaches. Deliberately a child rather than a syscall.Exec: the launcher is
	// reached through a Python wrapper that owns the exit status, and replacing
	// the process would take that reporting path away for no gain — tmux holds
	// the terminal either way.
	cmd := exec.Command("tmux", append([]string{"-L", monitorServer}, attachArgs(layout.Session)...)...)
	// tmux owns this terminal for the length of the attach, so its stdio — its
	// stderr included — is its own.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, refusal.Passthrough()
	if err = cmd.Run(); err != nil {
		msg := fmt.Sprintf("project-window: attaching to %s: %v", layout.Session, err)
		// The class turns on who is reading, and that is a question the process
		// can answer rather than hand over. An agent has no terminal for tmux to
		// open onto, so its attach failing means only "you cannot attach" — and
		// the window it asked for exists, so --no-switch gets the same result.
		// A person's attach failing in a real terminal is something else, and
		// only they can say what.
		if refusal.Agent() {
			refusal.NoReport(msg, "Rerun with --no-switch; the window already exists").
				Command("project-window").Exit(1)
		}
		windowReport(msg,
			"whether a tmux attach that failed in their own terminal is something they can clear").
			Exit(1)
	}
}

// windowReport is the shape every project-window refusal that stops the command
// takes: the monitor session is not open, and the next move is a tmux server, a
// config preference or an install — none of them a call this command could make
// differently.
func windowReport(summary, decision string) *refusal.Error {
	return refusal.Report(summary, decision).Command("project-window")
}

// reportWindow says what happened, on stderr so it never lands in a caller's
// captured stdout, and how to reach it when the launcher did not attach.
func reportWindow(session string, created, attached bool) {
	where := fmt.Sprintf("added a %q window to tmux session %q", monitorWindowName, session)
	if created {
		where = fmt.Sprintf("created tmux session %q with a %q window", session, monitorWindowName)
	}
	line := fmt.Sprintf("• %s on the %q tmux server", where, monitorServer)
	if !attached {
		line += fmt.Sprintf("\n  Attach from its own terminal window: %s", attachHint(session))
	}
	// A success notice, not a refusal: nothing is blocked and there is nothing
	// to decide, so it carries no directive for either reader.
	refusal.Info(line).Print()
}

// tmuxArgs puts every launcher command on the monitor's own server.
func tmuxArgs(args []string) []string {
	return append([]string{"-L", monitorServer}, args...)
}

func tmuxRun(args []string) error {
	cmd := exec.Command("tmux", tmuxArgs(args)...)
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
	return exec.Command("tmux", tmuxArgs(args)...).Run() == nil
}

// tmuxOut runs a tmux command and returns its trimmed stdout — for the argv
// builders that ask tmux a question (a created pane's id).
func tmuxOut(args []string) (string, error) {
	out, err := exec.Command("tmux", tmuxArgs(args)...).Output()
	if err != nil {
		return "", fmt.Errorf("tmux %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}
