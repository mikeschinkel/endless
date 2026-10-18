// Package tmuxstartcmd implements `endless-go tmux start` (E-2240), behind
// `endless tmux start` and its alias `endless start`: the way Endless is
// launched.
//
// It starts Endless's two tmux servers (see internal/tmuxservers) and opens a
// terminal window attached to each:
//
//   - the console server, session `console`. A console it starts opens with a
//     project-monitor window for the project enclosing the working directory,
//     or with a plain shell window outside a project.
//   - the task-session server, session `tasks` by default, where task windows
//     live. A task window recorded as open when the server last ran, and no
//     longer open, is recreated and Claude resumed in it; `--no-restore` skips
//     that.
//
// Running it again reuses whatever is already running and opens both terminal
// windows again — that is how to get a terminal back onto a server after
// closing one.
package tmuxstartcmd

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/projectstatuscmd"
	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/resumewindowscmd"
	"github.com/mikeschinkel/endless/internal/tmuxservers"
)

// Run is the `endless-go tmux start` entry point.
func Run(args []string) {
	fs := refusal.NewFlags("tmux start")
	noRestore := fs.Bool("no-restore", false, "start without recreating the task windows open when the task-session server last ran")
	if err := fs.Parse(args); err != nil {
		fs.ExitOnHelp(err)
		refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("tmux start").Text(fs.Output()).Exit(2)
	}
	if fs.NArg() > 0 {
		refusal.NoReport(fmt.Sprintf("tmux start: unexpected argument %q", fs.Arg(0)),
			"Re-run with no arguments").Command("tmux start").Exit(2)
	}

	if _, err := exec.LookPath("tmux"); err != nil {
		startReport("tmux start: tmux is not installed, and Endless's servers are tmux servers",
			"whether to install tmux").Exit(1)
	}
	servers, err := tmuxservers.Resolve()
	if err != nil {
		startReport(fmt.Sprintf("tmux start: %v", err),
			"how to repair the Endless config file the tmux settings are read from").Exit(1)
	}
	if err = tmuxservers.EnsureDir(servers.Dir); err != nil {
		startReport(fmt.Sprintf("tmux start: %v", err),
			"whether to remove or fix that directory, or set tmux.socket_dir in ~/.config/endless/config.json").Exit(1)
	}
	self, err := os.Executable()
	if err != nil {
		startReport(fmt.Sprintf("tmux start: resolve self: %v", err),
			"whether to reinstall endless-go, which could not resolve the path of its own running binary").Exit(1)
	}

	// The record is read BEFORE the task-session server starts: starting it
	// links its first window, which fires the hook that rewrites the record
	// from what is open — nothing yet.
	recorded, recErr := tmuxservers.ReadState()

	out := os.Stdout
	startConsole(out, servers.Console, self)
	created := startTasks(out, servers.Tasks, self)
	switch {
	case *noRestore:
	case recErr != nil:
		refusal.NoReport(fmt.Sprintf("tmux start: reading the record of open task windows: %v", recErr),
			"Continue; no task windows were restored").Command("tmux start").Print()
	default:
		restore(out, servers.Tasks, recorded, created)
	}
	// The hooks keep the record current from here on; this records the set the
	// start itself produced, and a server started before the hooks existed.
	if _, err = servers.Tasks.Record(); err != nil {
		refusal.NoReport(fmt.Sprintf("tmux start: recording the open task windows: %v", err),
			"Continue; restore after the next restart may miss windows").
			Command("tmux start").Print()
	}
	openTerminals(out, servers)
}

// startConsole starts the console server unless it is running.
func startConsole(out io.Writer, console tmuxservers.Server, self string) {
	if console.HasSession() {
		fmt.Fprintf(out, "• console: running, reused (%s)\n", console.Socket)
		return
	}
	id, name, err := monitor.ProjectForCwd()
	if err == nil && id > 0 {
		dir, perr := monitor.ProjectPath(id)
		if perr != nil {
			dir = ""
		}
		_, warn, oerr := projectstatuscmd.OpenProjectWindow(console, self, name, dir)
		if oerr != nil {
			startReport(fmt.Sprintf("tmux start: %v", oerr),
				"whether a tmux server that would not start is something they can clear").Exit(1)
		}
		if warn != nil {
			refusal.NoReport(fmt.Sprintf("tmux start: console window: %v", warn),
				"Continue; the window has a shell but may lack the monitor pane").
				Command("tmux start").Print()
		}
		fmt.Fprintf(out, "• console: started (%s), with a monitor window for %s\n", console.Socket, name)
		return
	}
	if err != nil && !errors.Is(err, monitor.ErrNoProject) {
		// The working directory's project could not be looked up. The console
		// still opens; it just opens on a shell.
		refusal.NoReport(fmt.Sprintf("tmux start: finding the project for this directory: %v", err),
			"Continue; the console opens with a shell window").
			Command("tmux start").Print()
	}
	cwd, _ := os.Getwd()
	extra := []string{}
	if cwd != "" {
		extra = append(extra, "-c", cwd)
	}
	if _, _, err = console.StartSession(self, extra...); err != nil {
		startReport(fmt.Sprintf("tmux start: %v", err),
			"whether a tmux server that would not start is something they can clear").Exit(1)
	}
	fmt.Fprintf(out, "• console: started (%s), with a shell window\n", console.Socket)
}

