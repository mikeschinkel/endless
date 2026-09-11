package projectstatuscmd

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faultbadge"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/sessionstate"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// The project attention board's ranking and rendering (E-1976).
//
// The board answers one question: what in this project is claiming a PERSON's
// attention, loudest first. That is a different question from `session status`'s
// — "what is next for the task I am on" — which is why this is a second view
// rather than a flag on that one, and why its rows can be sessions.

// action is a row's primary classification, in rank order. Both the glyph and
// the sort position derive from it, so the enum's declaration order IS the
// board's priority order — there is no second list to keep in step.
type action int

const (
	// actWaiting: a live session paused on the user — 'prompted' (blocked on a
	// permission prompt) or 'needs_input' (it asked a question). Ranked FIRST
	// even though E-1976's brief says unverified sorts to the top, and the
	// difference is a real one: a paused session is a hard stop that nothing but
	// the user can clear, and there are at most a handful of them, while
	// unverified runs to dozens.
	//
	// E-2091 gave the rank its producer. Both states land here and the board
	// needs no third rank to tell them apart: the age column already
	// distinguishes a live prompt from a two-month-old row on sight, which is
	// what that column is for.
	actWaiting action = iota
	// actVerify: an `unverified` task. The reason the board exists: 57 of these
	// accumulated in `endless` alone precisely because nothing kept them in view.
	actVerify
	// actRead: an `unreviewed` task — a research or brainstorm outcome delivered
	// and waiting to be read. Distinct from actVerify because the act is
	// different (read a document vs. run a command), and because conflating the
	// two would let a pile of unread outcomes hide inside the verify count.
	actRead
	// actReview: a `submitted` task — a plan awaiting approval. Ranked below the
	// two "something is finished" states because approving unblocks work that has
	// not started, while verifying and reading close work already done.
	actReview
	// actOrphan: an `underway` task with no live session on it. Work somebody
	// claimed and walked away from. No status describes this — it is the ABSENCE
	// of a session that makes it interesting — which is why the board computes it
	// rather than querying for it.
	actOrphan
	// actIdle: a live session whose turn has ended. It is waiting for the user's
	// next prompt, so it is a genuine claim on attention, just a quieter one than
	// a session blocked mid-turn.
	actIdle
	// actDoing: a live session working. Nothing is needed from the user; it is on
	// the board so the board is a complete picture of the project rather than a
	// worry list with no context.
	actDoing
	// actReady: a `ready` task — spawnable. --all only: this is a claim on
	// CAPACITY, not on attention, and there are enough of them to bury every rank
	// above.
	actReady
	// actUnknown: a row no rule above matched — a should-never-happen net, kept
	// for the same reason sessionstatuscmd keeps one. Its ⁇ appearing on the
	// board means a status or session state slipped past classify().
	actUnknown
)

// actionMeta is the one table: glyph, legend label, footer noun, and sort
// direction per action, indexed by the enum so rank order is legend order and
// footer order for free.
//
// Glyphs are borrowed from the views the user already reads — ☑ ⚑ ◷ ▶ ⟳ from
// `session status`, ‖ from `session list` — wherever the meaning is the same, so
// the board teaches no new vocabulary for concepts that already have one. Only
// the two genuinely new ideas get new glyphs: ⚠ for a blocked session and ☰ for
// an outcome to read. Every glyph measures one column (asserted in
// TestActionGlyphsAreSingleWidth), which is what keeps the fixed prefix aligned.
//
// oldestFirst is per-action, and almost every action says no.
//
// The default is NEWEST first, because the per-group cap makes the top of a
// group the only part most people ever read, and the newest event is the
// actionable one. That holds for both kinds of row and for the same reason:
// among unverified tasks, the work that just finished is what you can verify
// now, while 30-to-90-day rows are a relevance decision (E-1977's backlog
// drain), and among idle sessions the one that just handed something back is
// what you were waiting for, while a 15-day-old pane is abandoned. Measured
// against the real database, oldest-first pinned both groups' visible ten to
// their oldest rows and pushed everything from the last hour under the cap —
// the exact disappearance this board exists to prevent.
//
// actWaiting is the single exception, and it is not an inconsistency: a session
// blocked mid-turn is a QUEUE, not a feed. Longest-blocked-first is the fair
// discipline there, and the group is small enough that the cap never bites.
var actionMeta = [...]struct {
	icon        string
	label       string
	noun        string
	oldestFirst bool
}{
	actWaiting: {"⚠", "waiting", "waiting", true},
	actVerify:  {"☑", "verify", "unverified", false},
	actRead:    {"☰", "read", "unreviewed", false},
	actReview:  {"⚑", "review", "submitted", false},
	actOrphan:  {"◷", "orphan", "orphaned", false},
	actIdle:    {"‖", "idle", "idle", false},
	actDoing:   {"⟳", "doing", "working", false},
	actReady:   {"▶", "ready", "ready", false},
	actUnknown: {"⁇", "unknown", "unknown", false},
}

