// Package tmuxstopcmd implements `endless-go tmux stop` (E-2385), behind
// `endless tmux stop` and its alias `endless stop`: the counterpart of
// `endless start`, winding Endless's tmux servers down gracefully.
//
//	stop tasks    the task-session server, after every Claude in it has quit
//	stop console  the console server, which holds monitors and shells only
//	stop all      tasks, then the console (also spelled `stop --all`)
//
// `stop tasks` refuses while any session bound to one of its panes is working,
// listing them; --force skips that. It then freezes the record of open task
// windows (tmuxservers.StoppingOption), so the hooks cannot rewrite it while
// windows close and `endless start` restores the windows that were open when
// the stop began. It types `/exit` into each pane running Claude, so each
// quits through its own exit path and Endless records its session as ended,
// and waits for them. Only when every one has quit is the server killed; any
// still running at the timeout are listed and the server is left running,
// unless --force.
//
// A server that is not running is reported as already stopped, not an error.
package tmuxstopcmd

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/tmuxservers"
)

// Usage is the command's synopsis, printed when no target is named.
const Usage = `Usage: endless stop tasks|console|all [--force] [--timeout <duration>]
       endless stop --all [--force] [--timeout <duration>]

  tasks    Quit every Claude on the task-session server, then stop it
  console  Stop the console server
  all      Stop the task-session server, then the console
`

// pollInterval is how often the wait for Claude to quit looks again.
var pollInterval = 250 * time.Millisecond

// exitKeyGap separates `/exit` from the Enter that submits it. Claude reads a
// burst of keys arriving together as a paste, and an Enter inside a paste is a
// newline, not a submit.
var exitKeyGap = 300 * time.Millisecond

// Run is the `endless-go tmux stop` entry point.
func Run(args []string) {
	fs := refusal.NewFlags("tmux stop")
	all := fs.Bool("all", false, "stop the task-session server, then the console")
	force := fs.Bool("force", false, "stop the task-session server even while sessions are working or Claude has not quit")
	timeout := fs.Duration("timeout", 30*time.Second, "how long to wait for every Claude to quit")
	positional, err := parseInterspersed(fs, args)
	if err != nil {
		fs.ExitOnHelp(err)
		refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("tmux stop").Text(fs.Output()).Exit(2)
	}
	target, err := parseTarget(*all, positional)
	if err != nil {
		refusal.NoReport(fmt.Sprintf("tmux stop: %v", err),
			"Name one of tasks, console or all and retry").
			Command("tmux stop").Text(Usage).Exit(2)
	}

	servers, err := tmuxservers.Resolve()
	if err != nil {
		refusal.Report(fmt.Sprintf("tmux stop: %v", err),
			"how to repair the Endless config file the tmux settings are read from").
			Command("tmux stop").Exit(1)
	}

	out := os.Stdout
	opts := options{force: *force, timeout: *timeout}
	if target == "tasks" || target == "all" {
		if !stopTasks(out, servers.Tasks, opts) {
			if target == "all" {
				fmt.Fprintln(out, "• console: left running, because the task-session server is still running")
			}
			os.Exit(1)
		}
	}
	if target == "console" || target == "all" {
		stopConsole(out, servers.Console)
	}
}

