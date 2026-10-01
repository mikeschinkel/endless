package sessionmonitorcmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
	"github.com/mikeschinkel/endless/internal/spawnlaunchcmd"
	"github.com/mikeschinkel/endless/internal/upid"
)

// Run dispatches `endless-go session-monitor <verb>`.
//
// Nothing in this file has a row in the refusal inventory — the verb landed
// after that audit — so every class below comes from the one question: can the
// agent continue without asking the user? For the dispatch refusals the answer
// is yes and the reason is the same both times. This subcommand has exactly one
// verb, the Python CLI always passes it, and a verb that is absent or misspelled
// is therefore a hand-run invocation or a skew between the two halves; the retry
// is `restart`, and nothing about the user's tmux has been touched.
func Run(args []string) {
	if len(args) == 0 {
		// Today this prints the usage block with nothing above it, so Text pins
		// what a person reads and the summary exists only to give the agent's
		// verdict a line to carry.
		refusal.NoReport(
			"endless-go session-monitor: no verb given",
			"Re-run with `restart`",
		).Command("session-monitor").Text(usageText()).Exit(2)
	}
	switch args[0] {
	case "restart":
		runRestart(args[1:])
	case "-h", "--help", "help":
		fmt.Fprint(os.Stdout, usageText())
	default:
		refusal.NoReport(
			fmt.Sprintf("endless-go session-monitor: unknown verb %q", args[0]),
			"Re-run with `restart`",
		).Command("session-monitor").Detail(usageText()).Exit(2)
	}
}

// usageText is the usage block, returned rather than written: a person asking
// for help reads it on stdout, while a refusal that carries it hands it to Text
// or Detail so the classified rendering owns the write.
func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go session-monitor restart [--tmux-session NAME | --all-tmux-sessions] [--dry-run]",
		"  restart  respawn every tagged session-monitor pane in place onto the installed binary",
	}, "\n") + "\n"
}

// runRestart implements `endless-go session-monitor restart`, the Go half of
// `endless session monitor --restart`.
func runRestart(args []string) {
	fs := refusal.NewFlags("session-monitor restart")
	var (
		session = fs.String("tmux-session", "", "restart the monitors in this tmux session")
		all     = fs.Bool("all-tmux-sessions", false, "restart the monitors in every tmux session")
		dryRun  = fs.Bool("dry-run", false, "list the panes that would be restarted, and change nothing")
	)
	if err := fs.Parse(args); err != nil {
		// This set was already ContinueOnError, so flag printed its own text and
		// the SITE chose the exit — help and a bad flag alike left with 2. Only
		// the class is added here: fs.Output() is flag's error line plus its
		// usage block, replayed verbatim, and the status stays the one this verb
		// has always returned. ExitOnHelp is deliberately not used; it would
		// turn `-h` into an exit 0, which is a behavior change this task has no
		// mandate to make.
		if errors.Is(err, flag.ErrHelp) {
			refusal.Info(fs.Output()).Exit(2)
		}
		refusal.NoReport(err.Error(), "Correct the flag and retry").
			Command("session-monitor restart").Text(fs.Output()).Exit(2)
	}
	if fs.NArg() > 0 {
		// A positional where this verb takes only flags; the scope is named with
		// --tmux-session, never as a bare word.
		refusal.NoReport(
			fmt.Sprintf("session-monitor restart: unexpected argument %q", fs.Arg(0)),
			"Name the scope with --tmux-session NAME or --all-tmux-sessions and retry",
		).Command("session-monitor restart").Exit(2)
	}
	sc, err := resolveScope(*session, *all, os.Getenv("TMUX_PANE"))
	if err != nil {
		// Both of resolveScope's refusals are answered by the same retry, which
		// is why they share one class: the two scope flags together, or neither
		// of them from outside tmux. In both cases the agent says which sessions
		// it means and runs the command again, and nothing has changed yet.
		refusal.NoReport(
			fmt.Sprintf("session-monitor restart: %v", err),
			"Re-run naming one scope: --tmux-session NAME or --all-tmux-sessions",
		).Command("session-monitor restart").Exit(2)
	}
	if err = restart(os.Stdout, sc, *dryRun); err != nil {
		// REPORT, for both failures that reach here. One is tmux refusing to
		// list the panes at all — no server, or a session that is not there —
		// and the other is one or more panes that would not respawn; the
		// per-pane lines are already on stdout above this. Either way the user's
		// monitors are not running and the next move is in their terminal, not
		// in another invocation of this command: an agent that retried would
		// kill whatever those panes now hold, a second time.
		refusal.Report(
			fmt.Sprintf("session-monitor restart: %v", err),
			"what to do about a session-monitor restart tmux would not complete",
		).Command("session-monitor restart").Cause(err).Exit(1)
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
