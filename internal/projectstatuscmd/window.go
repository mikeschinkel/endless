package projectstatuscmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/tmuxservers"
)

// The monitor's tmux home: a window in the console (E-1976; its own server
// since E-2156; the console server since E-2240).
//
// `project monitor <name> --tmux` adds a window named for the project to the
// `console` session on Endless's console server (tmuxservers): the monitor
// above a bare shell. Each run ADDS a window, even when one already exists for
// that project, so a run always ends with a running monitor; a window too many
// is closed like any other.
//
// The console server is one of Endless's own, never the user's default server:
// the monitor and the task sessions want incompatible window layouts, and the
// user's own tmux is theirs. `endless tmux start` starts it with the task-
// session server; this command starts it too when it is not running.
//
// With no terminal attached to the console, the command opens one onto it (the
// configured tmux.terminal, or a detected terminal). With no way to open one, a
// command run outside tmux attaches in its own terminal; inside tmux it prints
// the attach command, because one tmux client cannot switch to a session on
// another server.
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
	// Session is the console session.
	Session string
	// Window is the window's name: the project's, folded to a legal tmux name.
	Window string
	// Dir is the working directory both panes start in — the project's main
	// checkout, because both are observation surfaces onto the main database and
	// the Python CLI routes its DB from cwd.
	Dir string
	// MonitorCmd is the argv of the monitor's pane.
	MonitorCmd []string
}

// layoutFor is the window for one project.
func layoutFor(project, dir string) windowLayout {
	return windowLayout{
		Session:    tmuxservers.ConsoleSession,
		Window:     monitor.SanitizeTmuxName(project),
		Dir:        dir,
		MonitorCmd: monitorCommand(project),
	}
}

// newSessionExtra is what the console's new-session takes when the project
// window is its first: the window's name and directory. The session is
// created detached and with NO command, so the first pane is the user's shell;
// the monitor is inserted above that shell afterwards (splitMonitorArgs).
func newSessionExtra(l windowLayout) []string {
	args := []string{"-n", l.Window}
	if l.Dir != "" {
		args = append(args, "-c", l.Dir)
	}
	return args
}