// parseInterspersed parses flags on either side of the target (`stop tasks
// --timeout 10s`), returning the positionals: the flag package stops at the
// first positional, so it parses again after each one.
func parseInterspersed(fs *refusal.Flags, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// parseTarget reads which server(s) to stop: exactly one of tasks, console
// and all, or --all alone. Pure.
func parseTarget(all bool, args []string) (string, error) {
	switch {
	case len(args) > 1:
		return "", fmt.Errorf("one target, not %d (%s)", len(args), strings.Join(args, " "))
	case all && len(args) == 1 && args[0] != "all":
		return "", fmt.Errorf("--all and %q both name what to stop", args[0])
	case all:
		return "all", nil
	case len(args) == 0:
		return "", fmt.Errorf("name what to stop")
	}
	switch args[0] {
	case "tasks", "console", "all":
		return args[0], nil
	}
	return "", fmt.Errorf("unknown target %q", args[0])
}

type options struct {
	force   bool
	timeout time.Duration
}

// stopConsole kills the console server. It holds project monitors and shells,
// no Claude sessions, so there is nothing to wind down first.
func stopConsole(out io.Writer, console tmuxservers.Server) {
	if !console.Reachable() {
		fmt.Fprintf(out, "• console: already stopped (%s)\n", console.Socket)
		return
	}
	if err := console.Run("kill-server"); err != nil {
		refusal.Report(fmt.Sprintf("tmux stop: stopping the console server: %v", err),
			"whether a tmux server that would not stop is something they can clear").
			Command("tmux stop").Exit(1)
	}
	fmt.Fprintf(out, "• console: stopped (%s)\n", console.Socket)
}

// stopTasks winds the task-session server down. Reports whether the server is
// stopped when it returns — false when it refused or left the server running.
func stopTasks(out io.Writer, tasks tmuxservers.Server, opts options) (stopped bool) {
	if !tasks.Reachable() {
		fmt.Fprintf(out, "• tasks: already stopped (%s)\n", tasks.Socket)
		return true
	}
	panes, ok := listPanes(tasks)
	if !ok {
		refusal.Report("tmux stop: the task-session server answered but would not list its panes",
			"whether a tmux server that will not list its panes is something they can clear; nothing was stopped").
			Command("tmux stop").Exit(1)
	}
	uuid, _ := tasks.Out("show-options", "-gqv", "@server_uuid")
	sessions, err := monitor.SessionsOnPanes(uuid, paneIDs(panes))
	if err != nil && !opts.force {
		refusal.Report(fmt.Sprintf("tmux stop: reading which sessions are on the task-session server: %v", err),
			"whether to stop it anyway with --force, without knowing whether any session there is working; nothing was stopped").
			Command("tmux stop").Exit(1)
	}

	if working := workingSessions(sessions); len(working) > 0 && !opts.force {
		refusal.Report("tmux stop: sessions on the task-session server are working:\n"+describe(working),
			"whether to wait for them, or stop now with --force and interrupt their work; nothing was stopped").
			Command("tmux stop").Exit(1)
	}

	// Record what is open now, then freeze the record: closing windows must
	// not rewrite it, so a later `endless start` restores exactly these.
	if _, err = tasks.Record(); err != nil {
		refusal.NoReport(fmt.Sprintf("tmux stop: recording the open task windows: %v", err),
			"Continue; the next start restores the windows last recorded").
			Command("tmux stop").Print()
	}
	if err = tasks.Run("set-option", "-g", tmuxservers.StoppingOption, "1"); err != nil {
		refusal.NoReport(fmt.Sprintf("tmux stop: freezing the record of open task windows: %v", err),
			"Continue; the record may lose windows as they close").
			Command("tmux stop").Print()
	}

	targets := claudePanes(panes, sessions)
	for _, p := range targets {
		sendExit(tasks, p)
	}
	if len(targets) > 0 {
		fmt.Fprintf(out, "• tasks: asked %d Claude session(s) to quit; waiting up to %s\n",
			len(targets), opts.timeout)
	}
	left := waitForQuit(tasks, targets, opts.timeout)

	if len(left) > 0 && !opts.force {
		_ = tasks.Run("set-option", "-gu", tmuxservers.StoppingOption)
		refusal.Report(fmt.Sprintf("tmux stop: Claude has not quit in %d pane(s) after %s, so the task-session server was left running:\n%s",
			len(left), opts.timeout, describePanes(left, sessions)),
			"whether to answer those sessions and stop again, or stop now with --force and kill them").
			Command("tmux stop").Print()
		return false
	}
	// Printed before the kill: run from a pane on this server, this process
	// goes with it.
	fmt.Fprintf(out, "• tasks: stopped (%s)\n", tasks.Socket)
	if err = tasks.Run("kill-server"); err != nil {
		refusal.Report(fmt.Sprintf("tmux stop: stopping the task-session server: %v", err),
			"whether a tmux server that would not stop is something they can clear").
			Command("tmux stop").Exit(1)
	}
	return true
}

// pane is one pane of the server.
type pane struct {
	ID      string
	Command string // pane_current_command
}

func listPanes(s tmuxservers.Server) ([]pane, bool) {
	out, err := s.Out("list-panes", "-a", "-F", "#{pane_id}\t#{pane_current_command}")
	if err != nil {
		return nil, false
	}
	return parsePanes(out), true
}

// parsePanes reads `list-panes -F "#{pane_id}\t#{pane_current_command}"`. Pure.
func parsePanes(out string) []pane {
	var panes []pane
	for _, line := range strings.Split(out, "\n") {
		id, cmd, _ := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if id = strings.TrimSpace(id); id != "" {
			panes = append(panes, pane{ID: id, Command: strings.TrimSpace(cmd)})
		}
	}
	return panes
}

func paneIDs(panes []pane) []string {
	ids := make([]string, len(panes))
	for i, p := range panes {
		ids[i] = p.ID
	}
	return ids
}

// claudeCommand matches the pane_current_command of a pane running Claude
// Code: its name, or — what a live one reports, since Claude sets its process
// title to its version — a version number.
var claudeCommand = regexp.MustCompile(`^(?:claude(?:-code)?|\d+\.\d+\.\d+\S*)$`)

// claudePanes is the panes to type /exit into: those bound to a live session,
// and those whose command is Claude's, but never one at a shell prompt —
// /exit typed there is a command, and its Claude has already gone. Pure.
func claudePanes(panes []pane, sessions []monitor.PaneSession) []string {
	bound := map[string]bool{}
	for _, s := range sessions {
		bound[s.Pane] = true
	}
	var out []string
	for _, p := range panes {
		if monitor.IsShellCommand(p.Command) {
			continue
		}
		if bound[p.ID] || claudeCommand.MatchString(p.Command) {
			out = append(out, p.ID)
		}
	}
	return out
}

func workingSessions(sessions []monitor.PaneSession) []monitor.PaneSession {
	var out []monitor.PaneSession
	for _, s := range sessions {
		if s.State == string(sessionstate.Working) {
			out = append(out, s)
		}
	}
	return out
}

// describe lists sessions one per line: task, session id, state. Pure.
func describe(sessions []monitor.PaneSession) string {
	var b strings.Builder
	for _, s := range sessions {
		task := "no task"
		if s.TaskID > 0 {
			task = fmt.Sprintf("E-%d", s.TaskID)
		}
		fmt.Fprintf(&b, "  %s  ES-%d  %s\n", task, s.EndlessSessionID, s.State)
	}
	return strings.TrimRight(b.String(), "\n")
}

// describePanes lists panes Claude is still running in, with the session
// bound there when there is one. Pure.
func describePanes(left []string, sessions []monitor.PaneSession) string {
	var lines []string
	for _, id := range left {
		i := slices.IndexFunc(sessions, func(s monitor.PaneSession) bool { return s.Pane == id })
		if i < 0 {
			lines = append(lines, fmt.Sprintf("  pane %s  (no Endless session)", id))
			continue
		}
		lines = append(lines, fmt.Sprintf("  pane %s%s", id, strings.TrimPrefix(describe(sessions[i:i+1]), " ")))
	}
	return strings.Join(lines, "\n")
}

// sendExit types /exit into a pane, then Enter.
func sendExit(s tmuxservers.Server, paneID string) {
	_ = s.Run("send-keys", "-t", paneID, "-l", "/exit")
	time.Sleep(exitKeyGap)
	_ = s.Run("send-keys", "-t", paneID, "Enter")
}

// waitForQuit waits up to timeout for Claude to quit in every target pane, and
// returns those it is still running in. A pane has quit when it is gone — a
// spawned window's Claude is its pane's command — or is back at a shell.
func waitForQuit(s tmuxservers.Server, targets []string, timeout time.Duration) []string {
	deadline := time.Now().Add(timeout)
	left := targets
	for len(left) > 0 {
		panes, ok := listPanes(s)
		if !ok {
			// The server went away: nothing is running in it.
			return nil
		}
		left = stillRunning(left, panes)
		if len(left) == 0 || !time.Now().Before(deadline) {
			break
		}
		time.Sleep(pollInterval)
	}
	return left
}

// stillRunning is the targets present in panes and not at a shell. Pure.
func stillRunning(targets []string, panes []pane) []string {
	cmd := map[string]string{}
	for _, p := range panes {
		cmd[p.ID] = p.Command
	}
	var left []string
	for _, id := range targets {
		if c, present := cmd[id]; present && !monitor.IsShellCommand(c) {
			left = append(left, id)
		}
	}
	return left
}
