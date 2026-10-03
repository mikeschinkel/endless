// Package taskrow is the task-row vocabulary `session status` and `project
// status` share (E-2156): the action glyphs and their labels, the rule mapping a
// task's status to its action, the type letter, the phase character, the fixed
// row prefix, and the legend fit.
//
// Two views drawing the same task should draw the same row. Before this package
// each view carried its own glyph table and its own type and phase columns, and
// they had already drifted — `session status` wore the should-never-happen ⁇ on
// every `unreviewed` task, while `project status` had a ☰ for it. One table
// means a glyph added for one view is a glyph the other already knows.
//
// What stays with each view is what genuinely differs: which DECORATIONS
// outrank a status (session status's ● this / ↑ parent / ↩ from describe a
// viewing session; project status has none), the extra columns each draws, and
// the order rows are listed in.
package taskrow

import (
	"fmt"
	"strings"

	"github.com/mattn/go-runewidth"

	"github.com/mikeschinkel/endless/internal/taskstatus"
)

// Action is a row's primary classification. Declaration order is legend order,
// and `session status` also sorts by it, so a new member goes where it should
// rank rather than at the end.
type Action int

const (
	// This, Parent and From are `session status`'s session-relative
	// decorations: the viewing session's own task, its task-tree parent, and the
	// task of the session that spawned it. No other view has a viewing session,
	// so no other view produces them.
	This Action = iota
	Parent
	From
	// Waiting: a live session on the task is blocked on a permission prompt. It
	// is Doing at a higher urgency — the same fact, needing the user — which is
	// why it ranks immediately above it.
	Waiting
	// Doing: a live session holds the task.
	Doing
	// Do: a `ready` task — reviewed, waiting to start.
	Do
	// Review: a `submitted` task — a plan waiting for the owner's review. Spawn
	// and claim accept it exactly as they accept `ready` (E-2200: the plan and
	// open questions are the whole gate); approval is an optional review record,
	// and this glyph is where that record shows. It gets its own ⚑ glyph rather
	// than folding into Do (▶), so a row separates "reviewed, ready to spawn" from
	// "plan not yet reviewed". Ranked right after Do so it reads "here's what's
	// been reviewed, then here's what's one review away". ⚑ is U+2691 BLACK FLAG
	// (E-1765).
	Review
	// Plan: a task with no usable plan — `unplanned` or `revisit`.
	Plan
	// Verify: an `unverified` task, implementation awaiting the owner's verdict.
	Verify
	// Read: an `unreviewed` task — a research or brainstorm outcome delivered and
	// awaiting a read. Distinct from Verify because the act is different: read a
	// document, not run a command.
	Read
	// Orphan: an `underway` task with no live session on it — claimed work that
	// has stalled.
	Orphan
	// Landed: the task's work has merged (E-1693). ⏚ is U+23DA EARTH GROUND,
	// "landed/grounded". E-1750 split it out of the old ⁇ catch-all.
	Landed
	// Unknown: a status Classify does not recognize — a should-never-happen
	// net (every real status is handled), so ⁇ (U+2047) in a legend flags an
	// unhandled status slipping through.
	Unknown
	// Done: a terminal-status task (confirmed/assumed/declined/obsolete/
	// completed) whose work never landed — E-1871. Before it, such rows fell
	// through to Unknown, so the most ordinary rows in the database wore ⁇ and
	// drowned out its diagnostic value. ⇥ is U+21E5 RIGHTWARDS ARROW TO BAR.
	//
	// ⏚ landed WINS over ⇥ (see Classify), so ⇥ marks only closed work that
	// never merged — the informative case. Last in rank: an unhandled status
	// deserves more prominence than a finished task.
	Done
)

