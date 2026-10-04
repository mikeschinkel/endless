package projectstatuscmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/faultrow"
	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/liveview"
	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/taskrow"
	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// The layout and rendering behind `endless project status` and `endless
// project monitor` (E-2156, replacing E-1976's session-and-rank display).
//
// Three lists in one frame, in this order, with no separators or headers
// between them:
//
//  1. urgent — every urgent task.
//  2. epics — every epic in now/next.
//  3. everything else in now/next.
//
// Each task appears once, in the highest list it qualifies for: an urgent epic
// is in list 1 and never also in list 2. No separator is needed because the
// rows identify themselves — list 1 carries the urgent phase glyph, list 2 the
// epic type letter.
//
// Only list 3 is ever truncated, and only by `project monitor`, by pane height:
// it takes whatever the frame has left, and grows when the terminal does.
// `project status` truncates nothing. Pane pressure is deliberate — it is what
// gets work demoted to `later`.
//
// An interim design by intent: a scrolling TUI is expected to replace it.

// list is which of the three lists a row belongs to, in render order.
type list int

const (
	listUrgent list = iota
	listEpics
	listOther
	listCount
)

// listNames are the --json spellings, so a consumer can regroup the rows
// without re-deriving the rule.
var listNames = [listCount]string{"urgent", "epics", "other"}

// listOf places a row in the highest list it qualifies for.
func listOf(r monitor.ProjectStatusRow) list {
	switch {
	case r.Phase == "urgent":
		return listUrgent
	case r.TypeSlug == "epic":
		return listEpics
	default:
		return listOther
	}
}

// sortKey is --sort's value. Both orders are reverse-chronological: `updated`
// by when the task last changed, `id` by when it was filed. Mike wants to try
// both in use, which is the flag's whole purpose.
type sortKey string

const (
	sortUpdated sortKey = "updated"
	sortID      sortKey = "id"
)

func validSortKey(s string) bool {
	return s == string(sortUpdated) || s == string(sortID)
}

// tsLayouts are the two spellings a timestamp arrives in. Endless writes the
// first; SQLite's own datetime() writes the second, which is what a hand-seeded
// fixture or an older row carries. Compared as parsed times, not strings, so a
// mix of the two cannot misorder.
var tsLayouts = []string{"2006-01-02T15:04:05", "2006-01-02 15:04:05"}

// parseTS reads a stored timestamp as UTC; ok is false when it cannot.
func parseTS(s string) (t time.Time, ok bool) {
	s = strings.TrimSpace(s)
	for _, layout := range tsLayouts {
		if p, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return p, true
		}
	}
	return time.Time{}, false
}

// sortRows orders one list newest first by key, falling back to the id
// (descending, which is also newest first) so the order is total and a repaint
// of unchanged data is byte-identical. An unreadable timestamp sinks: a row
// whose clock cannot be read has no claim to the top.
func sortRows(rows []monitor.ProjectStatusRow, key sortKey) {
	sort.SliceStable(rows, func(i, j int) bool {
		if key == sortUpdated {
			ti, iok := parseTS(rows[i].TaskUpdated)
			tj, jok := parseTS(rows[j].TaskUpdated)
			if iok != jok {
				return iok
			}
			if !ti.Equal(tj) {
				return ti.After(tj)
			}
		}
		return rows[i].TaskID > rows[j].TaskID
	})
}

// partition splits rows into the three lists, each sorted.
func partition(rows []monitor.ProjectStatusRow, key sortKey) [listCount][]monitor.ProjectStatusRow {
	var out [listCount][]monitor.ProjectStatusRow
	for _, r := range rows {
		l := listOf(r)
		out[l] = append(out[l], r)
	}
	for l := range out {
		sortRows(out[l], key)
	}
	return out
}

// classify is the row's action glyph: `session status`'s vocabulary and its
// status rule (taskrow.Classify), with the one decoration this view has. There
// is no viewing session, so ● this, ↑ parent and ↩ from never appear.
//
// A live session changes an `underway` row only: ⟳ when a live session holds
// it, ◷ when none does — the only signal that claimed work has stalled — and ⚠
// when the session holding it is blocked on a permission prompt, the same fact
// at a higher urgency. Every other status keeps its own glyph whether or not a
// session is still open on it, so an `unverified` task whose session is idle
// still reads ☑.
func classify(r monitor.ProjectStatusRow) taskrow.Action {
	if r.Status == string(taskstatus.Underway) && r.LiveSession {
		if r.Prompted {
			return taskrow.Waiting
		}
		return taskrow.Doing
	}
	return taskrow.Classify(r.Status, r.Landed)
}

