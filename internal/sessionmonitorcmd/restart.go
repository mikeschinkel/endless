package sessionmonitorcmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
	"github.com/mikeschinkel/endless/internal/upid"
)

// Run dispatches `endless-go session-monitor <verb>`.
func Run(args []string) {
	if len(args) == 0 {
		usage(os.Stderr)
		os.Exit(2)
	}
	switch args[0] {
	case "restart":
		runRestart(args[1:])
	case "-h", "--help", "help":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "endless-go session-monitor: unknown verb %q\n", args[0])
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "Usage: endless-go session-monitor restart [--tmux-session NAME | --all-tmux-sessions] [--dry-run]")
	fmt.Fprintln(w, "  restart  respawn every tagged session-monitor pane in place onto the installed binary")
}

// runRestart implements `endless-go session-monitor restart`, the Go half of
// `endless session monitor --restart`.
func runRestart(args []string) {
	fs := flag.NewFlagSet("session-monitor restart", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		session = fs.String("tmux-session", "", "restart the monitors in this tmux session")
		all     = fs.Bool("all-tmux-sessions", false, "restart the monitors in every tmux session")
		dryRun  = fs.Bool("dry-run", false, "list the panes that would be restarted, and change nothing")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "session-monitor restart: unexpected argument %q\n", fs.Arg(0))
		os.Exit(2)
	}
	sc, err := resolveScope(*session, *all, os.Getenv("TMUX_PANE"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "session-monitor restart: %v\n", err)
		os.Exit(2)
	}
	if err = restart(os.Stdout, sc, *dryRun); err != nil {
		fmt.Fprintf(os.Stderr, "session-monitor restart: %v\n", err)
		os.Exit(1)
	}
}

// scope is the set of tmux sessions a restart covers: every session, or the one
// target names. label is how the summary refers to it.
type scope struct {
	all    bool
	target string
	label  string
}

// resolveScope turns the flags into a scope. With neither flag the scope is
// the session holding pane — the caller's own pane, from $TMUX_PANE — and with
// no pane there is no current session to default to, so that is a usage error
// rather than a guess.
//
// A named session is targeted as `=NAME`, tmux's exact match. A bare name is a
// prefix match, so `--tmux-session act` would otherwise restart the monitors
// of a session called `active`.
func resolveScope(session string, all bool, pane string) (sc scope, err error) {
	switch {
	case all && session != "":
		err = fmt.Errorf("--tmux-session and --all-tmux-sessions are mutually exclusive")
	case all:
		sc = scope{all: true, label: "any tmux session"}
	case session != "":
		sc = scope{target: "=" + session, label: fmt.Sprintf("tmux session %q", session)}
	case pane == "":
		err = fmt.Errorf("not inside tmux: pass --tmux-session NAME or --all-tmux-sessions")
	default:
		// A pane id is a valid session target: tmux resolves it to the session
		// holding the pane, and reads it at call time rather than from $TMUX's
		// session field, which goes stale when a pane is moved.
		sc = scope{target: pane, label: "this tmux session"}
	}
	return sc, err
}

// paneFormat is the list-panes format restart parses: one tab-separated line
// per pane. The tag is last because it is the only field that can be empty.
const paneFormat = "#{pane_id}\t#{session_name}:#{window_index}.#{pane_index}\t#{pane_current_path}\t#{" + TagOption + "}"

// listPanesArgs builds the list-panes for sc: `-a` for every session, else
// `-s -t <target>` for every pane of one session.
func listPanesArgs(sc scope) []string {
	if sc.all {
		return []string{"list-panes", "-a", "-F", paneFormat}
	}
	return []string{"list-panes", "-s", "-t", sc.target, "-F", paneFormat}
}

// respawnArgs builds `tmux respawn-pane -k -t <pane> [-c <dir>] -- <cmd...>`.
// -k kills what the pane holds; -c keeps the directory the monitor was
// running in, so the pane comes back where it was in every respect.
func respawnArgs(pane, dir string, cmd []string) []string {
	args := []string{"respawn-pane", "-k", "-t", pane}
	if dir != "" {
		args = append(args, "-c", dir)
	}
	args = append(args, "--")
	return append(args, cmd...)
}