// meta is the one glyph table, indexed by Action. Every glyph measures one
// column (asserted in TestGlyphsAreSingleWidth), which is what keeps the fixed
// row prefix aligned.
var meta = [...]struct{ icon, label string }{
	This:    {"●", "this"},
	Parent:  {"↑", "parent"},
	From:    {"↩", "from"},
	Waiting: {"⚠", "waiting"},
	Doing:   {"⟳", "doing"},
	Do:      {"▶", "do"},
	Review:  {"⚑", "review"},
	Plan:    {"✎", "plan"},
	Verify:  {"☑", "verify"},
	Read:    {"☰", "read"},
	Orphan:  {"◷", "orphan"},
	Landed:  {"⏚", "landed"},
	Unknown: {"⁇", "unknown"},
	Done:    {"⇥", "closed"},
}

// Count is the number of actions, for callers that keep a per-action array.
const Count = len(meta)

func (a Action) Icon() string  { return meta[a].icon }
func (a Action) Label() string { return meta[a].label }

// Actions is every action in declaration order.
func Actions() []Action {
	out := make([]Action, Count)
	for i := range out {
		out[i] = Action(i)
	}
	return out
}

// Classify maps a task's own facts to its action, below whatever decorations a
// view applies first. Landed outranks a terminal status (⏚ over ⇥, so ⇥ marks
// only closed work that never merged), and a terminal status outranks the
// status switch.
func Classify(status string, landed bool) Action {
	if landed {
		return Landed
	}
	if IsTerminal(status) {
		return Done
	}
	switch status {
	case "ready":
		return Do
	case "submitted":
		return Review
	case "unplanned", "needs_plan", "revisit":
		return Plan
	case "verify", "unverified":
		return Verify
	case "unreviewed":
		return Read
	case "underway", "in_progress":
		return Orphan
	default:
		return Unknown
	}
}

// IsTerminal reports whether a status is a terminus rather than a verb.
func IsTerminal(status string) bool {
	return taskstatus.Has(taskstatus.Terminal, status)
}

// TypeLetter is the single-column task-type indicator.
func TypeLetter(slug string) string {
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

// PhaseChar is the single-column phase indicator: ✓ for a terminal task, else
// one character per phase.
func PhaseChar(phase string, terminal bool) string {
	if terminal {
		return "✓"
	}
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

// PrefixWidth is the fixed left block's width: action glyph, space, type
// letter, a one-column mark, a six-wide id, space, phase character, space.
const PrefixWidth = 13

// Prefix renders the fixed left block. mark is the one-column slot between the
// type letter and the id — `session status` puts its unsettled marker there; a
// view with nothing to say passes a space, which also keeps the letter from
// abutting the id. id is already padded to six columns (it may carry color
// escapes outside the padding, which is why it is not padded here).
func Prefix(a Action, typeSlug, mark, id, phase string) string {
	return fmt.Sprintf("%s %s%s%s %s ", a.Icon(), TypeLetter(typeSlug), mark, id, phase)
}

// PadID renders a task id as `E-NNN` padded to its six-column slot.
func PadID(id int64) string {
	return fmt.Sprintf("%-6s", fmt.Sprintf("E-%d", id))
}

// LegendEntry is one glyph and its label in a legend line.
type LegendEntry struct{ Icon, Label string }

// JoinLegend renders entries as icon+sep+label, separated by between.
func JoinLegend(entries []LegendEntry, sep, between string) string {
	parts := make([]string, 0, len(entries))
	for _, e := range entries {
		parts = append(parts, e.Icon+sep+e.Label)
	}
	return strings.Join(parts, between)
}

// FitLegend fits a legend to cols (E-2188) rather than leaving it to
// soft-wrap: the NORMAL form (`<icon> <label>`, two spaces apart) when it fits,
// the COMPACT form (`<icon><label>`, one space apart) when only that fits, and
// the normal form again — wrapping — when neither does, since a legend that
// must wrap anyway should wrap in the easier-to-read form. prefix is text that
// leads the line (a project name, say) and counts toward the width.
//
// Never truncated: cutting the legend off drops the meaning of glyphs that are
// on screen.
func FitLegend(prefix string, entries []LegendEntry, cols int) string {
	normal := prefix + JoinLegend(entries, " ", "  ")
	if runewidth.StringWidth(normal) <= cols {
		return normal
	}
	if compact := prefix + JoinLegend(entries, "", " "); runewidth.StringWidth(compact) <= cols {
		return compact
	}
	return normal
}