// startTasks starts the task-session server unless it is running. Returns the
// placeholder pane of a session it created, "" when it reused one.
func startTasks(out io.Writer, tasks tmuxservers.Server, self string) (placeholder string) {
	if tasks.HasSession() {
		// init is gated on @server_uuid, so this is a no-op on a server that
		// already has one, and gives liveness an identity on one that does not.
		if err := tasks.Init(self); err != nil {
			refusal.NoReport(fmt.Sprintf("tmux start: %v", err),
				"Continue; this server's sessions may read unknown in session liveness").
				Command("tmux start").Print()
		}
		fmt.Fprintf(out, "• tasks: running, reused (%s, session %s)\n", tasks.Socket, tasks.Session)
		return ""
	}
	home, _ := os.UserHomeDir()
	extra := []string{}
	if home != "" {
		extra = append(extra, "-c", home)
	}
	pane, _, err := tasks.StartSession(self, extra...)
	if err != nil {
		startReport(fmt.Sprintf("tmux start: %v", err),
			"whether a tmux server that would not start is something they can clear").Exit(1)
	}
	fmt.Fprintf(out, "• tasks: started (%s, session %s)\n", tasks.Socket, tasks.Session)
	return pane
}

// restore recreates the recorded task windows that are not open, puts the task
// windows in recorded tab order, then resumes Claude in the recreated ones.
//
// A session start creates needs a first window, and that window is a plain
// shell. When restore brings task windows back into such a session, the
// placeholder is closed, so a restart leaves exactly the windows that were
// open.
func restore(out io.Writer, tasks tmuxservers.Server, recorded []int64, placeholder string) {
	open, ok := tasks.TaskWindows()
	if !ok {
		return
	}
	missing := missingIDs(recorded, open)
	if len(missing) == 0 {
		reorder(tasks, recorded)
		return
	}

	var windowIDs, names []string
	for _, id := range missing {
		name := fmt.Sprintf("E-%d", id)
		args := []string{"new-window", "-d", "-t", "=" + tasks.Session + ":", "-n", name}
		if dir, perr := monitor.TaskProjectPath(id); perr == nil && dir != "" {
			args = append(args, "-c", dir)
		}
		args = append(args, "-P", "-F", "#{window_id}")
		wid, werr := tasks.Out(args...)
		if werr != nil {
			refusal.NoReport(fmt.Sprintf("tmux start: recreating the window for %s: %v", name, werr),
				"Continue; resume it by hand with `endless session resume "+name+"`").
				Command("tmux start").Print()
			continue
		}
		windowIDs = append(windowIDs, wid)
		names = append(names, name)
	}
	if len(windowIDs) == 0 {
		reorder(tasks, recorded)
		return
	}
	if placeholder != "" {
		_ = tasks.Run("kill-pane", "-t", placeholder)
	}
	reorder(tasks, recorded)
	fmt.Fprintf(out, "• tasks: restored %d task window(s): %s\n", len(names), strings.Join(names, " "))
	var summary strings.Builder
	if err := resumewindowscmd.Restore(tasks.Socket, windowIDs, &summary); err != nil {
		refusal.NoReport(fmt.Sprintf("tmux start: resuming the restored windows: %v", err),
			"Continue; resume each with `endless session resume E-NNNN`").
			Command("tmux start").Print()
		return
	}
	for _, line := range strings.Split(strings.TrimRight(summary.String(), "\n"), "\n") {
		fmt.Fprintf(out, "  %s\n", line)
	}
}

// reorder swaps the session's task windows into recorded order (E-2385).
// new-window puts a recreated window at the next free index, which is not
// where it stood, and a window that stayed open has kept its own place; both
// are moved among the positions task windows hold, so a non-task window stays
// where it is. Cosmetic when it fails: every window is still there.
func reorder(tasks tmuxservers.Server, recorded []int64) {
	wins, ok := tasks.Windows()
	if !ok {
		return
	}
	for _, sw := range orderSwaps(wins, recorded) {
		if err := tasks.Run("swap-window", "-d", "-s", sw[0], "-t", sw[1]); err != nil {
			refusal.NoReport(fmt.Sprintf("tmux start: putting the task windows in order: %v", err),
				"Continue; every window was restored, some out of their recorded order").
				Command("tmux start").Print()
			return
		}
	}
}