// taggedPane is one pane carrying the monitor tag.
type taggedPane struct {
	id    string // tmux pane id, %N
	where string // session:window.pane, for the summary
	dir   string // pane_current_path
	tag   string // the raw tag value
}

// parsePanes returns the tagged panes in list-panes output; untagged panes are
// not monitors and are dropped here.
//
// Each pane appears once. Grouped tmux sessions (`new-session -t`) share their
// windows, so `list-panes -a` reports a shared pane once per session in the
// group. Respawning it once per listing would kill the monitor the first
// respawn just started; the first listing wins.
func parsePanes(out string) (panes []taggedPane) {
	seen := map[string]bool{}
	for line := range strings.SplitSeq(out, "\n") {
		f := strings.SplitN(line, "\t", 4)
		if len(f) != 4 || f[3] == "" || seen[f[0]] {
			continue
		}
		seen[f[0]] = true
		panes = append(panes, taggedPane{id: f[0], where: f[1], dir: f[2], tag: f[3]})
	}
	return panes
}

// action is what restart does with one tagged pane.
type action int

const (
	actRestart action = iota // its monitor is running: respawn it
	actStale                 // its UPID names no running process: clear the tag, skip
)

// decide is the whole trust rule. A tag is left behind by a monitor that was
// killed outright, on a pane that now holds something else — usually the shell
// the monitor was typed at. Respawning kills whatever the pane holds, so a pane
// is restarted only while the process its UPID names is still running.
func decide(p taggedPane, alive func(upid.UPID) bool) (act action, why string) {
	u, err := upid.Parse(p.tag)
	switch {
	case err != nil:
		return actStale, fmt.Sprintf("unreadable tag %q", p.tag)
	case !alive(u):
		return actStale, fmt.Sprintf("stale tag: process %d is gone", u.PID)
	}
	return actRestart, fmt.Sprintf("monitor pid %d", u.PID)
}

// Seams, so the sequence restart drives is testable without a tmux server.
var (
	tmuxOut        = runTmuxOut
	processAlive   = upid.UPID.Alive
	monitorCommand = spawnlaunchcmd.MonitorCommand
)

// restart respawns every live monitor pane in sc and clears every stale tag,
// printing one line per tagged pane. With dryRun it prints what it would do
// and changes nothing.
//
// One pane failing to respawn does not stop the rest; the failures are
// reported per pane and summed into the returned error.
func restart(w io.Writer, sc scope, dryRun bool) (err error) {
	var out string
	var panes []taggedPane
	var failed int
	out, err = tmuxOut(listPanesArgs(sc)...)
	if err != nil {
		goto end
	}
	panes = parsePanes(out)
	if len(panes) == 0 {
		fmt.Fprintf(w, "no session monitor panes in %s\n", sc.label)
	}
	for _, p := range panes {
		act, why := decide(p, processAlive)
		switch {
		case act == actStale && dryRun:
			fmt.Fprintf(w, "would skip %s (%s): %s\n", p.id, p.where, why)
		case act == actStale:
			if _, e := tmuxOut(untagArgs(p.id)...); e != nil {
				fmt.Fprintf(w, "skipped %s (%s): %s; clearing the tag failed: %v\n", p.id, p.where, why, e)
				continue
			}
			fmt.Fprintf(w, "skipped %s (%s): %s; tag cleared\n", p.id, p.where, why)
		case dryRun:
			fmt.Fprintf(w, "would restart %s (%s): %s\n", p.id, p.where, why)
		default:
			if _, e := tmuxOut(respawnArgs(p.id, p.dir, monitorCommand())...); e != nil {
				fmt.Fprintf(w, "failed %s (%s): %v\n", p.id, p.where, e)
				failed++
				continue
			}
			fmt.Fprintf(w, "restarted %s (%s): was %s\n", p.id, p.where, why)
		}
	}
	if failed > 0 {
		err = fmt.Errorf("%d pane(s) failed to restart", failed)
	}
end:
	return err
}

// runTmuxOut execs one tmux command and returns its trimmed stdout, with tmux's
// stderr folded into the error so a per-pane failure reads as one line.
func runTmuxOut(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("tmux %s: %s", args[0], msg)
		}
		return "", fmt.Errorf("tmux %s: %w", args[0], err)
	}
	return strings.TrimSpace(string(out)), nil
}