func (a action) icon() string      { return actionMeta[a].icon }
func (a action) label() string     { return actionMeta[a].label }
func (a action) noun() string      { return actionMeta[a].noun }
func (a action) oldestFirst() bool { return actionMeta[a].oldestFirst }

// actions is every action in rank order, for callers that need to iterate the
// board's groups (the renderer, the cap, the legend).
func actions() []action {
	out := make([]action, 0, len(actionMeta))
	for i := range actionMeta {
		out = append(out, action(i))
	}
	return out
}

// defaultGroupCap is the per-group row limit.
//
// PER GROUP, not per board, and that is the whole point. A single board-wide cap
// with `verify` ranked near the top would spend every row on unverified tasks
// and push the sessions — the "many concurrent sessions" the board was built to
// triage — off the bottom. Capping each group means every kind of claim keeps a
// place on the board however lopsided the backlog is, and the footer says how
// much of each was left out.
//
// Ten rather than rowcap.py's twenty for the same reason: this view lives in a
// tmux pane sized to its own frame, and eight groups × twenty is not a board.
const defaultGroupCap = 10

// classify maps a row to its action. The SESSION half wins wherever a row has
// both, because a live session is a fact about right now while a task status is
// a fact about the work — and the board ranks by what is happening.
func classify(r monitor.ProjectStatusRow) action {
	if r.HasSession() {
		switch r.SessionState {
		case sessionstate.Prompted, sessionstate.NeedsInput:
			return actWaiting
		case sessionstate.Idle:
			return actIdle
		case sessionstate.Working:
			return actDoing
		default:
			return actUnknown
		}
	}
	switch taskstatus.Status(r.Status) {
	case taskstatus.Unverified:
		return actVerify
	case taskstatus.Unreviewed:
		return actRead
	case taskstatus.Submitted:
		return actReview
	case taskstatus.Underway:
		return actOrphan
	case taskstatus.Ready:
		return actReady
	default:
		return actUnknown
	}
}

// clock is the timestamp a row is sorted and aged by: its session's last
// activity when it has one, else the task's last update. Same rule as classify —
// the session half wins, because that is the clock the row's action is about.
func clock(r monitor.ProjectStatusRow) string {
	if r.HasSession() {
		return r.SessionActivity
	}
	return r.TaskUpdated
}

// tsLayouts are the two spellings a timestamp arrives in. Endless writes the
// first (strftime('%Y-%m-%dT%H:%M:%S')); SQLite's own datetime() writes the
// second, which is what a hand-seeded fixture or an older row carries.
var tsLayouts = []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"}

// parseTS reads a stored timestamp as UTC. A value it cannot parse yields the
// zero time, which ages as "-" rather than as an absurd duration.
func parseTS(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range tsLayouts {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t
		}
	}
	return time.Time{}
}

// ageWidth is the age column's fixed width. Four columns holds every value the
// formatter can produce ("999d"), so the column never shifts the title.
const ageWidth = 4

// formatAge renders a duration in at most ageWidth columns, coarsening as it
// grows: seconds, minutes, hours, then days. Precision below the unit is not
// worth a column here — "3h" and "3h12m" prompt the same action.
func formatAge(d time.Duration) string {
	switch {
	case d < 0:
		// A clock ahead of ours. Rather than render a negative age, treat it as
		// now: a skewed timestamp is not worth a distinct display state.
		return "0s"
	case d < time.Minute:
		return strconv.Itoa(int(d.Seconds())) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d.Minutes())) + "m"
	case d < 48*time.Hour:
		return strconv.Itoa(int(d.Hours())) + "h"
	default:
		days := int(d.Hours() / 24)
		if days > 999 {
			days = 999
		}
		return strconv.Itoa(days) + "d"
	}
}

