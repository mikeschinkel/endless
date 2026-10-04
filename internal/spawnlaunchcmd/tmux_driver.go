package spawnlaunchcmd

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
)

// tmux_driver.go isolates every tmux invocation behind pure argv builders plus
// one thin exec wrapper, so the future multiplexer-driver refactor (Herdr / a
// built-in multiplexer, relates E-1085) can add backends without touching the
// spec/argv/exec logic in spawn_window.go and spawn_launch.go.

// newWindowArgs builds
//
//	tmux new-window [-d] {-a|-b} -t <place.target> [-c <cwd>] -n <name> -P -F #{pane_id} -- <cmd...>
//
// detached adds `-d`, which creates the window without making it the current
// window of its session. The auto-spawn job (E-1814) passes it so a session
// nobody asked for never takes focus from the one the user is typing in, and a
// manual `task spawn --no-refocus` passes it for the same reason (E-2234); a
// plain manual `task spawn` omits it, because the person who asked is looking
// for the window.
//
// place is REQUIRED and names both the session the window is created in
// (E-2125) and where its tab lands within that session (E-2234). Without an
// explicit session tmux picks the "current" one, which off a command line means
// the most recently active one on the server — so a spawn asked for in one
// session materialized in whichever session the person happened to be looking
// at, with nothing on the window saying where it came from. See windowPlacement
// for why the position is always `-a`/`-b` against a window rather than tmux's
// "next free index".
//
// The `--` terminates tmux flag parsing so the window command and its args are
// passed through literally (no shell re-quoting of cmd elements). When cwd is
// empty the `-c` flag is omitted and the window inherits the caller's cwd.
//
// `-P -F #{pane_id}` makes new-window report the created window's only pane, so
// the layout builder anchors on that id instead of looking the window back up
// by name. A name lookup is the same unqualified-target bug one call later: two
// sessions can each hold a window called `E-1705`, and `-t E-1705` would answer
// with whichever the server considered current.
func newWindowArgs(place windowPlacement, cwd, windowName string, detached bool, cmd []string) []string {
	args := []string{"new-window"}
	if detached {
		args = append(args, "-d")
	}
	args = append(args, place.flag, "-t", place.target)
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	args = append(args, "-n", windowName, "-P", "-F", "#{pane_id}", "--")
	return append(args, cmd...)
}

// Placement is where a spawned window's tab lands among its session's windows
// (E-2234). Manual `task spawn` defaults to PlaceFirst; the auto-spawn job
// defaults to PlaceLast, configurable as auto_spawn.placement.
type Placement string

const (
	// PlaceFirst inserts before every existing window in the session.
	PlaceFirst Placement = "first"
	// PlaceLast appends after every existing window in the session.
	PlaceLast Placement = "last"
	// PlaceLeft inserts just before the session's active window.
	PlaceLeft Placement = "left"
	// PlaceRight inserts just after the session's active window.
	PlaceRight Placement = "right"
)

// Placements is every valid Placement, in the order they are documented.
var Placements = []Placement{PlaceFirst, PlaceLast, PlaceLeft, PlaceRight}

// ParsePlacement validates a placement name. Exported because the auto-spawn
// job validates auto_spawn.placement with it before it shells a spawn.
func ParsePlacement(s string) (Placement, error) {
	for _, p := range Placements {
		if string(p) == s {
			return p, nil
		}
	}
	return "", fmt.Errorf("placement %q is not one of %q, %q, %q or %q",
		s, PlaceFirst, PlaceLast, PlaceLeft, PlaceRight)
}

// relative reports whether the placement is anchored on the session's active
// window, which has to be asked of tmux before the argv can be built.
func (p Placement) relative() bool {
	return p == PlaceLeft || p == PlaceRight
}

// windowPlacement is new-window's position flag and the window it is relative
// to: `-b` inserts before that window, `-a` after it, and tmux shuffles the
// later windows up an index to make room.
type windowPlacement struct {
	flag   string
	target string
}

