// Package resumewindowscmd implements `endless-go resume-windows` (E-2196):
// bring back every task window's Claude session after a tmux crash, in one
// command, with no window touched by hand.
//
// tmux-resurrect restores each window's name, layout and panes, but not the
// Claude session or the session monitor that ran in them. The window NAME is
// the task id shown on its tab, and it is authoritative. Per window:
//
//  1. Parse the task id from the name (an optional leading `*` is the user's
//     attention marker). Any other name: skip.
//  2. If any pane holds a live session: skip — it is already running Claude.
//  3. Keep one pane at a shell prompt, preferring the one already in the
//     task's worktree (it keeps its scrollback). None at a prompt: skip.
//  4. Kill every other pane.
//  5. Type the resume into the kept pane with send-keys, so it runs exactly
//     as if typed there: its output and any error print in that pane, and
//     quitting Claude returns to the shell.
//
// The typed line changes to the task's project root first. `session resume`
// changes into the worktree itself, and a pane restored INSIDE a self-dev
// worktree would otherwise have every endless command refused for want of an
// explicit --db — which `--db main` cannot answer in general, because it is
// refused in projects that are not self-dev. The main checkout needs neither.
//
// A window whose task cannot be resolved is still dispatched: resume prints
// the reason in that window, where the user will look, and the summary here
// names it too. Every dispatch is typed, not awaited, so the summary reports
// what was sent, never what came of it.
//
// The Python CLI (`endless session resume --tmux-session NAME` /
// `--all-tmux-sessions`) is a passthrough to this subcommand.
package resumewindowscmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// Run is the `endless-go resume-windows` entry point.
func Run(args []string) {
	os.Exit(run(args, os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("resume-windows", flag.ContinueOnError)
	fs.SetOutput(stderr)
	session := fs.String("tmux-session", "", "resume the windows of this tmux session")
	all := fs.Bool("all-tmux-sessions", false, "resume the windows of every tmux session")
	dryRun := fs.Bool("dry-run", false, "print what would be done, and do nothing")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "resume-windows: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	if (*session == "") == !*all {
		fmt.Fprintln(stderr, "resume-windows: give exactly one of --tmux-session NAME or --all-tmux-sessions")
		return 2
	}

	windows, err := listWindows()
	if err != nil {
		fmt.Fprintf(stderr, "resume-windows: %v\n", err)
		return 1
	}
	if *session != "" {
		windows = inSession(windows, *session)
		if len(windows) == 0 {
			fmt.Fprintf(stderr, "resume-windows: no tmux session named %q\n", *session)
			return 1
		}
	}

	self := os.Getenv("TMUX_PANE")
	var plans []plan
	for _, w := range windows {
		plans = append(plans, decide(gather(w, self)))
	}
	if !*dryRun {
		for i := range plans {
			if plans[i].Skip == "" {
				plans[i].DispatchErr = dispatch(plans[i])
			}
		}
	}
	report(stdout, plans, *dryRun)
	return 0
}

// ── window facts ─────────────────────────────────────────────────────────────

type pane struct {
	ID      string
	Command string // pane_current_command
	Path    string // pane_current_path
}

type window struct {
	ID      string // @N — unique per server; a linked window is listed once
	Session string // the first session tmux lists it under, for the summary
	// Sessions is every session the window belongs to. Grouped sessions
	// (`active` and the unattached twin a second client makes, `active-6`)
	// share all their windows, and a linked window sits in several sessions;
	// --tmux-session must find it under any of those names.
	Sessions []string
	Index    string
	Name     string
	Panes    []pane
}

func (w window) label() string {
	return fmt.Sprintf("%s:%s", w.Session, w.Index)
}

// facts is everything decide needs about one window, gathered up front so the
// decision itself is a pure function and is tested without tmux or a database.
type facts struct {
	Window      window
	TaskID      int64 // 0: the name is not a task id
	HoldsSelf   bool  // the window holding the pane this command runs in
	LiveSession int64 // a live session in one of its panes, else 0
	LiveErr     error
	Worktree    string // the task's worktree on disk, "" when there is none
	Resolved    bool   // resume-target found a resumable session for the task
	ResolveErr  error
	ProjectRoot string
}

// taskName matches a window named for a task: `E-NNNN`, optionally preceded by
// the user's attention marker `*`, with surrounding whitespace (` *E-2187`).
var taskName = regexp.MustCompile(`^\s*(?:\*\s*)?[Ee]-(\d+)\s*$`)

// parseTaskName returns the task id a window name carries, or false for any
// name that is not one.
func parseTaskName(name string) (int64, bool) {
	m := taskName.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

// gather reads the facts decide needs. The database is read only for a window
// named for a task that is not the caller's own; nothing here writes.
func gather(w window, selfPane string) facts {
	f := facts{Window: w}
	id, ok := parseTaskName(w.Name)
	if !ok {
		return f
	}
	f.TaskID = id
	for _, p := range w.Panes {
		if p.ID == selfPane {
			f.HoldsSelf = true
			return f
		}
	}
	ids := make([]string, len(w.Panes))
	for i, p := range w.Panes {
		ids[i] = p.ID
	}
	f.LiveSession, f.LiveErr = monitor.LiveSessionForPanes(ids)
	if f.LiveErr != nil || f.LiveSession != 0 {
		return f
	}

	target, err := monitor.ResolveResumeTarget(fmt.Sprintf("E-%d", id))
	if err != nil {
		f.ResolveErr = err
		f.ProjectRoot, _ = monitor.TaskProjectPath(id)
		return f
	}
	f.Resolved = true
	f.Worktree = target.WorktreePath
	f.ProjectRoot = target.ProjectPath
	if f.ProjectRoot == "" {
		f.ProjectRoot, _ = monitor.TaskProjectPath(id)
	}
	return f
}

// ── the decision ─────────────────────────────────────────────────────────────

type plan struct {
	Window      window
	TaskID      int64
	Skip        string   // why nothing is done; "" means dispatch
	Keep        string   // the pane the resume is typed into
	Kill        []string // every other pane in the window
	Command     string   // the line typed into Keep
	Warning     string   // a failure resume will report in the pane
	DispatchErr error
}

// decide turns one window's facts into what to do with it. Pure.
func decide(f facts) plan {
	p := plan{Window: f.Window, TaskID: f.TaskID}
	switch {
	case f.TaskID == 0:
		p.Skip = "not named for a task"
		return p
	case f.HoldsSelf:
		p.Skip = "this command is running in it"
		return p
	case f.LiveErr != nil:
		p.Skip = fmt.Sprintf("could not check for a live session: %v", f.LiveErr)
		return p
	case f.LiveSession != 0:
		p.Skip = fmt.Sprintf("already running Claude (ES-%d)", f.LiveSession)
		return p
	}
	keep, ok := choosePane(f.Window.Panes, f.Worktree)
	if !ok {
		p.Skip = "no pane is at a shell prompt"
		return p
	}
	p.Keep = keep.ID
	for _, q := range f.Window.Panes {
		if q.ID != keep.ID {
			p.Kill = append(p.Kill, q.ID)
		}
	}
	// A resolved task with no worktree on disk was landed and its worktree
	// reaped: --review rebuilds a detached, read-mostly tree without touching
	// the task's status. An UNresolved task gets no --review — resume fails on
	// the resolution before it would matter, and says why in the pane.
	review := f.Resolved && f.Worktree == ""
	p.Command = resumeCommand(f.TaskID, f.ProjectRoot, review)
	if f.ResolveErr != nil {
		p.Warning = f.ResolveErr.Error()
	}
	return p
}

// choosePane picks the pane the resume is typed into: one at a shell prompt,
// preferring one already inside the task's worktree (its scrollback is the
// task's), else the first at a prompt. False when no pane is at a prompt.
func choosePane(panes []pane, worktree string) (pane, bool) {
	var first *pane
	for i := range panes {
		p := &panes[i]
		if !monitor.IsShellCommand(p.Command) {
			continue
		}
		if worktree != "" && within(p.Path, worktree) {
			return *p, true
		}
		if first == nil {
			first = p
		}
	}
	if first == nil {
		return pane{}, false
	}
	return *first, true
}

// within reports whether path is dir or below it.
func within(path, dir string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, "../")
}

// resumeCommand is the line typed into the kept pane. `--rebind` because
// tmux-resurrect does not restore `@endless_*` window options, so a restored
// window's claim is stale or absent; resume rewrites it. `--no-sibling-panes`
// is not needed: the siblings are gone by the time this runs.
func resumeCommand(taskID int64, projectRoot string, review bool) string {
	cmd := fmt.Sprintf("endless session resume E-%d --rebind", taskID)
	if review {
		cmd += " --review"
	}
	if projectRoot == "" {
		return cmd
	}
	return "cd " + shellQuote(projectRoot) + " && " + cmd
}

// shellQuote single-quotes s for a POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// ── tmux ─────────────────────────────────────────────────────────────────────

// tmuxOut is the one seam through which this package talks to tmux, so the
// dispatch sequence is tested by recording argv rather than driving a server.
var tmuxOut = func(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

// listWindows reads every window on the server with its panes, in the order
// tmux lists them. A window in several sessions — grouped or linked — is kept
// once, remembering every session it is in: killing its panes twice would fail
// the second time, and it is one window.
func listWindows() ([]window, error) {
	wout, err := tmuxOut("list-windows", "-a", "-F",
		"#{window_id}\t#{session_name}\t#{window_index}\t#{window_name}")
	if err != nil {
		return nil, err
	}
	pout, err := tmuxOut("list-panes", "-a", "-F",
		"#{window_id}\t#{pane_id}\t#{pane_current_command}\t#{pane_current_path}")
	if err != nil {
		return nil, err
	}
	return parseWindows(wout, pout), nil
}

// parseWindows joins list-windows and list-panes output. Pure. The free-text
// field (window name, pane path) is last on each line so a tab inside it
// cannot shift the others.
func parseWindows(wout, pout string) []window {
	var windows []window
	index := map[string]int{}
	for _, line := range lines(wout) {
		f := strings.SplitN(line, "\t", 4)
		if len(f) < 4 {
			continue
		}
		if i, seen := index[f[0]]; seen {
			windows[i].Sessions = append(windows[i].Sessions, f[1])
			continue
		}
		index[f[0]] = len(windows)
		windows = append(windows, window{
			ID: f[0], Session: f[1], Sessions: []string{f[1]}, Index: f[2], Name: f[3],
		})
	}
	seen := map[string]bool{}
	for _, line := range lines(pout) {
		f := strings.SplitN(line, "\t", 4)
		if len(f) < 4 {
			continue
		}
		i, ok := index[f[0]]
		// list-panes -a repeats a linked window's panes once per session.
		if !ok || seen[f[1]] {
			continue
		}
		seen[f[1]] = true
		windows[i].Panes = append(windows[i].Panes, pane{ID: f[1], Command: f[2], Path: f[3]})
	}
	return windows
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimRight(l, "\r"); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func inSession(windows []window, name string) []window {
	var out []window
	for _, w := range windows {
		for _, s := range w.Sessions {
			if s == name {
				w.Session = name // report it under the name that was asked for
				out = append(out, w)
				break
			}
		}
	}
	return out
}

// dispatch clears the window down to the kept pane and types the resume into
// it. Nothing here selects a window or switches a client: the user stays
// where they ran the command.
func dispatch(p plan) error {
	for _, id := range p.Kill {
		if _, err := tmuxOut("kill-pane", "-t", id); err != nil {
			return err
		}
	}
	// A restored pane may sit in copy mode; leave it, or the keys are eaten as
	// copy-mode commands. Fails harmlessly when the pane is not in copy mode.
	_, _ = tmuxOut("send-keys", "-t", p.Keep, "-X", "cancel")
	// Clear any partial input at the prompt, then type the line literally.
	for _, args := range [][]string{
		{"send-keys", "-t", p.Keep, "C-u"},
		{"send-keys", "-t", p.Keep, "-l", p.Command},
		{"send-keys", "-t", p.Keep, "Enter"},
	} {
		if _, err := tmuxOut(args...); err != nil {
			return err
		}
	}
	return nil
}

// ── the summary ──────────────────────────────────────────────────────────────

func report(w io.Writer, plans []plan, dryRun bool) {
	var dispatched, failed, skipped int
	for _, p := range plans {
		name := strings.TrimSpace(p.Window.Name)
		switch {
		case p.Skip != "":
			skipped++
			fmt.Fprintf(w, "  - %-10s %-12s skipped: %s\n", name, p.Window.label(), p.Skip)
		case p.DispatchErr != nil:
			failed++
			fmt.Fprintf(w, "  ✗ %-10s %-12s %v\n", name, p.Window.label(), p.DispatchErr)
		default:
			dispatched++
			verb := "resuming in"
			if dryRun {
				verb = "would resume in"
			}
			line := fmt.Sprintf("  ✓ %-10s %-12s %s %s", name, p.Window.label(), verb, p.Keep)
			if len(p.Kill) > 0 {
				closeVerb := "closed"
				if dryRun {
					closeVerb = "closing"
				}
				line += fmt.Sprintf(" (%s %s)", closeVerb, strings.Join(p.Kill, " "))
			}
			fmt.Fprintln(w, line)
			if dryRun {
				fmt.Fprintf(w, "      %s\n", p.Command)
			}
			if p.Warning != "" {
				fmt.Fprintf(w, "      ! resume will fail there: %s\n", p.Warning)
			}
		}
	}
	if dryRun {
		fmt.Fprintf(w, "Dry run: %d would resume, %d skipped. Nothing was changed.\n", dispatched, skipped)
		return
	}
	fmt.Fprintf(w, "%d dispatched, %d skipped", dispatched, skipped)
	if failed > 0 {
		fmt.Fprintf(w, ", %d failed", failed)
	}
	fmt.Fprintln(w, ".")
	if dispatched > 0 {
		fmt.Fprintln(w, "Dispatched is not confirmed: a resume that fails prints why in its own window.")
	}
}
