// Package sessionstatuscmd implements `endless-go session-status`: the read
// command behind the Python `endless session status` (snapshot) and `endless
// session monitor` (live, looping) verbs (E-1465, renamed E-1688). It resolves
// the focal task for the current tmux window, gathers the cross-session
// what's-next rows via monitor.SessionStatusRows, and renders them as a compact,
// width-aware, single-spaced table. The --monitor flag turns the one-shot
// snapshot into the top-like live loop; both share this one renderer.
//
// The Python verbs shell out to this subcommand inheriting the terminal's
// stdout, so width and color are detected here against the real tty. Pin to the
// main DB happens in the endless-go dispatcher (sessions live in main).
package sessionstatuscmd

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/mikeschinkel/endless/internal/faultbadge"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessiontaskrelation"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// fallbackCols is used when the terminal width can't be detected (output not a
// tty, no --cols, no $COLUMNS). Matches the bash prototype's default.
const fallbackCols = 90

// action is the primary classification of a row, in sort-rank order. The icon
// and rank both derive from it. Parent and from (spawner) outrank the
// in-flight/do/plan/etc. statuses; the bash prototype's icon-glyph sort had a
// latent bug (it matched '⤴' but rendered '↑'), avoided here by ranking on the
// enum, not the glyph.
type action int

const (
	actThis action = iota
	actParent
	actFrom
	actDoing
	actDo
	// actReview: a `submitted` task — planned/spec-complete but awaiting the
	// user's approval, so NOT spawnable (the claim gate refuses it). It gets its
	// own ⚑ glyph and `review` label rather than folding into actDo (▶), whose
	// glyph reads as "ready to spawn". Ranked right after actDo so it reads
	// "here's what's spawnable, then here's what's one approval away". ⚑ (U+2691
	// BLACK FLAG) measures single-width (asserted in TestActionIcons) so it aligns
	// in the width-aware table like every other icon.
	actReview
	actPlan
	actVerify
	actOrphan
	// actLanded: the task's work has merged (E-1693). ⏚ (U+23DA EARTH GROUND,
	// "landed/grounded") measures single-width (verified with go-runewidth), so
	// it aligns in the width-aware table like every other icon.
	actLanded
	// actUnknown: a status classify() doesn't recognize — a should-never-happen
	// safety net (every real status is handled above), so its ⁇ appearing in the
	// legend flags an unhandled status slipping through. ⁇ (U+2047) also measures
	// single-width. Appended after the pre-existing members so sortRows' enum
	// ranking is unchanged.
	actUnknown
	// actDone: a terminal-status task (confirmed/assumed/declined/obsolete/
	// completed) whose work never landed — E-1871. Before this it fell through
	// classify()'s switch to actUnknown, so the most ordinary rows in the database
	// wore ⁇, the should-never-happen glyph, and drowned out its diagnostic value
	// (declined/obsolete never land, so they hit it ALWAYS). ⇥ (U+21E5 RIGHTWARDS
	// ARROW TO BAR) reads as a terminus and measures single-width (asserted in
	// TestActionIcons), so the fixed 13-col prefix stays aligned.
	//
	// ⏚ landed WINS over ⇥: classify() checks r.Landed before the status switch,
	// so a landed terminal task reads ⏚ and ⇥ marks only closed work that never
	// merged — the informative case. TestClassify pins that precedence.
	//
	// APPENDED, not inserted: enum order is both legend order and sortRows' rank,
	// so appending leaves every existing rank untouched (the rule E-1750 followed
	// for actUnknown). Closed rows therefore sort last under --all, below the ⁇
	// anomaly rows — an unhandled status deserves more prominence than a finished
	// task.
	actDone
	// actTriage: an `untriaged` task — filed, but nobody has looked at it yet
	// (E-1845). Deliberately NOT actPlan: "needs a plan" is a judgment already
	// made about the task, and the whole point of `untriaged` is that no such
	// judgment exists yet. Collapsing the two would erase the distinction the
	// status was added to draw. ◌ (U+25CC DOTTED CIRCLE) reads as "not yet a ○",
	// the unplanned glyph, and measures single-width (asserted in TestActionIcons)
	// so the fixed 13-col prefix stays aligned.
	//
	// APPENDED, not inserted, per the rule actUnknown and actDone followed: enum
	// order is both legend order and sortRows' rank, so appending leaves every
	// existing rank untouched. Sorting last is right on its merits too — an
	// untriaged task is the least actionable row in the view, and `untriaged` is
	// now the DEFAULT status, so these rows would otherwise crowd real work off
	// the top of every listing.
	actTriage
)

// actionMeta maps each action to its legend glyph and label, indexed by the
// action enum so enum order is legend order for free. buildLegend derives the
// dynamic header from this table; icon()/label() read it.
var actionMeta = [...]struct{ icon, label string }{
	actThis:    {"●", "this"},
	actParent:  {"↑", "parent"},
	actFrom:    {"↩", "from"},
	actDoing:   {"⟳", "doing"},
	actDo:      {"▶", "do"},
	actReview:  {"⚑", "review"},
	actPlan:    {"✎", "plan"},
	actVerify:  {"☑", "verify"},
	actOrphan:  {"◷", "orphan"},
	actLanded:  {"⏚", "landed"},
	actUnknown: {"⁇", "unknown"},
	actDone:    {"⇥", "closed"},
	actTriage:  {"◌", "triage"},
}

func (a action) icon() string  { return actionMeta[a].icon }
func (a action) label() string { return actionMeta[a].label }

// hiddenMode selects how the VIEWING session's hidden task rows (E-1914) are
// treated by one render. Hiding is per (session, task) and display-scoped: it
// changes nothing about the task itself, only whether this session's listing
// draws it.
type hiddenMode int

const (
	// hiddenOmit is the default: hidden rows are dropped, and a footer reports
	// how many. The footer is REQUIRED whenever the count is non-zero — a hidden
	// task must never disappear without a trace.
	hiddenOmit hiddenMode = iota
	// hiddenShow (--show-hidden) renders everything, hidden rows marked with ⊘.
	hiddenShow
	// hiddenOnly (--only-hidden) renders the hidden set alone. This is the
	// discovery path for unhiding without already knowing the ids.
	hiddenOnly
)

// hiddenGlyph marks a hidden row under --show-hidden / --only-hidden. ⊘ (U+2298
// CIRCLED DIVISION SLASH) reads as "suppressed" and measures single-width
// (asserted in TestHiddenGlyphWidth), so the row prefix stays aligned. It occupies
// its own conditional column — present only when a rendered row is hidden — the
// same width-on-demand idiom blockField uses.
const hiddenGlyph = "⊘"

// hiddenFooter is the always-printed trace for suppressed rows in the default
// mode. It names the flag that reveals them so the listing is self-documenting.
func hiddenFooter(n int) string {
	return fmt.Sprintf("… %d hidden (--show-hidden)", n)
}