// minTitleBudget is the floor on the columns left for a title. Below this the
// title is all ellipsis and the row says nothing.
const minTitleBudget = 10

// frameOpts is one render's shape.
type frameOpts struct {
	// budget is the lines the destination can show; <= 0 means unbounded.
	budget int
	// truncate lets list 3 be cut to fit budget. False for `project status`,
	// which truncates nothing at any height.
	truncate bool
	cols     int
	color    bool
	sort     sortKey
	// emptyPhrase names the phases the frame covers, for the empty hint.
	emptyPhrase string
	// styles is each list's colors; the zero value is defaultListStyles.
	styles *listStyles
}

// legendLines is how many terminal lines the legend occupies at cols. The
// legend may wrap — glyphs are never truncated out of it — so at a narrow width
// it can cost more than one line, and a budget that assumed otherwise would
// overrun by exactly that much.
func legendLines(text string, cols int) int {
	if cols <= 0 {
		return 1
	}
	if w := runewidth.StringWidth(text); w > cols {
		return (w + cols - 1) / cols
	}
	return 1
}

// legend is the header: the project's name, then the glyphs present on the
// rendered rows, in taskrow order. The name shares the line rather than taking
// one of its own; in a pane every row is scarce.
func legend(project string, lists [listCount][]monitor.ProjectStatusRow, cols int) string {
	var present [taskrow.Count]bool
	for _, rows := range lists {
		for _, r := range rows {
			present[classify(r)] = true
		}
	}
	var entries []taskrow.LegendEntry
	for _, a := range taskrow.Actions() {
		if present[a] {
			entries = append(entries, taskrow.LegendEntry{Icon: a.Icon(), Label: a.Label()})
		}
	}
	return taskrow.FitLegend(project+" · ", entries, cols)
}

// footerFor is the trace list 3 leaves when it is cut: how many rows the pane
// could not fit and where to see them. A row dropped without a trace is the
// defect the footer exists to prevent.
func footerFor(omitted int) string {
	return fmt.Sprintf("… %d more (project status)", omitted)
}

// emptyHint is the whole frame when no task is in the covered phases. It names
// the project so an unattended pane still says what it is looking at.
func emptyHint(project, phrase string) string {
	return "  no open tasks in " + phrase + " in " + project
}

// render writes one complete frame and returns the number of task rows drawn.
// A return of 0 means the frame is the empty hint.
//
// The frame carries no clock. It is a pure function of the row set, so two
// renders of unchanged data are byte-identical and the monitor repaints only
// when a row actually changes — motion in the corner of the eye means
// something.
//
// scope is the frame's project as the fault store understands it (E-1960): the
// fault row counts THIS project's open incidents plus the machine-level ones,
// never another project's.
func render(
	w io.Writer,
	project string,
	rows []monitor.ProjectStatusRow,
	o frameOpts,
	scope faults.ProjectScope,
) int {
	// The fault row is rendered first, into a buffer, so the budget can reserve
	// exactly the lines it takes — none when no incident is open — and the
	// third list gets every line the pane has left.
	var faultBuf strings.Builder
	faultrow.Render(&faultBuf, o.cols, o.color, scope)
	faultLines := strings.Count(faultBuf.String(), "\n")

	lists := partition(rows, o.sort)
	if len(rows) == 0 {
		fmt.Fprintln(w, liveview.Dim(emptyHint(project, o.emptyPhrase), o.color))
		io.WriteString(w, faultBuf.String())
		return 0
	}

	// Fit list 3. The legend is measured over the FULL row set first; cutting
	// list 3 can only remove glyphs from it, so the legend actually drawn is
	// never longer than the one budgeted for.
	omitted := 0
	if o.truncate && o.budget > 0 {
		fixed := legendLines(legend(project, lists, o.cols), o.cols) + faultLines +
			len(lists[listUrgent]) + len(lists[listEpics])
		room := o.budget - fixed
		if other := lists[listOther]; len(other) > room {
			keep := room - 1 // the footer takes a line
			if keep < 0 {
				keep = 0
			}
			omitted = len(other) - keep
			lists[listOther] = other[:keep]
		}
	}

	fmt.Fprintln(w, liveview.Dim(legend(project, lists, o.cols), o.color))

	titleBudget := o.cols - taskrow.PrefixWidth
	if titleBudget < minTitleBudget {
		titleBudget = minTitleBudget
	}

	drawn := 0
	for l, set := range lists {
		for _, r := range set {
			fmt.Fprintln(w, o.styles.colorize(rowLine(r, titleBudget, o.cols), r, list(l), o.cols, o.color))
			drawn++
		}
	}
	if omitted > 0 {
		fmt.Fprintln(w, liveview.Dim("      "+footerFor(omitted), o.color))
	}

	io.WriteString(w, faultBuf.String())
	return drawn
}

