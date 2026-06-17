package tmuxcmd

import (
	"flag"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// runStatusLine prints one styled line on stdout for tmux to substitute
// into status-format[1]. Always exits 0 — a non-zero exit causes tmux
// to render an empty cell, which flickers and shifts width.
//
// Pane resolution order: --pane flag (set by tmux via #{pane_id}
// substitution in status-format[1]) → TMUX_PANE env (set by interactive
// shells; useful for direct invocation but NOT populated for tmux's #()
// substitution context).
func runStatusLine(args []string) {
	fs := flag.NewFlagSet("status-line", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	paneArg := fs.String("pane", "", "Tmux pane ID (overrides TMUX_PANE env)")
	if err := fs.Parse(args); err != nil {
		fmt.Print(placeholder())
		return
	}

	pane := *paneArg
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}

	status, err := monitor.GetPaneStatus(pane)
	if err != nil {
		// Real error (DB unreachable, etc.). Render placeholder; stay
		// silent on stderr to avoid log spam during interactive use.
		fmt.Print(placeholder())
		return
	}

	switch status.Kind {
	case monitor.PaneStatusActive:
		fmt.Print(format(status.Task))
	case monitor.PaneStatusNoTask:
		fmt.Print(hintNoTask())
	case monitor.PaneStatusClaudeNoSession:
		fmt.Print(hintNoSession())
	default:
		fmt.Print(placeholder())
	}
}

// format renders the active-task status line in the order:
//
//	[E-NNN] · project · type · phase · tier · status [· {E-AAA E-BBB +}]
//
// Title is intentionally omitted — bar space is scarce; title lives
// in the menu popup. Style mirrors the user's right-status "Help"
// marker (bold yellow) for the ID, then inherits status-style default
// for the rest so it matches the user's theme. Fields are omitted
// when their value is empty/nil so the row stays compact.
//
// The trailing blockers segment is appended only when the task has at
// least one active blocker; absence is the unblocked signal (E-1550).
func format(info *monitor.ActiveTaskInfo) string {
	out := fmt.Sprintf("#[fg=colour226,bold][%s]#[default]", taskIDPrefix(info))
	for _, field := range []string{
		info.ProjectName,
		info.Type,
		info.Phase,
		tierString(info.Tier),
		info.Status,
	} {
		if field != "" {
			out += " · " + field
		}
	}
	if seg := blockersSegment(info.TaskID); seg != "" {
		out += " · " + seg
	}
	return out
}

// blockersSegment renders the trailing `{E-NNNN E-MMMM +}` chip showing
// the task's active blockers (E-1550). Returns the empty string when
// there are no active blockers or when the DB query fails — silence is
// the unblocked signal, and a transient DB error should not blank the
// rest of the row.
//
// Layout:
//   - 0 blockers   → ""               (caller drops the leading " · ")
//   - 1 blocker    → "{E-N}"
//   - 2 blockers   → "{E-N E-M}"
//   - 3+ blockers  → "{E-N E-M +}"    ("+" overflow; trio order is id ASC)
//
// The whole chip (braces + IDs + spaces) is wrapped in a single orange
// color group so the shape is unmistakable at a glance and color is
// reset on exit so downstream segments inherit the theme default.
func blockersSegment(taskID int64) string {
	ids, err := monitor.GetActiveBlockers(taskID)
	if err != nil || len(ids) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("#[fg=colour208,bold]{")
	show := ids
	if len(show) > 2 {
		show = show[:2]
	}
	for i, id := range show {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "E-%d", id)
	}
	if len(ids) > 2 {
		b.WriteString(" +")
	}
	b.WriteString("}#[default]")
	return b.String()
}

// taskIDPrefix renders the bare task-id token (no brackets, no styling) for
// the status line and menu header, encoding the session's epic context
// (E-1571):
//
//   - ActiveEpicID nil                  → "E-<task>"          (no epic context)
//   - ActiveEpicID == TaskID            → "E-<epic>"          (viewing the epic itself)
//   - ActiveEpicID != TaskID            → "E-<epic>:E-<task>" (viewing a child of the epic)
//
// TaskID is the session's active_task_id (the item currently in view); when an
// epic is active and a child is in view, the epic id leads and the child
// trails.
func taskIDPrefix(info *monitor.ActiveTaskInfo) string {
	if info.ActiveEpicID == nil || *info.ActiveEpicID == info.TaskID {
		return fmt.Sprintf("E-%d", info.TaskID)
	}
	return fmt.Sprintf("E-%d:E-%d", *info.ActiveEpicID, info.TaskID)
}

// tierString formats the nullable tier integer for display, returning
// an empty string when tier is nil or 0 ("n/a") so those rows skip the
// segment entirely.
func tierString(tier *int64) string {
	if tier == nil || *tier == 0 {
		return ""
	}
	return fmt.Sprintf("t%d", *tier)
}

// hintNoTask is shown when the pane (or its window) has an Endless
// session but no active task. Inherits the theme's status-style fg
// (readable on the user's background) with italics for emphasis.
func hintNoTask() string {
	return "#[italics]claim a task ▸  endless task claim <id>#[default]"
}

// hintNoSession is shown when the focused pane is running Claude but
// no Endless session row exists for any pane in this window. Most
// commonly means the Claude hook isn't installed or the session
// predates the install.
func hintNoSession() string {
	return "#[italics]no Endless session ▸  endless setup claude-hook#[default]"
}

// placeholder is shown when no Endless context applies to this pane.
// A single dim dot keeps the bar width stable so the rest of the row
// doesn't reflow.
func placeholder() string {
	return "#[fg=colour240]·#[default]"
}