// The redraw cadence and the pane self-sizing rule moved to internal/liveview
// in E-1976, when `project status` became a second view needing both.
// These aliases keep the names this package's tests were written against, and
// keep the two dashboards provably sharing one set of numbers.
const (
	monitorInterval        = liveview.Interval
	monitorPaneSlack       = liveview.PaneSlack
	monitorPaneMinHeight   = liveview.PaneMinHeight
	monitorPaneEmptyHeight = liveview.PaneEmptyHeight
	monitorPanePctOfWindow = liveview.PanePctOfWindow
)

// no-task hints are shown when no focal task resolves. They mirror the tmux
// status line's PaneStatusKind hints (internal/tmuxcmd/status_line.go) so the bar
// and this view agree about "nothing here" instead of the list inventing an
// unrelated task (E-1698). Leading spaces align with the (formerly placeholder)
// list body; unlike the bar's versions these are plain text, not tmux #[...]
// format strings.
const (
	hintClaimBind = "  no task — claim or bind one:  endless task claim <id>  /  endless task bind <id>"
	hintNoSession = "  no Endless session — register it:  endless setup claude-hook"
)

// noTaskHintFor maps the resolved PaneStatusKind to the message the list prints
// when no focal task resolves. PaneStatusClaudeNoSession → register-session (the
// pane runs Claude but has no session row); every other no-task kind — session
// present but unclaimed, or no Endless context at all (including the non-tmux
// case) — → claim/bind.
func noTaskHintFor(kind monitor.PaneStatusKind) string {
	if kind == monitor.PaneStatusClaudeNoSession {
		return hintNoSession
	}
	return hintClaimBind
}