// ageCell is a row's age against now, or "-" when its timestamp is unreadable.
func ageCell(r monitor.ProjectStatusRow, now time.Time) string {
	t := parseTS(clock(r))
	if t.IsZero() {
		return "-"
	}
	return formatAge(now.Sub(t))
}

// group is one rank's rows, already sorted, plus how many the cap dropped.
type group struct {
	act     action
	rows    []monitor.ProjectStatusRow
	omitted int
}

// buildGroups classifies and sorts, then applies TWO independent limits.
//
// Sorting happens before either, which is what makes a limit mean "the N that
// matter most" rather than "whichever N the database returned".
//
//   - groupCap is the caller's --limit: a ceiling on any single group, so one
//     lopsided rank cannot spend the whole board. <= 0 means uncapped.
//   - budget is how many rows the destination can actually SHOW (see
//     liveview.DetectRows). budget <= 0 means unbounded — a piped snapshot, or
//     a terminal whose size cannot be read.
//
// Both are needed, and neither substitutes for the other. Without the cap, 57
// unverified tasks take every row the budget has. Without the budget, five
// groups of ten overrun a pane, and an overrun frame scrolls — losing its TOP,
// which is precisely the rows the ranking put there.
func buildGroups(rows []monitor.ProjectStatusRow, groupCap, budget, overhead int, now time.Time) []group {
	byAction := map[action][]monitor.ProjectStatusRow{}
	for _, r := range rows {
		a := classify(r)
		byAction[a] = append(byAction[a], r)
	}

	var out []group
	for _, a := range actions() {
		set := byAction[a]
		if len(set) == 0 {
			continue
		}
		sortGroup(a, set, now)
		out = append(out, group{act: a, rows: set})
	}

	allot(out, groupCap, budget, overhead)
	return out
}

// badgeLines is the allowance for the fault badge. It renders only when an
// incident is open, but it is budgeted for unconditionally: discovering it at
// paint time would cost exactly the row the budget was protecting.
const badgeLines = 1

// nonRowLines is what a frame spends before a single row is drawn: the legend
// plus the badge allowance.
//
// The legend is measured, not assumed to be one line. It is the one part of the
// frame that may WRAP — glyphs are never truncated out of it, because a legend
// missing a glyph that is on screen is worse than a legend that takes two lines
// — so at a narrow width it costs more than a row does, and a budget that
// assumed otherwise would overrun by exactly that much.
func nonRowLines(legendText string, cols int) int {
	lines := 1
	if cols > 0 {
		if w := runewidth.StringWidth(legendText); w > cols {
			lines = (w + cols - 1) / cols
		}
	}
	return lines + badgeLines
}

// allot decides how many rows each group renders, in place.
//
// The per-group cap alone is easy. The BUDGET is the interesting half, and it is shared:
// every present group has a claim on the visible rows, because a board that
// spends its whole height on unverified tasks has stopped being a board of
// sessions — which is what it was built to be.
//
// The allocation is classic fair-share with carry: walk the groups from the one
// wanting fewest rows to the one wanting most, give each an equal share of what
// is left, and let a group that wants less than its share release the difference
// to those still waiting. Small groups therefore always render whole, and the
// oversized ones split what remains — which is the outcome anyone would draw by
// hand, arrived at without a special case.
//
// Each group that will truncate also costs a footer line, and the footer is not
// optional: a row dropped without a trace is the defect the whole rowcap idiom
// exists to prevent. So a truncated group is charged for its own footer.
func allot(groups []group, groupCap, budget, overhead int) {
	want := make([]int, len(groups))
	have := make([]int, len(groups))
	for i, g := range groups {
		have[i] = len(g.rows)
		want[i] = have[i]
		if groupCap > 0 && want[i] > groupCap {
			want[i] = groupCap
		}
	}

	give := want
	if budget > 0 {
		give = fairShare(want, have, budget-overhead)
	}

	for i := range groups {
		n := give[i]
		if n < 0 {
			n = 0
		}
		if n < len(groups[i].rows) {
			groups[i].omitted = len(groups[i].rows) - n
			groups[i].rows = groups[i].rows[:n]
		}
	}
}

