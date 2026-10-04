// Package projectstatuscmd implements `endless-go project-status` and
// `endless-go project-window`: the read command behind the Python `endless
// project status` (snapshot) and `endless project monitor` (live) verbs, and the
// tmux launcher that gives the live one its dedicated two-pane session (E-1976).
//
// It is the project-scoped counterpart to internal/sessionstatuscmd. That view
// answers "what is next for the task I am on"; this one lists the project's
// open urgent, now and next tasks (E-2156). The two share their live-pane
// machinery (internal/liveview), their fault row (internal/faultrow) and their
// task-row vocabulary (internal/taskrow): a task reads the same in both.
//
// The Python verbs shell out here inheriting the terminal's stdout, so width and
// color are detected against the real tty.
package projectstatuscmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/term"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// fallbackCols is the width used when none can be detected (output not a tty, no
// --cols, no $COLUMNS). Matches sessionstatuscmd's.
const fallbackCols = 90

// monitorPctOfWindow is the share of the tmux window the monitor's pane starts
// at when `project monitor --tmux` builds the layout. After that the pane's
// height is the user's: the frame fits whatever pane it is in, and dragging
// the divider is how to see more rows or fewer (E-2156). Two thirds keeps the
// monitor legible while leaving a third for the shell the layout exists to
// provide.
const monitorPctOfWindow = 65

// Run dispatches both subcommands this package owns. One package, because the
// window IS the monitor's home — the layout exists to hold it, and
// splitting them would put the two halves of one feature in two places.
func Run(sub string, args []string) {
	switch sub {
	case "project-window":
		runWindow(args)
	default:
		runStatus(args)
	}
}

// options is one parsed invocation.
type options struct {
	project   string
	projectID int64
	monitor   bool
	later     bool
	sort      string
	cols      int
	rows      int
	asJSON    bool
}

// defaultPhases is what both views cover by default; laterPhases is what
// `project status --later` covers instead — the phase every other view leaves
// out.
var (
	defaultPhases = []string{"urgent", "now", "next"}
	laterPhases   = []string{"later"}
)