// placementFor turns a Placement into new-window's position for session sid (a
// `$N` id). activeWindow is the session's active window id (`@N`), needed only
// for PlaceLeft and PlaceRight.
//
// Every placement is relative to a real window, never `<session>:` alone. That
// form means "next free index", which is neither first nor reliably last: with
// windows 0 and 2 it lands at 1, in the gap. `{start}` and `{end}` are tmux's
// lowest- and highest-numbered windows of the session, so `-b {start}` and
// `-a {end}` are the true ends whatever holes the numbering has.
func placementFor(p Placement, sid, activeWindow string) windowPlacement {
	sess := sessionTarget(sid)
	switch p {
	case PlaceLast:
		return windowPlacement{"-a", sess + "{end}"}
	case PlaceLeft:
		return windowPlacement{"-b", activeWindow}
	case PlaceRight:
		return windowPlacement{"-a", activeWindow}
	default:
		return windowPlacement{"-b", sess + "{start}"}
	}
}

// activeWindowArgs builds `tmux display-message -p -t <session>: #{window_id}`,
// which resolves a session to the id of its active window — what PlaceLeft and
// PlaceRight are relative to.
func activeWindowArgs(sid string) []string {
	return []string{"display-message", "-p", "-t", sessionTarget(sid), "#{window_id}"}
}

// hasSessionArgs builds `tmux has-session -t =<name>`. The `=` makes tmux match
// the name exactly: a bare name also matches as a prefix and then as a glob, so
// `--tmux-session end` would quietly land in a session called `endless`.
func hasSessionArgs(name string) []string {
	return []string{"has-session", "-t", "=" + name}
}

// namedSessionIDArgs builds `tmux display-message -p -t =<name>: #{session_id}`,
// which resolves an exactly-named session to its `$N` id.
func namedSessionIDArgs(name string) []string {
	return []string{"display-message", "-p", "-t", "=" + name + ":", "#{session_id}"}
}

// sessionIDArgs builds `tmux display-message -p -t <pane> #{session_id}`, which
// resolves a pane to the id of the session holding it.
func sessionIDArgs(pane string) []string {
	return []string{"display-message", "-p", "-t", pane, "#{session_id}"}
}

// sessionTarget renders a session id (`$3`) as a target prefix: the id
// suffixed with `:`, so tmux reads it as a session rather than as a window
// name, and a window token such as `{start}` can follow. An id already carrying
// the suffix is kept.
func sessionTarget(id string) string {
	if strings.HasSuffix(id, ":") {
		return id
	}
	return id + ":"
}

// spawnerSession returns the session that owns the pane this process is running
// in, as a session id (`$3`).
//
// Session ids are used rather than names because a name can be changed or
// duplicated across a rename while a `$N` id is fixed for the session's life.
// The id is read from the pane rather than from $TMUX's third field, which
// records the session a client was attached to when the process started and
// goes stale the moment the pane is moved (`tmux move-window`, `break-pane`).
//
// An unresolvable target is an error, never a silent fall back to an untargeted
// new-window: landing in an unknown session is the defect, so a spawn that
// cannot say where it belongs refuses instead of guessing.
//
// Every failure here is carried as a classified refusal rather than a bare
// error, because the site that PRINTS them (runSpawnWindow) handles any error
// generically and would otherwise have to re-derive a class it cannot see. All
// three are the user's to decide: an agent cannot put itself inside a tmux
// pane, and it cannot make a tmux server answer a pane query it just refused.
func spawnerSession() (string, error) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return "", refusal.Report(
			"cannot tell which tmux session to open the window in: "+
				"$TMUX_PANE is unset. Run this from inside a tmux pane",
			"whether to start or attach tmux and rerun from a pane")
	}
	id, err := tmuxRunOut(sessionIDArgs(pane)...)
	if err != nil {
		return "", refusal.Report(
			fmt.Sprintf("resolve session of pane %s: %v", pane, err),
			"whether tmux failing to map a live pane to its session is something they can clear").
			Cause(err)
	}
	if id == "" {
		return "", refusal.Report(
			fmt.Sprintf("pane %s reported no session id", pane),
			"whether tmux answering a pane query with nothing is something they can clear")
	}
	return id, nil
}

// setOptionArgs builds `tmux set-option -w -t <target> <key> <value>` for one
// window option.
func setOptionArgs(target, key, value string) []string {
	return []string{"set-option", "-w", "-t", target, key, value}
}

