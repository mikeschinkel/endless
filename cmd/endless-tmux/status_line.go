package main

import (
	"errors"
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
		// Don't exit non-zero — tmux will keep calling us and we'd
		// rather render the placeholder than blink.
		fmt.Print(placeholder())
		return
	}

	pane := *paneArg
	if pane == "" {
		pane = os.Getenv("TMUX_PANE")
	}

	info, err := monitor.GetActiveTaskForPane(pane)
	if err != nil {
		if !errors.Is(err, monitor.ErrNoActiveTask) {
			// Real error (DB unreachable, etc.). Stay silent on stderr
			// to avoid log spam during interactive use; render placeholder.
			fmt.Fprint(os.Stderr, "")
		}
		fmt.Print(placeholder())
		return
	}

	fmt.Print(format(info))
}

// format renders the status line in the order:
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

// placeholder is shown when no active task is found. A single dim dot
// keeps the bar width stable so the rest of the row doesn't reflow.
func placeholder() string {
	return "#[fg=colour240]·#[default]"
}