func runStatus(args []string) {
	fs := refusal.NewFlags("project-status")
	var o options
	fs.StringVar(&o.project, "project", "", "project name (default: the project enclosing the working directory)")
	fs.Int64Var(&o.projectID, "project-id", 0, "explicit project id (headless: bypasses name/cwd resolution and reads the resolved DB context instead of pinning main; intended for tests)")
	fs.BoolVar(&o.monitor, "monitor", false, "redraw every 2s until interrupted (Ctrl-C) — `project monitor`; the only mode that truncates")
	fs.BoolVar(&o.later, "later", false, "show ONLY `later` tasks, instead of urgent/now/next")
	fs.StringVar(&o.sort, "sort", string(sortUpdated), "row order within each list, newest first: updated or id")
	fs.IntVar(&o.cols, "cols", 0, "terminal width override (0 = auto-detect)")
	fs.IntVar(&o.rows, "rows", 0, "terminal height override (0 = auto-detect; the budget the monitor fits its frame into)")
	fs.BoolVar(&o.asJSON, "json", false, "emit the row set as JSON instead of the rendered frame")
	if err := fs.Parse(args); err != nil {
		// Text carries flag's own error line and the flag defaults it appends,
		// which is what stderr held before this was classified.
		refusal.NoReport(err.Error(), "Fix the flag and retry").
			Command("project-status").Text(fs.Output()).Exit(2)
	}
	if !validSortKey(o.sort) {
		refusal.NoReport(fmt.Sprintf("project-status: --sort must be updated or id, not %q", o.sort),
			"Pass --sort updated or --sort id and retry").
			Command("project-status").Exit(2)
	}

	projectID, name := resolveProject(o)
	phases, phrase := defaultPhases, "urgent, now or next"
	if o.later {
		phases, phrase = laterPhases, "later"
	}
	key := sortKey(o.sort)

	// --json is a DATA dump, not a view: every row, and it wins over --monitor,
	// whose output is a drawn frame.
	if o.asJSON {
		rows, err := monitor.ProjectStatusRows(projectID, phases)
		if err != nil {
			fail(err)
		}
		if err = renderJSON(os.Stdout, name, rows, key); err != nil {
			fail(err)
		}
		return
	}

	color := liveview.ColorEnabled()
	// A project whose path cannot be read renders in the default colors.
	dir, _ := monitor.ProjectPath(projectID)
	frame := frameSpec{projectID: projectID, name: name, phases: phases, phrase: phrase,
		sort: key, rows: o.rows, styles: listStylesFor(dir)}

	// --monitor only makes sense against an interactive terminal (the redraw uses
	// cursor-positioning escapes). When stdout is piped or captured, degrade to a
	// single frame so scripts and pipes don't hang on an endless loop — still the
	// monitor's frame, truncated to the height it was asked to fit.
	if o.monitor {
		frame.truncate = true
	}
	if o.monitor && isTTY() {
		liveview.Loop(liveview.LoopConfig{
			Render:       frame.fn(),
			ColsOverride: o.cols,
			FallbackCols: fallbackCols,
			Color:        color,
			// No Pane: `session monitor` fits its pane to its frame, but this
			// frame fits its pane instead — the third list grows into whatever
			// height the user gives it. Fitting the pane as well would undo
			// every resize the user made (E-2156).
			// The E-698 trigger. NOTE: this does NOT by itself make the project
			// monitor auto-spawn's on/off switch, which is how E-1815 describes it
			// — `session monitor` fires the same runner, so closing this window
			// leaves any other monitor firing due jobs. Making the auto-spawn
			// selector exclusive to this window is E-1814's own job (its job gates
			// itself); stating that here rather than shipping a comment that claims
			// a property the code does not have.
			FireJobs: true,
			// A query that has started failing is not something a 2s repaint can
			// recover from, so the loop ends and the process says so. fail() runs
			// AFTER liveview.Loop has restored the cursor.
			Fatal: fail,
		})
		return
	}

	if _, err := frame.fn()(
		os.Stdout, liveview.DetectCols(o.cols, fallbackCols), color,
	); err != nil {
		fail(err)
	}
}

// frameSpec is everything one frame needs that does not change between
// repaints.
type frameSpec struct {
	projectID int64
	name      string
	phases    []string
	phrase    string
	sort      sortKey
	// rows is the --rows override; 0 detects the pane height per frame.
	rows int
	// truncate is true for `project monitor`, the only view that cuts list 3.
	truncate bool
	styles   *listStyles
}

// fn binds one render into the liveview.Frame shape. `project status` and
// `project monitor` call the SAME closure; they differ only in whether list 3
// may be cut to the pane.
func (f frameSpec) fn() liveview.Frame {
	return func(w io.Writer, cols int, color bool) (int, error) {
		rows, err := monitor.ProjectStatusRows(f.projectID, f.phases)
		if err != nil {
			return 0, err
		}
		// The height budget is read PER FRAME, not per process: a monitor must
		// re-fit when its pane is resized, the same argument liveview.Loop
		// already makes for re-detecting the width.
		budget := f.rows
		if f.truncate && budget == 0 {
			budget = paneBudget()
		}
		return render(w, f.name, rows, frameOpts{
			budget:      budget,
			truncate:    f.truncate,
			cols:        cols,
			color:       color,
			sort:        f.sort,
			emptyPhrase: f.phrase,
			styles:      f.styles,
		}, faults.ProjectScope(f.projectID)), nil
	}
}