func Run(args []string) {
	fs := flag.NewFlagSet("session-status", flag.ContinueOnError)
	all := fs.Bool("all", false, "include done-work (terminal-status) rows")
	monitorMode := fs.Bool("monitor", false, "live dashboard: redraw every 2s until interrupted (Ctrl-C)")
	tree := fs.Bool("tree", false, "render do/plan tasks as an IDs-only implementation-order tree")
	cols := fs.Int("cols", 0, "terminal width override (0 = auto-detect)")
	taskFlag := fs.Int64("task", 0, "explicit task id (headless: bypasses tmux/session resolution and reads the resolved DB context — the self-detected sandbox or --config-dir — instead of pinning the main DB; intended for tests)")
	fromSession := fs.Int64("from-session", 0, "explicit spawning session id paired with --task (headless: drives the ↩ from row + --tree spawner annotation without tmux resolution; intended for tests)")
	sessionFlag := fs.Int64("session", 0, "explicit viewing session id (headless: bypasses tmux resolution; alone it lists that session's surfaced/revisited rows — the no-goal view — and alongside --task it names the session whose hides apply; intended for tests)")
	showHidden := fs.Bool("show-hidden", false, "render this session's hidden task rows too, marked "+hiddenGlyph)
	onlyHidden := fs.Bool("only-hidden", false, "render ONLY this session's hidden task rows (the discovery path for unhiding)")
	asJSON := fs.Bool("json", false, "emit the row set as JSON instead of the table; every row carries its hidden state")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	// Mutually exclusive by design, not by precedence: "show everything" and
	// "show only the hidden subset" are contradictory requests, and silently
	// picking one would hide the other's rows without saying so.
	if *showHidden && *onlyHidden {
		fmt.Fprintln(os.Stderr, "session-status: --show-hidden and --only-hidden are mutually exclusive")
		os.Exit(2)
	}
	hm := hiddenOmit
	switch {
	case *onlyHidden:
		hm = hiddenOnly
	case *showHidden:
		hm = hiddenShow
	}

	// nextAnchor produces the ids this view is pinned to (see anchor). The two
	// headless branches name their focal directly, so their anchor is a constant
	// — there is nothing to wait for. The normal path resolves it from the
	// session's process handle, and the live monitor keeps calling this until a
	// focal task appears (E-1892).
	var nextAnchor func() (anchor, error)
	if *taskFlag > 0 {
		// Headless mode (E-1685 verify harness): the caller names the focal task
		// directly, so there is no live tmux pane / session to resolve — and no
		// reason to force the main DB. Skip PinMainDB so DB() honors whatever
		// context was already resolved in main.go (the self-detected per-worktree
		// sandbox, or an explicit --config-dir), which is what lets the verify
		// script exercise the dependents row-set against a seeded sandbox DB.
		// --from-session supplies the spawning session id the live path would read
		// from @endless_spawned_by, so the ↩ from row stays testable headless.
		//
		// --session, when paired with --task, names the VIEWING session — the one
		// whose per-session hides apply (E-1914). It does not change the row set
		// here: focal != 0 keeps gatherRows on the focal-anchored path.
		fixed := anchor{
			focal:           *taskFlag,
			parentSession:   *fromSession,
			emittingSession: *sessionFlag,
			hint:            hintClaimBind,
		}
		nextAnchor = func() (anchor, error) { return fixed, nil }
	} else if *sessionFlag > 0 {
		// Headless no-goal mode (E-1802 verify harness): the caller names the
		// emitting session directly (no live pane to resolve it from), so the
		// no-goal surfaced/revisited view is exercised against the seeded sandbox
		// DB. Same PinMainDB skip rationale as the --task branch.
		fixed := anchor{emittingSession: *sessionFlag, hint: hintClaimBind}
		nextAnchor = func() (anchor, error) { return fixed, nil }
	} else {
		// Normal path: session/pane state lives in the main DB regardless of cwd
		// (the hook pins its writes there), so pin main before resolving.
		//
		// This view is the ONE surface whose data is machine-scoped rather than
		// project-scoped, so it pins main even inside a self_dev worktree.
		//
		// E-698 briefly skipped the pin in a worktree, reasoning that every other
		// command resolves the sandbox there and one rule beats a per-command
		// exception. That broke the view outright: sessions and tasks are read by a
		// single-database JOIN (monitor.queryTaskForPanes:
		// `FROM sessions s JOIN live_tasks t ON t.id = s.task_id`), so pane
		// resolution IS a task read and cannot be split across two databases. Worse,
		// sandbox task ids are a separate universe — sandboxcmd.seedFromWorktree
		// copies one project row and one session row and NO tasks — so a task id
		// resolved from main means nothing there. The worktree monitor rendered
		// "no task" for every pane.
		//
		// The guard that skip was protecting (candidate job code writing to the main
		// database) belongs on the trigger, not on the DB context: jobs.RunDue
		// suppresses itself when it detects a self_dev worktree pinned to a real DB.
		// That keeps the protection without costing a working dashboard.
		//
		// An explicit --config-dir still wins, matching main.go's hook/tmux
		// pattern (E-1429: a per-invocation flag is trustworthy; the env-driven pin
		// is the fallback). That preserves the seam the verify harnesses drive.
		if !monitor.HasExplicitDBContext() {
			monitor.PinMainDB()
		}
		// process is the session's process handle — today a tmux pane id. The
		// monitor.* lookups below pair it with the current tmux server's uuid to
		// reach a `processes` row (E-1898); a bare pane id is not an identity,
		// since the next server reissues it. The tmux-shaped name stays confined
		// to the helpers that genuinely take a pane (fitPaneToFrame and those
		// lookups).
		process := os.Getenv("TMUX_PANE")
		nextAnchor = func() (anchor, error) { return resolveAnchor(process) }
	}

	// --json is a DATA dump, not a view: it emits every row the query returned,
	// each carrying its own hidden state, and leaves the include/exclude decision
	// to the consumer. It therefore ignores --show-hidden/--only-hidden (which
	// shape a rendering, not a row set) and wins over --tree and --monitor, whose
	// output is a drawn frame. --all still applies — that filters the QUERY.
	if *asJSON {
		a, err := nextAnchor()
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		if err := renderJSON(os.Stdout, a, *all); err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		return
	}

	// --tree is an IDs-only structural view: a single frame, no legend, no monitor
	// loop. It always considers the full do/plan set, so --all/--cols don't apply.
	// --tree wins over --monitor (the live loop only drives the table view), the
	// same way it short-circuited the prototype's watch loop.
	//
	// Per-session hiding does not apply here either, and that is deliberate: the
	// tree is the implementation-order DAG, not a listing. Dropping a node from it
	// because someone found the row noisy would silently misstate what has to
	// happen before what — a structural claim, not a display preference.
	if *tree {
		a, err := nextAnchor()
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		rows, err := monitor.SessionStatusRows(a.focal, a.parentSession, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		if err := renderTree(os.Stdout, rows, a.focal, a.hint); err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		return
	}

	color := colorEnabled()

	// --monitor only makes sense against an interactive terminal (the redraw uses
	// cursor-positioning escapes). When stdout is piped/captured, degrade to a
	// single frame so scripts and pipes don't hang on an endless loop.
	//
	// The loop resolves its own anchor on the first tick rather than being handed
	// one here (E-1892): a monitor launched by `task spawn` starts milliseconds
	// before its session registers, so a resolution failure — or an empty result —
	// at this point must not be final.
	if *monitorMode && term.IsTerminal(int(os.Stdout.Fd())) {
		monitorLoop(newAnchorTracker(nextAnchor), *all, *cols, color, hm)
		return
	}

	// One-shot render: a single frame has no later tick to recover in, so a
	// resolution error is fatal here exactly as it always has been.
	a, err := nextAnchor()
	if err != nil {
		fmt.Fprintln(os.Stderr, "session-status:", err)
		os.Exit(1)
	}
	// The row count is the live monitor's pane-fit input (E-1851); a one-shot
	// render has no pane to fit, so it is discarded here.
	if _, err := renderSnapshot(os.Stdout, a, *all, detectCols(*cols), color, hm); err != nil {
		fmt.Fprintln(os.Stderr, "session-status:", err)
		os.Exit(1)
	}
}

// anchor is the set of ids the view pins itself to, resolved as ONE unit. The
// three ids are read from the same not-yet-settled state and share a single
// race, which is why they move together (E-1892):
//
//   - focal — the window's claimed task, from the session row the hook writes.
//   - parentSession — from the @endless_spawned_by window option, which
//     spawn-launch writes immediately before its syscall.Exec, i.e. the same
//     instant the session row does not yet exist.
//   - emittingSession — this pane's OWN session, wearing two hats. As the no-goal
//     anchor (E-1802) it is consulted only while focal == 0, so freezing it at a
//     stale 0 would leave that view permanently empty for exactly the sessions it
//     exists to serve. As the VIEWER (E-1914) it is consulted on every path — it
//     is the session whose per-session hides this render honors.
//
// hint is the message rendered when no focal task resolves, mirroring the tmux
// status line's PaneStatusKind so the two surfaces agree (E-1698).
type anchor struct {
	focal           int64
	parentSession   int64
	emittingSession int64
	hint            string
}

// resolveAnchor reads the whole anchor for one process handle through the same
// pane-scoped path the tmux status line uses. Seamed as a package var so tests
// can drive a scripted sequence of results, mirroring the worktreeAnomalies seam
// in this same file.
//
// The zero anchor returned alongside an error is never rendered: the one-shot
// callers exit, and anchorTracker keeps its previous anchor and retries.
var resolveAnchor = func(process string) (anchor, error) {
	focal, kind, err := monitor.ResolveSessionStatusFocal(process)
	if err != nil {
		return anchor{}, err
	}
	a := anchor{
		focal:         focal,
		parentSession: monitor.ResolveSessionStatusParentSession(process),
		hint:          noTaskHintFor(kind),
	}
	// The viewing session is resolved on EVERY path now (E-1914), not only the
	// no-goal one, because per-session task hiding is scoped to whoever is
	// looking: the focal-anchored view needs the viewer id to know which hides
	// apply. Its OTHER job — anchoring the no-goal view (E-1802) on this pane's
	// own session so its surfaced/revisited work is still listed rather than
	// hidden behind the claim/bind hint — is unchanged.
	//
	// The error is fatal only in the no-goal case, where this id IS the anchor and
	// a failure means there is nothing to render. With a focal task the view stands
	// on its own, so a lookup failure degrades to "no viewer" — which shows every
	// row. That is the right failure mode: a listing that cannot identify its
	// viewer must never suppress rows on some other session's behalf.
	viewer, verr := monitor.ResolveSessionStatusSession(process)
	if verr != nil && a.focal == 0 {
		return anchor{}, verr
	}
	a.emittingSession = viewer
	return a, nil
}

// anchorTracker owns the anchor's lifecycle for the live monitor: re-resolve
// while no focal task has appeared, then freeze forever on the first hit.
//
// Freezing preserves E-1698's anchor-once contract unchanged — once a task is
// anchored the view stays pinned to THIS window's task as other sessions come
// and go. A session that never claims keeps re-resolving every tick, which is
// correct: that is how the view recovers when the user finally claims.
type anchorTracker struct {
	cur     anchor
	resolve func() (anchor, error)
}

func newAnchorTracker(resolve func() (anchor, error)) *anchorTracker {
	return &anchorTracker{cur: anchor{hint: hintClaimBind}, resolve: resolve}
}

// refresh returns the anchor to render this tick. A resolver error while still
// unanchored is NON-FATAL — unlike a render error, which exits — because the
// whole point is to outlast a transient nothing-here-yet: the previous anchor is
// kept and the next tick retries.
func (t *anchorTracker) refresh() anchor {
	if t.cur.focal != 0 {
		return t.cur
	}
	if next, err := t.resolve(); err == nil {
		t.cur = next
	}
	return t.cur
}

// gatherRows returns the rows to render: the focal-anchored what's-next set when
// a goal is claimed (focal != 0), else the emitting session's own surfaced/
// revisited rows (E-1802) when a session is present but unclaimed. Empty when
// neither resolves — renderTo then prints the claim/bind hint.
//
// Seamed as a package var (like worktreeAnomalies below) so tests can drive
// monitorFrame's resolve→render composition without a DB.
var gatherRows = func(focal, parentSession, emittingSession int64, all bool) ([]monitor.SessionStatusRow, error) {
	if focal != 0 {
		return monitor.SessionStatusRows(focal, parentSession, all)
	}
	if emittingSession != 0 {
		return monitor.SessionStatusRowsForSession(emittingSession, all)
	}
	return nil, nil
}

// renderSnapshot queries the current rows for the anchored focal/parent (or the
// emitting session when no goal is claimed) and renders one frame to w.
// It returns the number of TASK rows rendered — 0 means the frame is the
// no-task hint, which the monitor's pane fit treats differently from a short
// real frame (see paneHeightForFrame).
func renderSnapshot(w io.Writer, a anchor, all bool, cols int, color bool, hm hiddenMode) (int, error) {
	rows, err := gatherRows(a.focal, a.parentSession, a.emittingSession, all)
	if err != nil {
		return 0, err
	}
	// Flat view only: fill each row's Unsettled flag from its worktree's git state
	// so the renderer can mark the landed-vs-worktree delta with ◆ (E-1701). --tree
	// takes a separate path and skips this git cost.
	monitor.AnnotateSessionStatusUnsettled(context.Background(), rows)
	// Layer the VIEWING session's hides on top (E-1914). Annotating rather than
	// filtering in the query keeps the row set viewer-agnostic and leaves the
	// omit/show/only decision entirely to the renderer — which is also what lets
	// --json emit every row with its hidden state attached.
	if err := annotateHidden(rows, a.emittingSession); err != nil {
		return 0, err
	}
	// Layer the VIEWING session's relations on for the display tier (E-1696).
	// Annotated, not queried, for the same reason as the hides — and one more:
	// the focal row set unions EVERY session working the focal task, so a
	// relation taken from the query would report some other session's
	// classification as yours.
	if err := annotateRelation(rows, a.emittingSession); err != nil {
		return 0, err
	}
	renderTo(w, rows, a.focal, a.hint, cols, color, hm)
	return len(rows), nil
}

// annotateHidden is the per-session hide source, seamed as a package var (like
// worktreeAnomalies and gatherRows) so the renderer's hide behavior is testable
// without a DB.
var annotateHidden = monitor.AnnotateSessionStatusHidden

// annotateRelation is the per-session relation source, seamed as a package var
// on the same rule as annotateHidden so the tier is testable without a DB.
var annotateRelation = monitor.AnnotateSessionStatusRelation

// monitorFrame produces one live-monitor frame: refresh the anchor, then render
// against it. Split out of monitorLoop so tests can drive the resolve→render
// composition — the whole of what E-1892 changed — without a terminal, a ticker,
// or a signal. monitorLoop keeps only the paint/fit/tick machinery.
//
// It forwards renderSnapshot's task-row count, which is the pane fit's input:
// 0 rows means the no-task hint, held at monitorPaneEmptyHeight rather than
// exact-fitted (E-1851). That count is exactly what changes on the tick a focal
// task finally resolves, so the pane regrows in the same repaint the rows
// appear in.
func monitorFrame(tracker *anchorTracker, w io.Writer, all bool, cols int, color bool, hm hiddenMode) (int, error) {
	a := tracker.refresh()
	return renderSnapshot(w, a, all, cols, color, hm)
}

// monitorLoop redraws the view every monitorInterval until SIGINT/SIGTERM,
// repainting only when the rendered frame changes (so an idle view doesn't
// flicker). It hides the cursor for the duration and restores it on every exit
// path. Width is re-detected each tick so a terminal resize is honored. This is
// the live `session monitor` dashboard; it loops the same snapshot renderer
// `session status` prints once.
//
// On every repaint it also fits its own tmux pane to the frame (E-1851) — the
// monitor knows its row count, `task spawn` cannot, so the pane sizes itself
// instead of being guessed at creation. Best-effort and silent: outside tmux, or
// when tmux refuses (a single-pane window), the view is unchanged.
//
// The anchor is re-resolved by the tracker each tick until a focal task appears
// (E-1892), so a monitor started before its session registers — which is the
// common case under `task spawn`'s layout — recovers instead of showing the
// claim/bind hint forever.
func monitorLoop(tracker *anchorTracker, all bool, colsOverride int, color bool, hm hiddenMode) {
	liveview.Loop(liveview.LoopConfig{
		Render: func(w io.Writer, cols int, color bool) (int, error) {
			return monitorFrame(tracker, w, all, cols, color, hm)
		},
		ColsOverride: colsOverride,
		FallbackCols: fallbackCols,
		Color:        color,
		// process is the session's process handle — a tmux pane id today. The
		// view fits this pane to its own frame on every repaint (E-1851).
		Pane:     os.Getenv("TMUX_PANE"),
		FireJobs: true,
		Fatal: func(err error) {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		},
	})
}

// frameLines, paneHeightForFrame, fitPaneToFrame and eraseEachLineToEOL moved
// to internal/liveview in E-1976 (`project monitor` fits its pane by the same
// rule). They stay reachable under their original names so this package's tests
// — which are where the rule is pinned — keep exercising the shared code.
func frameLines(frame string) int { return liveview.FrameLines(frame) }

func paneHeightForFrame(lines, rows, windowHeight int) int {
	return liveview.PaneHeightForFrame(lines, rows, windowHeight)
}

func fitPaneToFrame(pane, frame string, rows, fitted int) int {
	return liveview.FitPaneToFrame(pane, frame, rows, fitted)
}

func eraseEachLineToEOL(frame string) string { return liveview.EraseEachLineToEOL(frame) }

// renderTo writes the legend and rows to w. focal==0 (or no rows) prints the
// no-task hint instead of an empty table — a claim/bind message (or a
// register-session message) mirroring the tmux status line, NEVER an unrelated
// task's rows (E-1698).
// worktreeAnomalies is the focal-row anomaly source, seamed as a package var so
// tests can drive the focal expansion with a stubbed anomaly set (a genuinely
// divergent worktree can't be seeded hermetically). Production points at the
// real DB/git-backed monitor.WorktreeAnomalies.
// Its TYPE changed with monitor.WorktreeAnomalies's signature (E-2128): the
// anomaly probes now take a context, so the stub a test substitutes takes one
// too.
var worktreeAnomalies = monitor.WorktreeAnomalies

func renderTo(w io.Writer, rows []monitor.SessionStatusRow, focal int64, noTaskHint string, cols int, color bool, hm hiddenMode) {
	// Gate on rows, not focal: a session with no claimed goal (focal == 0) still
	// has surfaced/revisited rows to render (E-1802). Only the truly-empty case —
	// no goal AND no session work — falls through to the claim/bind (or register-
	// session) hint, NEVER an unrelated task's rows (E-1698).
	//
	// Checked BEFORE the hide filter on purpose: "you have nothing here" and "you
	// have work but you hid it" are different situations, and the claim/bind hint
	// would be actively wrong advice for the second.
	if len(rows) == 0 {
		fmt.Fprintln(w, dim(noTaskHint, color))
		faultbadge.Render(w, cols, color, faults.AllProjects)
		return
	}

	// Apply the viewing session's hides (E-1914). hiddenN counts the SUPPRESSED
	// rows specifically, not every hidden row, so the footer reports what is
	// missing from this frame — under --show-hidden/--only-hidden nothing is
	// missing and there is nothing to footer.
	rows, hiddenN := applyHiddenMode(rows, hm)

	if len(rows) == 0 {
		// Everything was hidden. The footer is the whole frame — that is exactly
		// the trace the default mode owes the user, so it must not be swallowed by
		// an "empty" shortcut. Under --only-hidden an empty result means the
		// opposite (nothing is hidden), which gets its own hint.
		if hm == hiddenOnly {
			fmt.Fprintln(w, dim("  nothing hidden in this session", color))
		} else if hiddenN > 0 {
			fmt.Fprintln(w, dim(hiddenFooter(hiddenN), color))
		}
		faultbadge.Render(w, cols, color, faults.AllProjects)
		return
	}

	sortRows(rows)
	fmt.Fprintln(w, dim(buildLegend(rows), color))

	// Block-column width: 0 if nothing is blocked anywhere, 1 if no single row
	// is both blocked and blocking, 2 only when some row needs both glyphs.
	bw := 0
	for _, r := range rows {
		n := 0
		if r.BlockedByN > 0 {
			n++
		}
		if r.BlocksN > 0 {
			n++
		}
		if n > bw {
			bw = n
		}
	}

	// Hidden-column width, on the same width-on-demand rule as the block column:
	// the ⊘ slot exists only when a RENDERED row wears it, so the default view —
	// where hidden rows are omitted outright — is byte-identical to before E-1914.
	hw := 0
	for _, r := range rows {
		if r.Hidden {
			hw = 2
			break
		}
	}

	// Relation-column width, same width-on-demand rule (E-1696): the ⊕/· slot
	// exists only when a RENDERED row carries a mark, so a frame with no queued
	// or referenced rows is byte-identical to before.
	rw := relationColWidth(rows)

	// Fixed prefix width = "I L NNNNNN P " = 13 cols (icon, type letter, the
	// 6-wide left-justified E-id, phase char, each single-spaced).
	const prefixWidth = 13
	blockSeg := blockSegWidth(bw)
	titleBudget := cols - prefixWidth - blockSeg - hw - rw
	if titleBudget < minTitleBudget {
		titleBudget = minTitleBudget
	}

	for _, r := range rows {
		act := classify(r)
		line := fmt.Sprintf("%s %s%s%-6s %s ",
			act.icon(), typeLetter(r.TypeSlug), unsettledMark(r), "E-"+strconv.FormatInt(r.ID, 10), phaseChar(r),
		)
		line += hiddenField(r, hw)
		line += relationField(r, rw)
		line += blockField(r, bw)
		// The supersession note is charged to the title's budget, not appended
		// past it: the row must still fit `cols`, and the note is the part that
		// must survive — a title truncated a few glyphs earlier costs nothing,
		// a wrapped row costs the whole table's alignment (E-1956).
		note := statusNotes(r)
		avail := titleBudget - runewidth.StringWidth(note)
		if avail < minTitleBudget {
			avail = minTitleBudget
		}
		line += runewidth.Truncate(collapse(r.Title), avail, "…") + note
		fmt.Fprintln(w, colorize(line, r, color))

		// Focal-row detail: expand the coarse ◆ marker into the specific
		// git/worktree anomalies for the focal worktree (E-1758), the same set
		// `endless worktree check` reports. Silent when there are none — a clean
		// (or merely unlanded) focal worktree adds no lines here.
		//
		// One kind is suppressed HERE (not in the shared core): AnomalyUncommitted.
		// `worktree check` runs at HANDOFF, where a modified tree is a genuine anomaly,
		// so that surface keeps it. `session status` renders CONTINUOUSLY, including
		// on the focal task you are mid-implementation on, where uncommitted user
		// files are the EXPECTED work-in-progress state — a false positive (E-1768).
		// The other kinds (detached HEAD, branch mismatch, prunable) are genuine
		// even mid-work and still surface. The ◆ glyph on the row itself stays
		// (it correctly means unsettled — modified-or-unlanded, E-1701); only this
		// detail line goes.
		if r.IsFocal {
			for _, a := range worktreeAnomalies(context.Background(), r.ProjectID, r.ID) {
				if a.Kind == monitor.AnomalyUncommitted {
					continue
				}
				fmt.Fprintln(w, dim("      ◆ "+a.Line(), color))
			}
		}
	}

	// The hidden footer sits between the rows and the fault badge: it annotates
	// the row set (it says what the table is NOT showing you), while the badge
	// annotates the machine. Printed whenever anything was suppressed — never
	// conditional on width, color, or row count — because a hidden task that
	// vanishes without a trace is the one failure this feature must not have.
	if hiddenN > 0 {
		fmt.Fprintln(w, dim(hiddenFooter(hiddenN), color))
	}

	// Uncleared faults are appended last so they read as an annotation on the
	// view rather than competing with the task rows for attention (E-698).
	//
	// AllProjects, not this session's project (E-1960): `session status` is a
	// machine-wide view — it renders every live session on the box, whatever
	// project each is in — so a fault narrowed to one of them would be the only
	// narrowed thing on the frame. `project status` is where the scoped badge
	// belongs, and that is what it passes.
	faultbadge.Render(w, cols, color, faults.AllProjects)
}

// applyHiddenMode splits rows by the viewing session's hides and returns the set
// to render plus the number SUPPRESSED from this frame. It never mutates the
// input slice — --json renders the same annotated rows unfiltered.
//
//   - hiddenOmit: drop hidden rows; the count is what was dropped (→ footer).
//   - hiddenShow: keep everything; nothing is suppressed, so the count is 0.
//   - hiddenOnly: keep only hidden rows; nothing hidden is suppressed, so 0.
func applyHiddenMode(rows []monitor.SessionStatusRow, hm hiddenMode) ([]monitor.SessionStatusRow, int) {
	if hm == hiddenShow {
		return rows, 0
	}
	out := make([]monitor.SessionStatusRow, 0, len(rows))
	suppressed := 0
	for _, r := range rows {
		if r.Hidden == (hm == hiddenOnly) {
			out = append(out, r)
			continue
		}
		if hm == hiddenOmit {
			suppressed++
		}
	}
	return out, suppressed
}

// minTitleBudget is the floor on the columns left for a row's title, applied
// both to the shared budget and again after the supersession note is charged
// against it. Below this a title is all ellipsis and the row says nothing.
const minTitleBudget = 10

// relationNote is the inline '  (<phrase> E-NNN, E-MMM)' suffix a row carries
// beside its status, or "".
//
// The TERMINAL-status gate lives here, once, for every relation that uses this
// shape (E-1956 for `replaced by`, E-1185 for `duplicates`). A terminal status
// is where the row otherwise reads as the end of the story — ⇥ closed on a
// superseded or duplicated task looks abandoned rather than handed on. An open
// task keeps its plain row; the fact is still in `task show`. Gating also means
// the DEFAULT view, which has no terminal rows in it at all, renders exactly as
// it did before any of this existed.
//
// One gate, not one per note: two copies of a display rule are two things that
// can drift, and a third relation added later would have to remember to bring
// its own.
func relationNote(ids []int64, status, phrase string) string {
	if len(ids) == 0 || !isTerminal(status) {
		return ""
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, "E-"+strconv.FormatInt(id, 10))
	}
	return "  (" + phrase + " " + strings.Join(out, ", ") + ")"
}