// newWindowArgs adds the project's window to a console that already exists, its
// first pane a shell, reporting that pane's id. The trailing `:` targets the
// session rather than a window in it, and `=` makes the match exact.
//
// -n also turns tmux's automatic-rename off for the window, so the tab keeps the
// project's name instead of showing whatever the shell is running.
func newWindowArgs(l windowLayout) []string {
	args := []string{"new-window", "-t", "=" + l.Session + ":", "-n", l.Window}
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

// OpenProjectWindow puts a monitor window for one project in the console:
// as the first window of a console it starts, or added to the running one.
// self is the endless-go binary, which a started server is initialized with.
//
// The monitor pane is best-effort: a shell with no monitor beside it is a
// degraded but working window, and refusing over a failed split would trade
// the whole feature for half of it. Its failure is returned as warn; err is
// set only when there is no window at all.
func OpenProjectWindow(console tmuxservers.Server, self, project, dir string) (startedServer bool, warn, err error) {
	layout := layoutFor(project, dir)
	var shellPane string

	shellPane, startedServer, err = console.StartSession(self, newSessionExtra(layout)...)
	if err != nil {
		return startedServer, nil, err
	}
	if !startedServer {
		shellPane, err = console.Out(newWindowArgs(layout)...)
		if err != nil {
			return false, nil, fmt.Errorf("open the monitor window: %w", err)
		}
	}
	if err = console.Run(splitMonitorArgs(layout, shellPane)...); err != nil {
		return startedServer, fmt.Errorf("monitor pane: %w", err), nil
	}
	// Cosmetic if it fails — the user presses a pane key.
	if err = console.Run(selectPaneArgs(shellPane)...); err != nil {
		return startedServer, fmt.Errorf("focus the shell: %w", err), nil
	}
	return startedServer, nil, nil
}

func runWindow(args []string) {
	fs := refusal.NewFlags("project-window")
	project := fs.String("project", "", "project name (required)")
	noSwitch := fs.Bool("no-switch", false, "build the window but do not open a terminal onto the console")
	if err := fs.Parse(args); err != nil {
		// Text carries flag's own error line and the flag defaults it appends,
		// which is what stderr held before this was classified.
		refusal.NoReport(err.Error(), "Fix the flag and retry").
			Command("project-window").Text(fs.Output()).Exit(2)
	}
	if *project == "" {
		// The window is named for its project and every run adds one, so the
		// project is named rather than guessed from cwd.
		refusal.NoReport("project-window: --project is required",
			"Name the project (`endless project list` lists them) and retry").
			Command("project-window").Exit(2)
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		// Installing tmux, or deciding to watch the monitor by hand instead, is
		// the user's: the monitor is a redraw loop in somebody's terminal, not
		// a thing an agent can run in place of the window it asked for.
		windowReport(
			"project-window: tmux is not installed. Run `endless project monitor` "+
				"directly in a terminal instead — the console window needs tmux.",
			"whether to install tmux or run the live monitor in their own terminal").
			Exit(1)
	}

	servers, err := tmuxservers.Resolve()
	if err != nil {
		windowReport(fmt.Sprintf("project-window: %v", err),
			"how to repair the Endless config file the tmux settings are read from").
			Exit(1)
	}
	if err = tmuxservers.EnsureDir(servers.Dir); err != nil {
		windowReport(fmt.Sprintf("project-window: %v", err),
			"whether to remove or fix that directory, or set tmux.socket_dir in ~/.config/endless/config.json").
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

	self, err := os.Executable()
	if err != nil {
		windowReport(fmt.Sprintf("project-window: resolve self: %v", err),
			"whether to reinstall endless-go, which could not resolve the path of its own running binary").
			Exit(1)
	}

	console := servers.Console
	started, warn, err := OpenProjectWindow(console, self, name, dir)
	if err != nil {
		windowReport(fmt.Sprintf("project-window: %v", err),
			"whether a tmux server that refused a new window is something they can clear").
			Exit(1)
	}
	if warn != nil {
		refusal.NoReport(fmt.Sprintf("project-window: %v", warn),
			"Continue; the window has a shell but may lack the monitor pane or focus").
			Command("project-window").Print()
	}

	where := fmt.Sprintf("added a %q window to the console", layoutFor(name, dir).Window)
	if started {
		where = fmt.Sprintf("started the console with a %q window", layoutFor(name, dir).Window)
	}

	switch {
	case *noSwitch:
		reportWindow(where, "Attach from a terminal: "+console.AttachCommand())
	case console.Attached():
		reportWindow(where, "It is in the terminal already attached to the console.")
	default:
		openConsoleTerminal(console, servers.Terminal, where)
	}
}

// openConsoleTerminal brings a terminal to a console nobody is attached to.
func openConsoleTerminal(console tmuxservers.Server, template, where string) {
	if launcher, ok := tmuxservers.ResolveLauncher(template); ok {
		err := launcher.Open(console)
		if err == nil {
			reportWindow(where, fmt.Sprintf("Opened a %s window attached to it.", launcher.Name))
			return
		}
		refusal.NoReport(fmt.Sprintf("project-window: opening a terminal: %v", err),
			"Continue; attach to the console another way, as below").
			Command("project-window").Print()
	}
	if monitor.InTmux() {
		// One tmux client cannot switch to a session on another server.
		reportWindow(where, "Attach from its own terminal window: "+console.AttachCommand())
		return
	}

	// Outside tmux the caller's terminal becomes the client, so the attach runs
	// in the foreground with this process's stdio and blocks until the user
	// detaches. Deliberately a child rather than a syscall.Exec: the launcher is
	// reached through a Python wrapper that owns the exit status.
	reportWindow(where, "")
	argv := console.AttachArgv()
	cmd := console.Command(argv[3:]...)
	// tmux owns this terminal for the length of the attach, so its stdio — its
	// stderr included — is its own.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, refusal.Passthrough()
	if err := cmd.Run(); err != nil {
		msg := fmt.Sprintf("project-window: attaching to the console: %v", err)
		// An agent has no terminal for tmux to open onto, so its attach failing
		// means only "you cannot attach" — and the window it asked for exists,
		// so --no-switch gets the same result. A person's attach failing in a
		// real terminal is something only they can explain.
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
// takes: the monitor window is not open, and the next move is a tmux server, a
// config file or an install — none of them a call this command could make
// differently.
func windowReport(summary, decision string) *refusal.Error {
	return refusal.Report(summary, decision).Command("project-window")
}

// reportWindow says what happened, on stderr so it never lands in a caller's
// captured stdout, and how to reach it.
func reportWindow(where, reach string) {
	line := "• " + where
	if reach != "" {
		line += "\n  " + reach
	}
	// A success notice, not a refusal: nothing is blocked and there is nothing
	// to decide, so it carries no directive for either reader.
	refusal.Info(line).Print()
}