// orderSwaps is the swap-window pairs (source, target window ids) that put
// the task windows of wins, a session's windows in tab order, into the order
// of recorded. Task windows the record does not name keep their relative
// order after the recorded ones; a second window for one task counts as one
// the record does not name. Pure.
func orderSwaps(wins []tmuxservers.Window, recorded []int64) (swaps [][2]string) {
	var slots []string // window ids at the positions task windows hold, in order
	first := map[int64]string{}
	for _, w := range wins {
		if w.TaskID <= 0 {
			continue
		}
		slots = append(slots, w.ID)
		if _, dup := first[w.TaskID]; !dup {
			first[w.TaskID] = w.ID
		}
	}
	placed := map[string]bool{}
	var want []string
	for _, id := range recorded {
		if wid, open := first[id]; open && !placed[wid] {
			placed[wid] = true
			want = append(want, wid)
		}
	}
	for _, wid := range slots {
		if !placed[wid] {
			want = append(want, wid)
		}
	}
	cur := slices.Clone(slots)
	for i := range cur {
		if cur[i] == want[i] {
			continue
		}
		j := slices.Index(cur, want[i])
		swaps = append(swaps, [2]string{want[i], cur[i]})
		cur[i], cur[j] = cur[j], cur[i]
	}
	return swaps
}

// missingIDs is the recorded ids with no open window, in recorded order. Pure.
func missingIDs(recorded, open []int64) []int64 {
	have := map[int64]bool{}
	for _, id := range open {
		have[id] = true
	}
	var out []int64
	for _, id := range recorded {
		if !have[id] {
			out = append(out, id)
			have[id] = true
		}
	}
	return out
}

// openTerminals opens a terminal window onto each server, or prints the attach
// commands when there is no way to open one.
func openTerminals(out io.Writer, servers tmuxservers.Servers) {
	launcher, ok := tmuxservers.ResolveLauncher(servers.Terminal)
	if ok {
		var failed []tmuxservers.Server
		for _, s := range []tmuxservers.Server{servers.Console, servers.Tasks} {
			if err := launcher.Open(s); err != nil {
				refusal.NoReport(fmt.Sprintf("tmux start: opening a terminal on the %s server: %v", s.Role, err),
					"Continue; attach with the command printed below").Command("tmux start").Print()
				failed = append(failed, s)
			}
		}
		if len(failed) == 0 {
			fmt.Fprintf(out, "• opened a %s window on each server\n", launcher.Name)
			return
		}
		printAttach(out, failed)
		return
	}
	fmt.Fprintln(out, "• no terminal to open: set tmux.terminal in ~/.config/endless/config.json "+
		"(e.g. \"ghostty -e {cmd}\"), or attach from two terminal windows:")
	printAttach(out, []tmuxservers.Server{servers.Console, servers.Tasks})
}

func printAttach(out io.Writer, servers []tmuxservers.Server) {
	for _, s := range servers {
		fmt.Fprintf(out, "      %s\n", s.AttachCommand())
	}
}

// startReport is the shape of every refusal that stops the start: the cause is
// a tmux install, a server, a directory or a config file, all the user's.
func startReport(summary, decision string) *refusal.Error {
	return refusal.Report(summary, decision).Command("tmux start")
}

// RunRecord implements `endless-go tmux record-windows`, which the
// task-session server's hooks run whenever a window is linked, unlinked or
// renamed. Silent on success: tmux discards a background job's output.
func RunRecord(args []string) {
	fs := refusal.NewFlags("tmux record-windows")
	socket := fs.String("socket", "", "the task-session server's socket path")
	session := fs.String("session", "", "the task-session server's session")
	if err := fs.Parse(args); err != nil {
		fs.ExitOnHelp(err)
		refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("tmux record-windows").Text(fs.Output()).Exit(2)
	}
	srv := tmuxservers.Server{Role: tmuxservers.RoleTasks, Socket: *socket, Session: *session}
	if srv.Socket == "" || srv.Session == "" {
		servers, err := tmuxservers.Resolve()
		if err != nil {
			refusal.Faultf("tmux record-windows: %v", err).Command("tmux record-windows").Exit(1)
		}
		srv = servers.Tasks
	}
	if _, err := srv.Record(); err != nil {
		refusal.Faultf("tmux record-windows: %v", err).Command("tmux record-windows").Exit(1)
	}
}

// RunTasksTarget implements `endless-go tmux tasks-target`: where a spawned
// window goes when the task-session server is running. Prints the socket path
// and the session, tab-separated, and exits 0; exits 1, printing nothing, when
// the server is not running. For the Python spawn and claim paths, which decide
// whether they need the caller to be inside tmux at all.
func RunTasksTarget(args []string) {
	if len(args) > 0 {
		refusal.NoReport("tmux tasks-target: takes no arguments", "Re-run with none").
			Command("tmux tasks-target").Exit(2)
	}
	tasks, ok := tmuxservers.RunningTasks()
	if !ok {
		os.Exit(1)
	}
	fmt.Println(tasks.Socket + "\t" + tasks.Session)
}