func replacedByNote(r monitor.SessionStatusRow) string {
	return relationNote(r.ReplacedBy, r.Status, "replaced by")
}

func duplicatesNote(r monitor.SessionStatusRow) string {
	return relationNote(r.Duplicates, r.Status, "duplicates")
}

// statusNotes is every inline annotation a row carries. A task can be both
// superseded and a duplicate; the notes compose rather than one winning.
func statusNotes(r monitor.SessionStatusRow) string {
	return replacedByNote(r) + duplicatesNote(r)
}

// hiddenField renders the ⊘ column for a row to width hw (0 = column absent,
// 2 = glyph + space). Companion to blockField, same width-on-demand contract.
func hiddenField(r monitor.SessionStatusRow, hw int) string {
	if hw == 0 {
		return ""
	}
	if r.Hidden {
		return hiddenGlyph + " "
	}
	return "  "
}

// buildLegend returns the dynamic header line: only the glyphs actually present
// in rows, in enum order (actions) then a fixed order (decorations), joined by
// the same two-space separator with NO group divider. Rebuilt from the current
// rows each frame by renderTo, so `session status` (one-shot) and `session
// monitor` (looped) stay byte-identical by construction. Because only present
// glyphs are included there is never any absent-glyph padding; the set fits one
// line in >99% of cases and the terminal soft-wraps in the rare overflow (no
// truncation, which would hide a real glyph).
func buildLegend(rows []monitor.SessionStatusRow) string {
	var present [len(actionMeta)]bool
	var done, blocked, blocks, unsettled, notStarted, undetermined, hidden, queued, referenced bool
	for _, r := range rows {
		present[classify(r)] = true
		if isTerminal(r.Status) {
			done = true
		}
		if r.Hidden {
			hidden = true
		}
		switch r.Relation {
		case sessiontaskrelation.RelationQueued:
			queued = true
		case sessiontaskrelation.RelationReferenced:
			referenced = true
		}
		if r.BlockedByN > 0 {
			blocked = true
		}
		if r.BlocksN > 0 {
			blocks = true
		}
		switch unsettledMark(r) {
		case unsettledGlyph:
			unsettled = true
		case notStartedGlyph:
			notStarted = true
		case undeterminedGlyph:
			undetermined = true
		}
	}
	var parts []string
	for a := action(0); int(a) < len(actionMeta); a++ {
		if present[a] {
			parts = append(parts, a.icon()+" "+a.label())
		}
	}
	// Decorations after the actions, each shown only when a row bears it. ✓ is the
	// phase-column done marker (phaseChar); ⊗/⏸ match blockField; ◆, ⊙ and ~ match
	// unsettledMark (E-1701, E-2107, E-2128) — derived by CALLING it rather than
	// re-deriving the rule, so the legend cannot disagree with the column it
	// documents. ✓ leads the decorations as it marks the task's own state
	// (a focal/parent/from row can be terminal) before the relational/worktree
	// markers.
	if done {
		parts = append(parts, "✓ done")
	}
	if blocked {
		parts = append(parts, "⊗ blocked")
	}
	if blocks {
		parts = append(parts, "⏸ blocks")
	}
	if unsettled {
		parts = append(parts, unsettledGlyph+" unsettled")
	}
	// ⊙ sits beside ◆ because they are two states of the SAME column, and after
	// it because the column reads in descending order of outstanding work: ◆ has
	// some, ⊙ has none yet, a space has none left (E-2107).
	if notStarted {
		parts = append(parts, notStartedGlyph+" not started")
	}
	// ~ closes the same column's run, after the three states that are answers,
	// because it is the absence of one (E-2128).
	if undetermined {
		parts = append(parts, undeterminedGlyph+" not yet determined")
	}
	// ⊘ comes last: it is the only decoration that describes THIS SESSION's view
	// of the row rather than a property of the task or its worktree, and it can
	// only ever appear under --show-hidden/--only-hidden.
	if hidden {
		parts = append(parts, hiddenGlyph+" hidden")
	}
	// ⊕/· join ⊘ in the view-scoped tail: like hidden, they describe how the row
	// entered THIS SESSION's scope rather than anything about the task (E-1696).
	// ⊕ before ·, matching the order they sort in.
	if queued {
		parts = append(parts, queuedGlyph+" queued")
	}
	if referenced {
		parts = append(parts, referencedGlyph+" referenced")
	}
	return strings.Join(parts, "  ")
}

