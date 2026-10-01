package projectstatuscmd

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// The dedicated monitor session (E-1976, per E-1815's topology).
//
// `project monitor --tmux` gets its OWN tmux session with two panes: the monitor
// on top, and a bare interactive shell beneath it for running `endless` commands
// against what the monitor shows. Its own session rather than a window in the
// user's, because it is a standing surface rather than a piece of work — and
// because the user's
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
	// MonitorCmd is the argv of the monitor's pane.
	MonitorCmd []string
	// Project is the project the monitor is for. It is already baked into Session
	// (monitor.MonitorSessionName), and kept here because the layout is the one
	// place that knows both, which is what a future multiplexer driver will need.
	Project string
}

// newSessionArgs builds `tmux new-session -d -s <name> -c <dir>` — with NO
// command, so the session's first pane is the user's own shell.
//
// The SHELL is created first and the monitor inserted ABOVE it, not the other
// way round. This is E-1851's rule, learned in spawnlaunchcmd.buildLayoutAround
// and restated here because getting it backwards is invisible until it bites:
// the monitor shrinks its own pane to its frame on first paint
// (liveview.FitPaneToFrame), so creating the monitor first and then splitting a
// shell off it races that shrink. The shell then gets whatever few rows survived
// — or, in a short window, the split fails outright and there is no second pane
// at all. Splitting off a shell cannot race anything, because a shell does not
// resize itself.
//
// Detached (-d): creating the session must never yank the user out of what they
// are doing. Attaching or switching is a separate, explicit step.
//
// `-P -F #{pane_id}` reports the shell's pane id, which is what focus is handed
// back to once the monitor is in place.
func newSessionArgs(l windowLayout) []string {
	args := []string{"new-session", "-d", "-s", l.Session}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	return append(args, "-P", "-F", "#{pane_id}")
}

// splitMonitorArgs inserts the monitor ABOVE the shell pane (-b), running the
// monitor command.
//
// No `-l` height: the monitor sizes its own pane on first paint, from a budget
// it computes against the window (liveview.DetectRows), so a height guessed here
// would be overwritten a moment later — and guessing one is what E-1851 removed
// from the spawn layout for the same reason. tmux's even split is the starting
// point and the monitor settles from there.
//
// Targets the shell by PANE ID rather than by index: pane indexes depend on the
// user's pane-base-index and shift as panes are added, so index targeting
// silently addresses the wrong pane on a 1-based configuration.
func splitMonitorArgs(l windowLayout, shellPane string) []string {
	args := []string{"split-window", "-v", "-b", "-t", shellPane}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	args = append(args, "-P", "-F", "#{pane_id}", "--")
	return append(args, l.MonitorCmd...)
}