// resolveProject settles which project `project status` is about, and the DB it
// reads.
//
// Three ways in, in precedence order:
//
//  1. --project-id (headless). Skips the main-DB pin so the caller's resolved
//     context — whatever --db/--db-dir resolved —
//     is what gets read. This is the seam the verify harness drives, mirroring
//     sessionstatuscmd's --task.
//  2. --project NAME, against the main DB.
//  3. the project enclosing the working directory, against the main DB.
//
// Cases 2 and 3 pin the main database unless an explicit --db/--db-dir was given,
// for the same reason `session-status` does: this view reads sessions and tasks
// through a single-database join, sessions live in main regardless of cwd, and a
// worktree sandbox holds one project row and no tasks — so a sandbox read would
// render an empty frame from inside every worktree.
func resolveProject(o options) (int64, string) {
	if o.projectID > 0 {
		name := o.project
		if name == "" {
			if _, resolved, err := monitor.ProjectNameByID(o.projectID); err == nil {
				name = resolved
			} else {
				name = fmt.Sprintf("project %d", o.projectID)
			}
		}
		return o.projectID, name
	}

	if !monitor.HasExplicitDBContext() {
		monitor.PinMainDB()
	}

	if o.project != "" {
		id, name, err := monitor.ProjectByName(o.project)
		if err != nil {
			// Resolved in code rather than handed to the agent as a condition:
			// ErrNoProject says the NAME is not registered, which `endless
			// project list` answers and a second call fixes. Any other error is
			// the database failing to answer, and fail gives that to the user.
			if errors.Is(err, monitor.ErrNoProject) {
				refusal.NoReport(fmt.Sprintf("project-status: %v", err),
					"Check `endless project list` for the registered name and retry").
					Command("project-status").Exit(1)
			}
			fail(err)
		}
		return id, name
	}

	id, name, err := monitor.ProjectForCwd()
	if err != nil {
		if errors.Is(err, monitor.ErrNoProject) {
			// Genuinely undecidable here. This command can see that the working
			// directory is not in a project; it cannot see whether the user meant
			// to work on a project that IS registered — in which case naming it
			// is a retry away — or meant THIS directory to become one, which is
			// a choice about their repository that nobody else gets to make.
			refusal.ReportIf(
				"project-status: not inside a registered project. "+
					"Name one with --project <name>, or register this directory with `endless project init`.",
				"the user meant this directory itself to become an Endless project",
				"rerun with --project <name>, or from inside a directory that is already registered",
				"registering a directory as an Endless project is theirs to choose",
			).Command("project-status").Exit(1)
		}
		fail(err)
	}
	return id, name
}

func isTTY() bool { return liveview.IsTerminal(os.Stdout) }

// paneBudget is the lines a monitor frame may use: the height of the terminal
// it is drawing into — inside tmux, its own pane — less one. The one is the
// line the cursor sits on after the frame's last newline; a frame that filled
// every line would scroll the pane by one on each repaint, which is what left
// duplicated rows above the legend.
//
// Without a terminal to measure (stdout piped), it falls back to a share of the
// tmux window, so a captured monitor frame is still a plausible height.
func paneBudget() int {
	if _, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && h > 1 {
		return h - 1
	}
	return liveview.DetectRows(os.Getenv("TMUX_PANE"), monitorPctOfWindow, 0)
}

// fail ends the command on an error that reached it without a class of its own
// — a row query, a frame render, the JSON write, or the monitor loop giving up.
//
// An error that DID choose one keeps it: --project-id skips the main-DB pin, so
// inside a self-dev worktree err can be the E-1429 sandbox gate, which is a
// no-report "pass --db" and not a project-status failure at all. Everything
// else is the database or the renderer refusing under a read-only command —
// there is no other call this command could make instead, so the next move is
// the user's.
func fail(err error) {
	var classified *refusal.Error
	if !errors.As(err, &classified) {
		classified = refusal.Report(err.Error(),
			"whether a project-status read Endless could not complete is something they can clear")
	}
	classified.Command("project-status").
		Text(fmt.Sprintf("project-status: %v", err)).Exit(1)
}