// classify maps a row to its action, applying the status canonicalization from
// the plan: untriaged → triage; revisit/unplanned/needs_plan → plan;
// verify/unverified → verify;
// underway/in_progress → working (→ orphan when not in-flight); ready → do
// REGARDLESS of plan text (ED-1522, confirmed by Mike). Focal/parent/from/
// in-flight decorations take precedence over status; parent (real task-tree
// parent) outranks from (spawner) when a single task is both (E-1694).
func classify(r monitor.SessionStatusRow) action {
	switch {
	case r.IsFocal:
		return actThis
	case r.IsParent:
		return actParent
	case r.IsFrom:
		return actFrom
	case r.InFlight:
		return actDoing
	}
	// E-1693: a landed task's work has merged — no do/plan/verify verb applies. It
	// stays visible (still a non-terminal status) but routes to actLanded (⏚) so
	// the monitor never offers it as a fresh actionable spawn. Checked after the
	// decorations (a landed task a live session is on still reads ⟳) and before the
	// status switch.
	if r.Landed {
		return actLanded
	}
	// E-1871: a terminal status is a terminus, not a verb — route it to actDone
	// (⇥ closed) rather than letting it fall through the switch's default to
	// actUnknown (⁇), which is reserved for a status classify() does not know
	// about. Delegated to isTerminal rather than re-listed as switch cases so the
	// two cannot drift; a switch case cannot call a function, hence the if. It sits
	// AFTER the r.Landed check on purpose — ⏚ landed outranks ⇥ closed, so ⇥ marks
	// only closed work that never merged.
	if isTerminal(r.Status) {
		return actDone
	}
	switch r.Status {
	case "ready":
		return actDo
	case "submitted":
		// `submitted` = planned/spec-complete, awaiting human approval. It has a
		// spec already, so it is NOT `actPlan` (✎ plan) — that would mis-show a
		// planned-but-unapproved task as "needs a plan". But it is also NOT
		// `actDo` (▶): the claim gate refuses a submitted task, so rendering it as
		// spawnable contradicts the gate. It routes to its own actReview (⚑),
		// prompting the user to review/approve before it becomes actionable.
		return actReview
	case "untriaged":
		// E-1845. Load-bearing case: without it `untriaged` would fall through
		// to actUnknown, painting ⁇ — the should-never-happen marker — on the
		// most common row in the database, since every new task starts here. That
		// is the bug E-1871 fixed for terminal statuses, and it would be worse
		// this time. It is also NOT actPlan: an untriaged task has no judgment
		// about it yet, so "needs a plan" would be a claim nobody has made.
		return actTriage
	case "unplanned", "needs_plan", "revisit":
		return actPlan
	case "verify", "unverified":
		return actVerify
	case "underway", "in_progress":
		return actOrphan
	default:
		return actUnknown
	}
}

