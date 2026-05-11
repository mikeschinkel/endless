package main

import (
	"flag"
	"fmt"
	"os"

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
//	[E-NNN] · project · type · phase · tier · status
//
// Title is intentionally omitted — bar space is scarce; title lives
// in the menu popup. Style mirrors the user's right-status "Help"
// marker (bold yellow) for the ID, then inherits status-style default
// for the rest so it matches the user's theme. Fields are omitted
// when their value is empty/nil so the row stays compact.
func format(info *monitor.ActiveTaskInfo) string {
	out := fmt.Sprintf("#[fg=colour226,bold][E-%d]#[default]", info.TaskID)
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
	return out
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