// fairShare distributes `budget` lines across claimants, where want[i] is the
// rows claimant i would render if unconstrained by height (already clamped by
// --limit) and have[i] is how many rows it actually holds. Returns the row count
// granted to each, in the input's order.
//
// A claimant that renders fewer rows than it HAS also renders a footer saying so,
// and that footer costs a line. Charging it against `have` rather than `want` is
// the whole subtlety here: a group already trimmed by --limit footers without the
// budget having anything to do with it, so a budget that only charged for its own
// trims would come up one line short per capped group — which is a frame that
// overruns its pane by exactly the number of capped groups.
//
// Ordering note: the shares are computed over claimants sorted by size, but the
// RESULT is returned in input order, so the board's rank order is untouched. The
// sort decides who releases surplus to whom; it never decides who renders first.
func fairShare(want, have []int, budget int) []int {
	give := make([]int, len(want))
	if budget <= 0 {
		// No room for anything. Every group renders zero rows and one footer,
		// which still tells the truth about what is there.
		return give
	}

	order := make([]int, len(want))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return want[order[a]] < want[order[b]] })

	left := budget
	for pos, idx := range order {
		remaining := len(order) - pos
		share := left / remaining
		if share < 1 {
			share = 1
		}
		n := want[idx]
		cost := n
		if n < have[idx] {
			cost++ // already truncated by --limit: it footers regardless
		}
		if cost > share {
			// Trim to fit the share. The trim itself forces a footer, so the
			// last line of the share pays for it.
			n = share - 1
			if n < 0 {
				n = 0
			}
			cost = n + 1
		}
		left -= cost
		if left < 0 {
			left = 0
		}
		give[idx] = n
	}
	return give
}

// sortGroup orders one rank's rows by its own clock direction, falling back to
// the task id (then the session id) so the order is total and a repaint of
// unchanged data is byte-identical — a monitor that reshuffles equal rows every
// two seconds flickers for no reason.
func sortGroup(a action, rows []monitor.ProjectStatusRow, now time.Time) {
	oldest := a.oldestFirst()
	sort.SliceStable(rows, func(i, j int) bool {
		ti, tj := parseTS(clock(rows[i])), parseTS(clock(rows[j]))
		if !ti.Equal(tj) {
			// An unreadable timestamp is the zero time, which would sort to the
			// very front under oldest-first. Sink it instead: a row whose clock
			// cannot be read has no claim to the top of anything.
			if ti.IsZero() != tj.IsZero() {
				return tj.IsZero()
			}
			if oldest {
				return ti.Before(tj)
			}
			return ti.After(tj)
		}
		if rows[i].TaskID != rows[j].TaskID {
			return rows[i].TaskID < rows[j].TaskID
		}
		return rows[i].SessionID < rows[j].SessionID
	})
}

// footerFor is the omission trace for one capped group, in the idiom
// src/endless/rowcap.py established: the count and the flag that shows them.
// The noun is the GROUP's, not "rows", because on this board what was dropped
// matters as much as how many — "47 more unverified" is a different sentence
// from "47 more rows".
func footerFor(g group) string {
	return fmt.Sprintf("… %d more %s (--no-limit)", g.omitted, g.noun())
}

func (g group) noun() string { return g.act.noun() }

// emptyHint is the whole frame when nothing in the project is claiming
// attention. It names the project so an unattended pane still says what it is
// looking at.
func emptyHint(project string) string {
	return "  nothing needs attention in " + project
}