func sortRows(rows []monitor.SessionStatusRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		// E-1696, primary key: `referenced` rows sink below EVERYTHING, whatever
		// their status. Flood control — reads are high-volume and would otherwise
		// push real work off the top of the pane. This is the only key that
		// outranks the action classification, and only in the sinking direction.
		ri, rj := isReferenced(rows[i]), isReferenced(rows[j])
		if ri != rj {
			return !ri
		}
		ai, aj := classify(rows[i]), classify(rows[j])
		if ai != aj {
			return ai < aj
		}
		// E-1696, tiebreak only: between equally actionable rows, decided work
		// (goal/queued) sorts above incidental work. Deliberately BELOW the action
		// key — a queued task parked in `later` must not jump the row you are
		// actually working.
		if pi, pj := prominence(rows[i]), prominence(rows[j]); pi != pj {
			return pi < pj
		}
		pi, pj := phaseRank(rows[i].Phase), phaseRank(rows[j].Phase)
		if pi != pj {
			return pi < pj
		}
		return rows[i].ID < rows[j].ID
	})
}

func phaseRank(phase string) int {
	switch phase {
	case "urgent":
		return 0
	case "now":
		return 1
	case "next":
		return 2
	case "later":
		return 3
	case "maybe":
		return 4
	default:
		return 5
	}
}

