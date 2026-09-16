// Package projectstatuscmd implements `endless-go project-status` and
// `endless-go project-window`: the read command behind the Python `endless
// project status` (snapshot) and `endless project monitor` (live) verbs, and the
// tmux launcher that gives the live one its dedicated two-pane session (E-1976).
//
// It is the project-scoped counterpart to internal/sessionstatuscmd. That view
// answers "what is next for the task I am on"; this one answers "what in this
// project is claiming a person's attention", across every concurrent session.
// The two share their live-pane machinery (internal/liveview) and their fault
// badge (internal/faultbadge) and nothing else — the questions are different, so
// the row sets, the ranks and the glyph vocabularies are too.
//
// The Python verbs shell out here inheriting the terminal's stdout, so width and
// color are detected against the real tty.
package projectstatuscmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// fallbackCols is the width used when none can be detected (output not a tty, no
// --cols, no $COLUMNS). Matches sessionstatuscmd's.
const fallbackCols = 90

// monitorPctOfWindow is the share of the tmux window the monitor claims, and it
// is
// deliberately smaller than liveview.PanePctOfWindow's 80.
//
// For `session monitor` that 80% is a safety net: its frame is a handful of rows
// and the cap almost never binds. The monitor is the opposite — fair-share
// allocation grows the frame to fill whatever budget it is given, so the cap
// binds on EVERY frame and whatever it leaves is exactly what the shell pane
// below gets, forever. Two thirds keeps the monitor comfortably legible (five
// groups still render several rows each on any normal terminal) while leaving a
// third for the pane the user actually types in — which is the pane the whole
// two-pane layout exists to provide.
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
	all       bool
	limit     int
	noLimit   bool
	cols      int
	rows      int
	asJSON    bool
}

func runStatus(args []string) {
	fs := flag.NewFlagSet("project-status", flag.ContinueOnError)
	var o options
	fs.StringVar(&o.project, "project", "", "project name (default: the project enclosing the working directory)")
	fs.Int64Var(&o.projectID, "project-id", 0, "explicit project id (headless: bypasses name/cwd resolution and reads the resolved DB context instead of pinning main; intended for tests)")
	fs.BoolVar(&o.monitor, "monitor", false, "live dashboard: redraw every 2s until interrupted (Ctrl-C)")
	fs.BoolVar(&o.all, "all", false, "include `ready` tasks — spawnable work, a claim on capacity rather than attention")
	// The default is duplicated in src/endless/project_status_cmd.py, which
	// resolves the cap itself so `--limit`/`--no-limit` behave identically to
	// every other Endless listing and then passes a RESOLVED number. So this
	// default only applies to a direct `endless-go project-status` invocation.
	// The two are asserted equal by .endless/tasks/e-1976/verify.sh.
	fs.IntVar(&o.limit, "limit", defaultGroupCap, "max rows PER GROUP")
	fs.BoolVar(&o.noLimit, "no-limit", false, "render every row in every group")
	fs.IntVar(&o.cols, "cols", 0, "terminal width override (0 = auto-detect)")
	fs.IntVar(&o.rows, "rows", 0, "terminal height override (0 = auto-detect; the budget the monitor fits its frame into)")
	fs.BoolVar(&o.asJSON, "json", false, "emit the row set as JSON instead of the rendered frame")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	// Mutually exclusive by design, not by precedence, matching rowcap.py: a cap
	// and the removal of the cap are contradictory requests, and silently
	// honoring one answers a question the caller did not ask.
	if o.noLimit && wasSet(fs, "limit") {
		fmt.Fprintln(os.Stderr, "project-status: --limit and --no-limit are mutually exclusive")
		os.Exit(2)
	}
	if !o.noLimit && o.limit < 1 {
		fmt.Fprintln(os.Stderr, "project-status: --limit must be at least 1; pass --no-limit to render every row")
		os.Exit(2)
	}

	projectID, name := resolveProject(o)
	groupCap := o.limit
	if o.noLimit {
		groupCap = 0
	}

	// --json is a DATA dump, not a view: it emits every row the query returned and
	// leaves the include/exclude decision to the consumer, so it is UNCAPPED (the
	// rule rowcap.py sets for machine formats — a consumer parsing a truncated
	// payload has no footer to read and no way to notice) and it wins over
	// --monitor, whose output is a drawn frame. --all still applies: that filters
	// the query.
	if o.asJSON {
		rows, err := monitor.ProjectStatusRows(projectID, o.all)
		if err != nil {
			fail(err)
		}
		if err = renderJSON(os.Stdout, name, rows, time.Now().UTC()); err != nil {
			fail(err)
		}
		return
	}

	color := liveview.ColorEnabled()

	// --monitor only makes sense against an interactive terminal (the redraw uses
	// cursor-positioning escapes). When stdout is piped or captured, degrade to a
	// single frame so scripts and pipes don't hang on an endless loop.
	if o.monitor && isTTY() {
		liveview.Loop(liveview.LoopConfig{
			Render:       frameFunc(projectID, name, o.all, groupCap, o.rows),
			ColsOverride: o.cols,
			FallbackCols: fallbackCols,
			Color:        color,
			Pane:         os.Getenv("TMUX_PANE"),
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

	if _, err := frameFunc(projectID, name, o.all, groupCap, o.rows)(
		os.Stdout, liveview.DetectCols(o.cols, fallbackCols), color,
	); err != nil {
		fail(err)
	}
}

// frameFunc binds one render into the liveview.Frame shape. The snapshot
// and the monitor call the SAME closure, which is what makes `project status`
// provably one frame of `project monitor` rather than a lookalike.
func frameFunc(projectID int64, name string, all bool, groupCap, rowBudget int) liveview.Frame {
	return func(w io.Writer, cols int, color bool) (int, error) {
		rows, err := monitor.ProjectStatusRows(projectID, all)
		if err != nil {
			return 0, err
		}
		// Both the clock and the height budget are read PER FRAME, not per
		// process. A monitor left open overnight must age its rows — freezing the
		// clock at startup is the bug that leaves a 10-hour-idle session reading
		// "2m" — and it must re-fit when the window is resized, which is the same
		// argument liveview.Loop already makes for re-detecting the width.
		budget := rowBudget
		if budget == 0 {
			budget = liveview.DetectRows(os.Getenv("TMUX_PANE"), monitorPctOfWindow, 0)
		}
		return render(w, name, rows, groupCap, budget, cols, color, time.Now().UTC(),
			faults.ProjectScope(projectID)), nil
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
			fail(err)
		}
		return id, name
	}

	id, name, err := monitor.ProjectForCwd()
	if err != nil {
		if errors.Is(err, monitor.ErrNoProject) {
			fmt.Fprintln(os.Stderr,
				"project-status: not inside a registered project. "+
					"Name one with --project <name>, or register this directory with `endless project init`.")
			os.Exit(1)
		}
		fail(err)
	}
	return id, name
}

// wasSet reports whether a flag was given on the command line, as opposed to
// holding its default. flag has no built-in answer and the mutual-exclusion
// check needs one: --limit carries a non-zero default, so its VALUE cannot
// distinguish "not passed" from "passed the default".
func wasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func isTTY() bool { return liveview.IsTerminal(os.Stdout) }

func fail(err error) {
	fmt.Fprintln(os.Stderr, "project-status:", err)
	os.Exit(1)
}