// selectPaneArgs hands focus to a pane. After the monitor is inserted it is the
// active pane (tmux selects a new split), and the monitor is read, not typed
// in —
// so focus goes back to the shell explicitly rather than by luck.
func selectPaneArgs(pane string) []string {
	return []string{"select-pane", "-t", pane}
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

// monitorOptionKey is the tmux session option that marks a session as one
// Endless built, and records which project's monitor it holds.
//
// One option carrying both facts: its PRESENCE is the ownership proof, its VALUE
// is the project. A session without it was made by someone else, whatever it is
// called.
//
// This is load-bearing exactly because the name is configurable. With the
// built-in `e-<project>-monitor` a collision was implausible; the moment a user
// can set `tmux.session_name` to `{{project}}` — which is the natural thing to
// want — the launcher can find a session with the right name that is the user's
// own shell. Adopting it would switch them into a window with no monitor and
// report "reusing", which is a lie told confidently.
const monitorOptionKey = "@endless_monitor"

// setMonitorOptionArgs / getMonitorOptionArgs stamp and read the ownership mark.
//
// NOTE the target carries NO `=` prefix, unlike every other builder in this
// file. tmux's option commands do not accept the exact-match form and fail
// outright with `no such session: =name` — which is how an earlier draft of this
// shipped inert: the argv was written to match its neighbours, every argv test
// agreed with it, and nothing asked tmux whether it would take it. The verify
// suite now drives a real tmux server for this round trip.
//
// Losing the exact match costs nothing here: these are only ever called for a
// session `has-session -t =<name>` has already resolved exactly.
func setMonitorOptionArgs(session, project string) []string {
	return []string{"set-option", "-t", session, monitorOptionKey, project}
}

func getMonitorOptionArgs(session string) []string {
	return []string{"show-options", "-v", "-t", session, monitorOptionKey}
}

// ownership is what the launcher learned about a session already using the name
// it wants.
type ownership int

const (
	// ownNone: no session by that name. Build it.
	ownNone ownership = iota
	// ownMine: Endless built it, for THIS project. Reuse it.
	ownMine
	// ownOtherProject: Endless built it, for a different project. Only reachable
	// when the configured template does not vary by project — `monitor`, say. Not
	// an error in the session; an error in the name.
	ownOtherProject
	// ownForeign: a session by that name exists and Endless did not make it.
	// Never adopt it.
	ownForeign
)

// checkOwnership decides which of those four a name is in.
//
// An unstamped session reads as FOREIGN, and that is the deliberate direction.
// tmux reports an unset user option as an error rather than an empty string, so
// "no stamp" and "cannot read the stamp" arrive alike — and both mean the same
// thing operationally: nothing here proves Endless built it. Guessing generously
// would put us straight back to adopting a stranger's session.
//
// The one cost is a monitor created before this stamp existed: it reads foreign
// and the user is told to close it. That is a one-time message, not a silent
// wrong window.
func checkOwnership(session, project string) ownership {
	if !tmuxOK(hasSessionArgs(session)) {
		return ownNone
	}
	owner, err := tmuxOut(getMonitorOptionArgs(session))
	if err != nil || owner == "" {
		return ownForeign
	}
	if owner != project {
		return ownOtherProject
	}
	return ownMine
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
	noSwitch := fs.Bool("no-switch", false, "create the session but leave the caller where they are")
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
		// two-pane session it just asked for. Installing tmux, or deciding to
		// watch the monitor by hand instead, is the user's.
		windowReport(
			"project-window: tmux is not installed. Run `endless project monitor` "+
				"directly in a terminal instead — the dedicated two-pane session needs tmux.",
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
		// The template is a config preference, and an agent must not go editing
		// the user's config over it — but nothing is blocked either: the monitor
		// opens under the default name.
		refusal.NoReport(fmt.Sprintf("project-window: %v", warn),
			"Continue; the monitor opened under the default session name").
			Command("project-window").Print()
	}

	layout := windowLayout{
		Session:    sessionName,
		Dir:        dir,
		MonitorCmd: monitorCommand(name),
		Project:    name,
	}

	created := false
	switch checkOwnership(layout.Session, layout.Project) {
	case ownMine:
		// Ours, for this project. Fall through to the switch/attach below.
	case ownOtherProject:
		// The fix is a line in the user's own `tmux.session_name`, which is a
		// preference nobody else gets to set for them.
		windowReport(
			fmt.Sprintf("project-window: the session %q already holds another project's monitor",
				layout.Session),
			"whether to give tmux.session_name a per-project name, or to close the other project's monitor").
			Text(fmt.Sprintf(
				"project-window: the session %q already holds another project's monitor.\n"+
					"Your `tmux.session_name` renders the same name for every project. "+
					"Include the project in it — the default is %q.",
				layout.Session, DefaultSessionNameTemplate)).
			Exit(1)
	case ownForeign:
		// Closing a tmux session is destructive to whatever is running in it,
		// and this one is the user's: Endless never stamped it.
		windowReport(
			fmt.Sprintf("project-window: a tmux session named %q already exists and Endless did not create it. Refusing to take it over",
				layout.Session),
			"whether to close their own tmux session, or to set tmux.session_name to a name Endless can have").
			Text(fmt.Sprintf(
				"project-window: a tmux session named %q already exists and Endless did not create it.\n"+
					"Refusing to take it over. Either close it, or set `tmux.session_name` "+
					"in .endless/config.json to a name of your own (default: %q).",
				layout.Session, DefaultSessionNameTemplate)).
			Exit(1)
	case ownNone:
		shellPane, serr := tmuxOut(newSessionArgs(layout))
		if serr != nil {
			// tmuxOut captures stdout only, so tmux's own reason is not in this
			// message and there is nothing here to retry differently.
			windowReport(
				fmt.Sprintf("project-window: creating the monitor session: %v", serr),
				"whether a tmux server that refused a new session is something they can clear").
				Exit(1)
		}
		// The monitor pane is best-effort: a shell with no monitor beside it is a
		// degraded but working window, and refusing to open one over a failed
		// split would trade the whole feature for half of it.
		if err = tmuxRun(splitMonitorArgs(layout, shellPane)); err != nil {
			refusal.NoReport(fmt.Sprintf("project-window: monitor pane: %v", err),
				"Continue; the session has a shell but no monitor pane").
				Command("project-window").Print()
		} else if err = tmuxRun(selectPaneArgs(shellPane)); err != nil {
			// Focus belongs on the shell: the monitor is read, the shell is typed
			// in. Cosmetic if it fails — the user presses a pane key.
			refusal.NoReport(fmt.Sprintf("project-window: focus shell: %v", err),
				"Continue; the focus failure is cosmetic").
				Command("project-window").Print()
		}
		// Stamp ownership LAST, so a session that failed to build is not claimed
		// by a project whose monitor never started. A stamp that fails is fatal,
		// not best-effort: an unstamped session reads as foreign on the next
		// launch, so leaving one behind would strand the user under a name they
		// are then told they cannot have.
		if err = tmuxRun(setMonitorOptionArgs(layout.Session, layout.Project)); err != nil {
			// A half-built session is now sitting in the user's tmux server under
			// a name the next launch will refuse as foreign, and only they can
			// close it — an agent closing a tmux session takes whatever else is
			// running in it with it.
			windowReport(
				fmt.Sprintf("project-window: could not mark %q as Endless's: %v", layout.Session, err),
				"whether to close the half-built session left in their tmux server, and whether to try again").
				Text(fmt.Sprintf(
					"project-window: could not mark %q as Endless's: %v\n"+
						"Close it before running this again; unmarked, it will be refused as foreign.",
					layout.Session, err)).
				Exit(1)
		}
		created = true
	}

	if *noSwitch {
		reportWindow(layout.Session, created, false)
		return
	}

	if monitor.InTmux() {
		if err = tmuxRun(switchClientArgs(layout.Session)); err != nil {
			// The session itself is built and waiting; what failed is moving the
			// user's attached client into it, which is theirs to do.
			windowReport(
				fmt.Sprintf("project-window: switching to %s: %v", layout.Session, err),
				"whether to switch or attach to the session themselves — it already exists").
				Exit(1)
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
	// tmux owns this terminal for the length of the attach, so its stdio — its
	// stderr included — is its own.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, refusal.Passthrough()
	if err = cmd.Run(); err != nil {
		msg := fmt.Sprintf("project-window: attaching to %s: %v", layout.Session, err)
		// The class turns on who is reading, and that is a question the process
		// can answer rather than hand over. An agent has no terminal for tmux to
		// open onto, so its attach failing means only "you cannot attach" — and
		// the session it asked for exists, so --no-switch gets the same result.
		// A person's attach failing in a real terminal is something else, and
		// only they can say what.
		if refusal.Agent() {
			refusal.NoReport(msg, "Rerun with --no-switch; the session already exists").
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
	// A success notice, not a refusal: nothing is blocked and there is nothing
	// to decide, so it carries no directive for either reader.
	refusal.Info(line).Print()
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