// unsettledGlyph, notStartedGlyph and undeterminedGlyph are the three marked
// states of the unsettledMark column; the fourth is a plain space. ◆ (U+25C6
// BLACK DIAMOND) is E-1701's original. ⊙ (U+2299 CIRCLED DOT OPERATOR) is
// E-2107's addition, chosen for its silhouette: within this column the only
// distinction is circle vs diamond — which ◇ (U+25C7) would not have given. The
// other circled operators — ⊗ blocked, ⏸ blocks, ⊘ hidden, ⊕ queued — all render
// AFTER the id, so position disambiguates them. All measure one column (asserted
// in TestUnsettledMark), like the space they replace.
//
// ~ (U+007E TILDE) is E-2128's, meaning "not yet determined". Three things
// recommend it over every circle-and-diamond variant:
//
//   - `·` was the obvious pick and is WRONG. This package already defines it as
//     referencedGlyph (relation_tier.go) and buildLegend prints it as
//     `· referenced`. In a ROW position disambiguates the two — unsettledMark
//     precedes the id, relationField follows it — but the legend has no position,
//     and a frame holding both states would print `~ not yet determined
//     · referenced` on one line under one glyph. `◌` being taken by the triage
//     action is what rules out the remaining dotted circles.
//   - Being ASCII it is display width 1 BY DEFINITION, which sidesteps the East
//     Asian Ambiguous trap that forced ⊙ to be asserted rather than assumed
//     (E-1765, E-2107). The width test below is still written; it simply cannot
//     fail.
//   - Its silhouette is a horizontal wave, colliding with nothing in a vocabulary
//     of circles, diamonds, arrows and boxes — actions ● ↑ ↩ ⟳ ▶ ⚑ ✎ ☑ ◷ ⏚ ⁇ ⇥ ◌,
//     unsettled ◆ ⊙, phase ✓ ! 1 2 3 ?, hidden ⊘, relation ⊕ ·, block ⊗ ⏸ — and
//     "approximate, unresolved" is the right reading of it.
const (
	unsettledGlyph    = "◆"
	notStartedGlyph   = "⊙"
	undeterminedGlyph = "~"
)

// unsettledMark is the single-column separator between the task-type letter and
// the id. Four states (E-2107, E-2128), together answering "is there work product
// here, and where is it?":
//
//	~   not yet determined — the worktree is clean and nothing has computed
//	    whether its commits reached the base branch (E-2128).
//	◆   work product, still outstanding — the worktree diverges from main:
//	    unlanded commits, or changes made since a land (E-1701).
//	⊙   no work product yet — never spawned, or claimed and still empty.
//	    (space) work product, and all of it landed.
//
// All four are width 1, so the fixed 13-col prefix and its alignment hold in
// every state.
//
// `~` is tested FIRST, ahead of ◆. If the expensive answer has not been computed,
// the row says so rather than reporting a state derived from something else —
// which is ED-1589's rule, and the reason the glyph exists at all: before it, an
// unavailable answer rendered as a blank, byte-identical to a verified-clean
// worktree.
//
// Two refinements keep `~` from swallowing rows whose answer IS known, and both
// fall out of what each probe costs rather than from policy. A task with no
// worktree is KNOWN — there is nothing to land and no git ran. And a DIRTY
// worktree is known to be unsettled, because `git status --porcelain` stays live
// and is visible every tick without the cache. Both are decided in
// monitor.UnsettledDetail.UnsettledKnown, so this column cannot disagree with the
// probe about which rows are eligible. `~` therefore appears on clean worktrees
// during the first job pass after a cold start, and on a machine where nothing
// fires the job — and since the monitor tick is what fires it, that window is
// normally one interval.
//
// The two cases sharing ⊙ — a task nobody picked up, and a task a session is
// sitting on that has produced nothing — are the same fact about the work, and
// this column deliberately does not try to tell them apart: the action icon and
// the status already do.
//
// The ⊙/space split is decided by STATUS, not by landing history. task_landings
// is not a reliable record of what reached main (E-2087 measured branches whose
// content is demonstrably on main with no landing row at all), and a git-side
// answer would put a new probe on a per-row hot path. taskstatus.Shipped is
// exactly "reached the verification gate or passed it", which is what having
// produced work product means, and it is already on the row for free — so a
// status added to the vocabulary forces this decision rather than silently
// defaulting to a blank.
//
// One accepted mis-signal: a task that lands mid-flight and keeps working stays
// `underway`, so it wears ⊙ despite real landed work. It is still true that
// nothing is outstanding. The legend therefore labels ⊙ by what it MEANS —
// "not started" — not by the status test it is derived from, so the derivation
// can be sharpened later without the vocabulary changing.
//
// ⊙ must NOT join ◆ in vetoing dim (see colorize): ◆ means "still something to
// do here", ⊙ means the opposite, and a never-started `later` row should still
// read dim.
//
// buildLegend documents whichever of ◆/⊙ a rendered row bears (E-1750 for ◆,
// reversing the 2026-07-01 "◆ stays out of the legend" call). Distinct from
// --tree's leading focal marker — different view, different glyph, no clash.
func unsettledMark(r monitor.SessionStatusRow) string {
	switch {
	case !r.UnsettledKnown:
		return undeterminedGlyph
	case r.Unsettled:
		return unsettledGlyph
	case !taskstatus.Has(taskstatus.Shipped, r.Status):
		return notStartedGlyph
	default:
		return " "
	}
}