// render writes one complete board frame and returns the number of TASK/SESSION
// rows drawn. A return of 0 means the frame is the empty hint, which the pane fit
// treats differently from a short real frame.
// scope is the board's project as the fault store understands it (E-1960): the
// badge below counts THIS project's open incidents plus the machine-level ones
// no project could be attributed to, never another project's. The board is
// scoped to one project in every other respect, and a badge that ignored that
// would be reporting on work the frame above it does not show.
func render(
	w io.Writer,
	project string,
	rows []monitor.ProjectStatusRow,
	groupCap, budget, cols int,
	color bool,
	now time.Time,
	scope faults.ProjectScope,
) int {
	// Two passes, and the reason is the legend. Which glyphs it names depends on
	// which groups are PRESENT, not on how many rows each renders — and the
	// budget cannot be spent until the legend's own line cost is known. So the
	// first pass groups without allotting, to learn the legend; the second spends
	// what is left. Nothing is queried twice.
	groups := buildGroups(rows, groupCap, 0, 0, now)
	if len(groups) == 0 {
		fmt.Fprintln(w, liveview.Dim(emptyHint(project), color))
		faultbadge.Render(w, cols, color, scope)
		return 0
	}
	legendText := legend(project, groups)
	groups = buildGroups(rows, groupCap, budget, nonRowLines(legendText, cols), now)

	fmt.Fprintln(w, liveview.Dim(legendText, color))

	// Width-on-demand columns, the idiom `session status` uses for its block and
	// hidden columns: a column exists only when a RENDERED row has something to
	// put in it, so a board of pure task rows is not padded with an empty session
	// gutter.
	sw := sessionColWidth(groups)

	prefix := prefixWidth + sw + ageWidth + 1
	titleBudget := cols - prefix
	if titleBudget < minTitleBudget {
		titleBudget = minTitleBudget
	}

	drawn := 0
	for _, g := range groups {
		if len(g.rows) == 0 {
			// Squeezed to nothing by the budget. The footer is the group's whole
			// presence on the board, and it still says the true thing: this rank
			// exists and holds N rows you are not seeing.
			if g.omitted > 0 {
				fmt.Fprintln(w, liveview.Dim("      "+footerFor(g), color))
			}
			continue
		}
		for _, r := range g.rows {
			line := rowPrefix(g.act, r) + sessionField(r, sw) +
				fmt.Sprintf("%*s ", ageWidth, ageCell(r, now)) +
				runewidth.Truncate(liveview.Collapse(title(r)), titleBudget, "…")
			// Final width guard. titleBudget has a floor (minTitleBudget), so on a
			// terminal too narrow for the fixed prefix plus that floor the line
			// would otherwise exceed `cols` and WRAP — and one wrapped row
			// misaligns the whole table, not just itself. Truncating the assembled
			// line degrades such a terminal to a clipped board, which is legible;
			// wrapping degrades it to noise. At any sane width this never fires.
			fmt.Fprintln(w, colorize(runewidth.Truncate(line, cols, ""), g.act, color))
			drawn++
		}
		if g.omitted > 0 {
			fmt.Fprintln(w, liveview.Dim("      "+footerFor(g), color))
		}
	}

	faultbadge.Render(w, cols, color, scope)
	return drawn
}

// prefixWidth is the fixed left block: glyph, space, type letter, space, a
// 6-wide task id, space, phase char, space.
//
// One column wider than `session status`'s otherwise-identical prefix, and the
// column buys a SPACE between the type letter and the id. That view spends the
// same column on its ◆ unsettled marker, so "T" and "E-1976" abut and every row
// opens with a token like `TE-1976` that reads as one word. This board has no
// unsettled marker — landed-vs-worktree state is a task-tree question, not an
// attention one — so the column is free, and a board built for scanning under
// load should not make the eye parse its first token.
const prefixWidth = 13

// SPECIFIED, NOT BUILT (E-2095): one unlanded claim belongs on this board.
//
// Read the paragraph above carefully before dismissing this as a contradiction.
// It rules out a per-row ◆ — "does this worktree still hold something" is a
// task-tree question. The claim below is a different one: a FINISHED task whose
// code never reached the base branch is somebody believing work shipped when it
// did not, which is squarely an attention claim. E-1115 sat `assumed` with its
// fix on an unlanded branch and the same bug was fixed again fourteen days
// later.
//
// The claim, in full, so the session that revises this board does not have to
// re-derive it:
//
//	 ⊘ 3 unlanded (Run: task unlanded)
//
//   - COUNT: the first section of `task unlanded` only — the tasks whose branch
//     still holds source, plus those whose probe could not run. NOT its second
//     section, which is a standing historical count of tasks with no landing on
//     file and would become permanent furniture.
//   - GLYPH ⊘, one column wide like every glyph in actionMeta.
//   - DRILL-DOWN: `endless task unlanded`.
//   - Suppressed entirely at zero.
//
// It is specified here rather than built because this board is heading for
// major revisions and a row designed now is a row designed to be replaced. The
// producer already exists and needs no new query: monitor.TaskLandedness over
// the project's finished tasks, keyed on `task/<id>`. Budget for it —
// measured on this repository, ~13s for 151 branches — which is why it wants a
// cached or on-demand path rather than a probe on every frame.