// rowLine is one task row: `session status`'s prefix — action glyph, type
// letter, id, phase — then the title. The one-column slot between the type
// letter and the id holds `session status`'s unsettled marker; this view leaves
// it blank, which also keeps the letter from abutting the id.
//
// The final truncate is a width guard: titleBudget has a floor, so on a
// terminal too narrow for the prefix plus that floor the line would otherwise
// wrap, and one wrapped row misaligns the whole table.
func rowLine(r monitor.ProjectStatusRow, titleBudget, cols int) string {
	line := taskrow.Prefix(classify(r), r.TypeSlug, " ", taskrow.PadID(r.TaskID),
		taskrow.PhaseChar(r.Phase, false)) +
		runewidth.Truncate(liveview.Collapse(r.Title), titleBudget, "…")
	return runewidth.Truncate(line, cols, "")
}

// SPECIFIED, NOT BUILT (E-2095): one unlanded claim belongs in this frame.
//
// It rules out a per-row ◆ — "does this worktree still hold something" is a
// task-tree question, which is why rowLine leaves that slot blank. The claim
// below is a different one: a FINISHED task whose code never reached the base
// branch is somebody believing work shipped when it did not, which is squarely
// an attention claim. E-1115 sat `assumed` with its fix on an unlanded branch
// and the same bug was fixed again fourteen days later.
//
// The claim, in full, so the session that revises `project status` does not have
// to re-derive it:
//
//	 ⊘ 3 unlanded (Run: task unlanded)
//
//   - COUNT: the first section of `task unlanded` only — the tasks whose branch
//     still holds source, plus those whose probe could not run. NOT its second
//     section, which is a standing historical count of tasks with no landing on
//     file and would become permanent furniture.
//   - GLYPH ⊘, one column wide like every glyph in taskrow.
//   - DRILL-DOWN: `endless task unlanded`.
//   - Suppressed entirely at zero.
//
// It is specified here rather than built because `project status` is heading for
// a scrolling TUI and a row designed now is a row designed to be replaced. The
// producer already exists and needs no new query: monitor.TaskLandedness over
// the project's finished tasks, keyed on `task/<id>`. Budget for it —
// measured on this repository, ~13s for 151 branches — which is why it wants a
// cached or on-demand path rather than a probe on every frame.

// listStyles is each list's background and foreground, as 256-color indexes.
// The background is what tells the three lists apart without separator rows.
// A negative index leaves that attribute at the terminal's default, which is
// how a list goes without a background.
type listStyles [listCount]struct{ bg, fg int }

// defaultListStyles is Mike's pick (E-2156): the theme's red, green and yellow
// (palette 1, 2, 3) behind near-black 232, so the tint follows the user's
// palette rather than fighting it. `project_status.colors` in config overrides
// any of them.
var defaultListStyles = listStyles{
	listUrgent: {1, 232},
	listEpics:  {2, 232},
	listOther:  {3, 232},
}

// colorize paints a row in its list's colors, padded to the full width so the
// band reads as one block. ⚠ and urgent rows are also bold — a session blocked
// on the user is the loudest thing a row can say. With color off the line is
// returned untouched. A nil receiver uses defaultListStyles.
func (st *listStyles) colorize(line string, r monitor.ProjectStatusRow, l list, cols int, enabled bool) string {
	if !enabled {
		return line
	}
	if st == nil {
		st = &defaultListStyles
	}
	style := st[l]
	sgr := ""
	if r.Phase == "urgent" || classify(r) == taskrow.Waiting {
		sgr = liveview.Bold
	}
	if style.bg >= 0 {
		sgr += fmt.Sprintf("\x1b[48;5;%dm", style.bg)
		if pad := cols - runewidth.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
	}
	if style.fg >= 0 {
		sgr += fmt.Sprintf("\x1b[38;5;%dm", style.fg)
	}
	if sgr == "" {
		return line
	}
	return sgr + line + liveview.Reset
}