func typeLetter(slug string) string {
	switch slug {
	case "epic":
		return "E"
	case "bugfix":
		return "F"
	case "research":
		return "R"
	case "brainstorm":
		return "B"
	default:
		return "T"
	}
}

// phaseChar is the single-column phase indicator: ✓ for done-work (focal/parent
// rows can be terminal), else a per-phase glyph.
func phaseChar(r monitor.SessionStatusRow) string {
	if isTerminal(r.Status) {
		return "✓"
	}
	switch r.Phase {
	case "urgent":
		return "!"
	case "now":
		return "1"
	case "next":
		return "2"
	case "later":
		return "3"
	case "maybe":
		return "?"
	default:
		return " "
	}
}

// blockField renders the block column for a row to the chosen total width bw:
// ⊗ when blocked by an open task, ⏸ when it blocks others. Width 0 emits
// nothing; width 1 emits one glyph + a space; width 2 emits both slots + a space.
func blockField(r monitor.SessionStatusRow, bw int) string {
	switch bw {
	case 0:
		return ""
	case 1:
		switch {
		case r.BlockedByN > 0:
			return "⊗ "
		case r.BlocksN > 0:
			return "⏸ "
		default:
			return "  "
		}
	default:
		c1, c2 := " ", " "
		if r.BlockedByN > 0 {
			c1 = "⊗"
		}
		if r.BlocksN > 0 {
			c2 = "⏸"
		}
		return c1 + c2 + " "
	}
}

// displayWidth is the terminal column count of s. Callers measure pre-color
// text (no ANSI escapes), so it's a thin wrapper over runewidth that keeps the
// renderer and its tests agreeing on width.
func displayWidth(s string) int {
	return runewidth.StringWidth(s)
}

func blockSegWidth(bw int) int {
	switch bw {
	case 0:
		return 0
	case 1:
		return 2
	default:
		return 3
	}
}

// isTerminal reports whether a status is a terminus rather than a verb — the
// rows classify() routes to actDone (⇥ closed). Delegated to taskstatus so it
// cannot drift from monitor.IsTerminalTaskStatus, which answers the same
// question for the cwd gate (E-1891).
func isTerminal(status string) bool {
	return taskstatus.Has(taskstatus.Terminal, status)
}

func detectCols(override int) int { return liveview.DetectCols(override, fallbackCols) }

func colorEnabled() bool { return liveview.ColorEnabled() }

// collapse squeezes internal whitespace runs to single spaces so multi-line or
// padded titles render on one line (matches `endless session list`). Shared with
// `project monitor` via internal/liveview since E-1976.
func collapse(s string) string { return liveview.Collapse(s) }

// ANSI helpers. Phase-by-intensity: urgent bold, later/maybe dim, terminal rows
// dim, everything else normal — except that an unsettled (◆) row is never dimmed
// (E-1707; see colorize). Kept to bold/dim (SGR 1/2) so it reads on any theme
// without color-profile guessing — lipgloss is reserved for the future TUI
// (E-859/E-1622), out of scope here.
const (
	ansiReset = liveview.Reset
	ansiBold  = liveview.Bold
	ansiDim   = liveview.DimSGR
)

// colorize applies the row's intensity. unsettled (◆, E-1701) VETOES every dim
// case: dim reads as "done, nothing to do here", which directly contradicts what
// ◆ means — this worktree still diverges from main and needs a land (E-1707, hit
// live on E-1687, where a `completed` + ◆ row rendered grey and the needed land
// was nearly missed). The veto covers the later/maybe phases as well as the
// terminal case: the failure mode is identical (grey swallows the marker), and a
// uniform "◆ is never dimmed" rule is easier to trust than a per-case carve-out.
// Vetoing dim yields NORMAL weight, not bold — bold stays reserved for `urgent`,
// so ◆ makes a row stop reading as done without also making it shout.
//
// terminal still outranks urgent (unchanged): a done urgent row reads dim, not
// bold, unless it is unsettled.
// hidden joins the dim cases (E-1914): under --show-hidden/--only-hidden a
// suppressed row is present but deliberately demoted, which is exactly what dim
// says. It sits inside the veto, not outside it — an unsettled hidden row still
// renders at normal weight, because "this worktree still needs a land" outranks
// "I asked not to see this" for the same reason it outranks "this is done".
// colorize applies the row's intensity: dim for muted rows, bold for urgent,
// normal otherwise.
//
// It takes the ROW rather than the five flags it used to derive them from
// (E-1696). Adding `referenced` to the dim set would have made a sixth boolean
// in a positional list of five, where every call site is a row anyway and a
// transposed pair is invisible at the call and at the definition.
//
// `referenced` joins the existing dim set rather than getting its own arm, so
// E-1707's unsettled veto covers it on the same terms as every other dim case:
// a diverged worktree always reads at full intensity, whatever put the row here.
//
// The veto keys on r.Unsettled, so `~` does not trigger it and does not need to
// be excluded by name (E-2128): a row whose verdict has not been computed is not
// a row with outstanding work, and a never-started `later` task should still read
// dim while the job catches up. ⊙ is out of the veto for the same reason.
func colorize(line string, r monitor.SessionStatusRow, enabled bool) string {
	if !enabled {
		return line
	}
	phase := r.Phase
	switch {
	case isTerminal(r.Status), r.Hidden, isReferenced(r),
		phase == "later", phase == "maybe":
		if r.Unsettled {
			return line
		}
		return ansiDim + line + ansiReset
	case phase == "urgent":
		return ansiBold + line + ansiReset
	default:
		return line
	}
}

func dim(s string, enabled bool) string { return liveview.Dim(s, enabled) }