// minTitleBudget is the floor on the columns left for a title. Below this the
// title is all ellipsis and the row says nothing.
const minTitleBudget = 10

// rowPrefix renders the fixed left block. A session row with no claimed task
// leaves the task-shaped cells blank rather than inventing a placeholder: blank
// reads as "there is no task here", which is the truth.
func rowPrefix(a action, r monitor.ProjectStatusRow) string {
	id, letter, phase := "", " ", " "
	if r.HasTask() {
		id = "E-" + strconv.FormatInt(r.TaskID, 10)
		letter = typeLetter(r.TypeSlug)
		phase = phaseChar(r.Phase)
	}
	return fmt.Sprintf("%s %s %-6s %s ", a.icon(), letter, id, phase)
}

// title is the row's text: the task's title, or — for a live session holding no
// task — a statement of that fact, since the row would otherwise be blank.
func title(r monitor.ProjectStatusRow) string {
	if r.HasTask() {
		return r.Title
	}
	return "(no task claimed)"
}

// sessionColWidth is the session column's width across every RENDERED row, or 0
// when no rendered row has a session.
func sessionColWidth(groups []group) int {
	w := 0
	for _, g := range groups {
		for _, r := range g.rows {
			if !r.HasSession() {
				continue
			}
			if n := len(sessionLabel(r)); n > w {
				w = n
			}
		}
	}
	if w == 0 {
		return 0
	}
	return w + 1 // one trailing space
}

func sessionLabel(r monitor.ProjectStatusRow) string {
	return "ES-" + strconv.FormatInt(r.SessionID, 10)
}

func sessionField(r monitor.ProjectStatusRow, sw int) string {
	if sw == 0 {
		return ""
	}
	label := ""
	if r.HasSession() {
		label = sessionLabel(r)
	}
	return fmt.Sprintf("%-*s", sw, label)
}

// typeLetter mirrors sessionstatuscmd's: the same letters for the same types, so
// a row means the same thing on both boards.
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

// phaseChar mirrors sessionstatuscmd's phase column. No ✓ case: nothing terminal
// reaches this board, so the only values are the five phases.
func phaseChar(phase string) string {
	switch phase {
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

// legend is the header: the project's name, then only the glyphs actually
// present, in rank order. Rebuilt from the current groups every frame, so the
// snapshot and the monitor stay byte-identical by construction.
//
// The project name shares the legend's line rather than taking one of its own.
// In a pane sized to its frame every row is scarce, and an unlabelled board is
// ambiguous the moment a second one is open.
func legend(project string, groups []group) string {
	parts := make([]string, 0, len(groups))
	for _, g := range groups {
		parts = append(parts, g.act.icon()+" "+g.act.label())
	}
	return project + " · " + strings.Join(parts, "  ")
}

// colorize applies a row's intensity. Bold is reserved for actWaiting alone —
// that is the row E-1815 calls load-bearing, the one whose being missed makes
// auto-spawn's attention cap unsafe, and a "loud" that is shared with three
// other ranks is not loud. Dim marks the two ranks that need nothing from the
// user: a session that is working, and (under --all) work that is merely
// spawnable.
//
// Intensity only, never color: the 30-47 ANSI range is remapped by the user's
// theme, and a board that renders as an unreadable block on someone else's
// terminal is worse than one that renders plainly on every terminal.
func colorize(line string, a action, enabled bool) string {
	switch a {
	case actWaiting:
		return liveview.Strong(line, enabled)
	case actDoing, actReady:
		return liveview.Dim(line, enabled)
	default:
		return line
	}
}