// splitWindowArgs builds
//
//	tmux split-window {-h|-v} [-b] -t <target> [-c <cwd>] [-l <length>] \
//	     -P -F #{pane_id} [-- <cmd...>]
//
// horizontal picks a left/right split (-h) over a top/bottom one (-v); before
// (-b) puts the NEW pane above/left of target instead of below/right. length is
// the new pane's size — columns for -h, rows for -v — and is omitted when <= 0,
// which leaves tmux's even 50/50 split. An empty cmd omits the `--` entirely so
// the pane runs tmux's default command (the user's interactive $SHELL); a
// non-empty cmd is passed through literally after `--`, no shell re-quoting.
//
// `-P -F #{pane_id}` is always present so the caller reads back the created
// pane's id. Ids, not indexes: pane indexes depend on the user's pane-base-index
// and shift as panes are added, so index targeting would silently address the
// wrong pane on a 1-based configuration.
func splitWindowArgs(target string, horizontal, before bool, cwd string, length int, cmd []string) []string {
	args := []string{"split-window"}
	if horizontal {
		args = append(args, "-h")
	} else {
		args = append(args, "-v")
	}
	if before {
		args = append(args, "-b")
	}
	args = append(args, "-t", target)
	if cwd != "" {
		args = append(args, "-c", cwd)
	}
	if length > 0 {
		args = append(args, "-l", strconv.Itoa(length))
	}
	args = append(args, "-P", "-F", "#{pane_id}")
	if len(cmd) > 0 {
		args = append(args, "--")
		args = append(args, cmd...)
	}
	return args
}

// selectPaneArgs builds `tmux select-pane -t <target>` — used to hand focus back
// to the claude pane once the layout is assembled.
func selectPaneArgs(target string) []string {
	return []string{"select-pane", "-t", target}
}

// windowOptionCommands returns the ordered set-option arg lists that publish the
// @endless_* window options SessionStart reads to bind the spawned session to
// its task. Kept pure so tests can assert the exact command list.
//
// @endless_auto_spawned is set only on an auto-spawned window (E-1814).
// SessionStart reads it to mark the session it binds there as auto-spawned,
// and only when the window's @endless_task_id agrees with the task the session
// actually binds — see hookcmd.markAutoSpawned for why agreement is required.
func windowOptionCommands(target string, spec LaunchSpec) [][]string {
	cmds := [][]string{
		setOptionArgs(target, "@endless_spawned_by", spec.SpawnedBy),
		setOptionArgs(target, "@endless_task_id", spec.TaskID),
		setOptionArgs(target, "@endless_project_id", spec.ProjectID),
	}
	if spec.AutoSpawned {
		cmds = append(cmds, setOptionArgs(target, AutoSpawnedOption, "1"))
	}
	return cmds
}

// AutoSpawnedOption is the window option marking a window the auto-spawn job
// opened. Exported because SessionStart (internal/hookcmd) reads it back.
const AutoSpawnedOption = "@endless_auto_spawned"

// tmuxRun / tmuxRunOut are the indirection the layout builder calls through.
// The argv builders above are pure and tested directly; the builder that
// SEQUENCES them is not, and since E-2106 three verbs share it — spawn, both
// resume paths, and a shell `task claim` — so the order and the pane it threads
// between splits are worth pinning without a live tmux server.
var (
	tmuxRun    = runTmux
	tmuxRunOut = runTmuxOut
)

// runTmux execs one tmux command, surfacing its stderr on failure.
func runTmux(args ...string) error {
	cmd := exec.Command("tmux", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = refusal.Passthrough()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux %v: %w", args, err)
	}
	return nil
}

// runTmuxOut execs one tmux command and returns its trimmed stdout, for the
// queries whose ANSWER is the point (pane ids). stderr still goes to ours so a
// failure is visible; stdout is captured rather than inherited so tmux's `-P`
// pane-id echo never leaks into the spawner's terminal.
func runTmuxOut(args ...string) (string, error) {
	cmd := exec.Command("tmux", args...)
	cmd.Stderr = refusal.Passthrough()
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("tmux %v: %w", args, err)
	}
	return strings.TrimSpace(string(out)), nil
}
