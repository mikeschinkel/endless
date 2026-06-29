// Package sessionnextcmd implements `endless-go session-next`: the read
// command behind the Python `endless session next` verb (E-1465). It resolves
// the focal task for the current tmux window, gathers the cross-session
// what's-next rows via monitor.SessionNextRows, and renders them as a compact,
// width-aware, single-spaced table.
//
// The Python verb shells out to this subcommand inheriting the terminal's
// stdout, so width and color are detected here against the real tty. Pin to the
// main DB happens in the endless-go dispatcher (sessions live in main).
package sessionnextcmd

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mattn/go-runewidth"
	"golang.org/x/term"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// legend is the fixed header line. Intentionally kept to a single ~80-col line
// (Mike's constraint, E-1465); revisit folds into "plan" and there is no
// "other" entry, so every glyph a user acts on is documented here.
const legend = "● this  ↑ parent  ⟳ doing  ▶ do  ✎ plan  ◷ orphan  ☑ verify | ⊗ blocked  ⏸ blocks"

// fallbackCols is used when the terminal width can't be detected (output not a
// tty, no --cols, no $COLUMNS). Matches the bash prototype's default.
const fallbackCols = 90

// action is the primary classification of a row, in sort-rank order. The icon
// and rank both derive from it. Parent outranks the in-flight/do/plan/etc.
// statuses; the bash prototype's icon-glyph sort had a latent bug (it matched
// '⤴' but rendered '↑'), avoided here by ranking on the enum, not the glyph.
type action int

const (
	actThis action = iota
	actParent
	actDoing
	actDo
	actPlan
	actVerify
	actOrphan
	actOther
)

func (a action) icon() string {
	switch a {
	case actThis:
		return "●"
	case actParent:
		return "↑"
	case actDoing:
		return "⟳"
	case actDo:
		return "▶"
	case actPlan:
		return "✎"
	case actVerify:
		return "☑"
	case actOrphan:
		return "◷"
	default:
		return "·"
	}
}

// watchInterval is the redraw cadence for --watch, matching the bash prototype.
const watchInterval = 2 * time.Second

func Run(args []string) {
	fs := flag.NewFlagSet("session-next", flag.ContinueOnError)
	all := fs.Bool("all", false, "include done-work (terminal-status) rows")
	watch := fs.Bool("watch", false, "redraw every 2s until interrupted (Ctrl-C)")
	tree := fs.Bool("tree", false, "render do/plan tasks as an IDs-only implementation-order tree")
	cols := fs.Int("cols", 0, "terminal width override (0 = auto-detect)")
	focalFlag := fs.Int64("focal", 0, "explicit focal task id (headless: bypasses tmux/session resolution and reads the resolved DB context — the self-detected sandbox or --config-dir — instead of pinning the main DB; intended for tests)")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	var focal, parentSession int64
	if *focalFlag > 0 {
		// Headless mode (E-1685 verify harness): the caller names the focal task
		// directly, so there is no live tmux pane / session to resolve — and no
		// reason to force the main DB. Skip PinMainDB so DB() honors whatever
		// context was already resolved in main.go (the self-detected per-worktree
		// sandbox, or an explicit --config-dir), which is what lets the verify
		// script exercise the dependents row-set against a seeded sandbox DB.
		focal = *focalFlag
	} else {
		// Normal path: session/pane state lives in the main DB regardless of cwd
		// (the hook pins its writes there), so pin main before resolving. Anchor
		// focal + parent ONCE, before any refresh loop, so the view stays pinned
		// to THIS window's task as other sessions come and go (matches the
		// prototype, which resolves the focal task before entering its watch loop).
		monitor.PinMainDB()
		pane := os.Getenv("TMUX_PANE")
		var err error
		focal, err = monitor.ResolveSessionNextFocal(pane)
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-next:", err)
			os.Exit(1)
		}
		parentSession = monitor.ResolveSessionNextParentSession(pane)
	}

	// --tree is an IDs-only structural view: a single frame, no legend, no watch
	// loop. It always considers the full do/plan set, so --all/--cols don't apply.
	if *tree {
		rows, err := monitor.SessionNextRows(focal, parentSession, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-next:", err)
			os.Exit(1)
		}
		if err := renderTree(os.Stdout, rows, focal); err != nil {
			fmt.Fprintln(os.Stderr, "session-next:", err)
			os.Exit(1)
		}
		return
	}

	color := colorEnabled()

	// --watch only makes sense against an interactive terminal (the redraw uses
	// cursor-positioning escapes). When stdout is piped/captured, degrade to a
	// single frame so scripts and pipes don't hang on an endless loop.
	if *watch && term.IsTerminal(int(os.Stdout.Fd())) {
		watchLoop(focal, parentSession, *all, *cols, color)
		return
	}

	if err := renderSnapshot(os.Stdout, focal, parentSession, *all, detectCols(*cols), color); err != nil {
		fmt.Fprintln(os.Stderr, "session-next:", err)
		os.Exit(1)
	}
}

// renderSnapshot queries the current rows for the anchored focal/parent and
// renders one frame to w.
func renderSnapshot(w io.Writer, focal, parentSession int64, all bool, cols int, color bool) error {
	rows, err := monitor.SessionNextRows(focal, parentSession, all)
	if err != nil {
		return err
	}
	renderTo(w, rows, focal, cols, color)
	return nil
}

// watchLoop redraws the view every watchInterval until SIGINT/SIGTERM, repainting
// only when the rendered frame changes (so an idle view doesn't flicker). It
// hides the cursor for the duration and restores it on every exit path. Width is
// re-detected each tick so a terminal resize is honored.
func watchLoop(focal, parentSession int64, all bool, colsOverride int, color bool) {
	out := os.Stdout
	fmt.Fprint(out, "\x1b[?25l")                         // hide cursor
	restore := func() { fmt.Fprint(out, "\x1b[?25h\n") } // show cursor + trailing newline

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	fmt.Fprint(out, "\x1b[2J\x1b[H") // clear screen, cursor home
	ticker := time.NewTicker(watchInterval)
	defer ticker.Stop()

	prev := ""
	for {
		var b strings.Builder
		if err := renderSnapshot(&b, focal, parentSession, all, detectCols(colsOverride), color); err != nil {
			restore()
			fmt.Fprintln(os.Stderr, "session-next:", err)
			os.Exit(1)
		}
		if frame := b.String(); frame != prev {
			// Home, repaint, then clear to end-of-display so a now-shorter frame
			// leaves no stale rows behind.
			fmt.Fprint(out, "\x1b[H"+frame+"\x1b[J")
			prev = frame
		}
		select {
		case <-sigs:
			restore()
			return
		case <-ticker.C:
		}
	}
}

// renderTo writes the legend and rows to w. focal==0 (or no rows) prints a short
// hint instead of an empty table.
func renderTo(w io.Writer, rows []monitor.SessionNextRow, focal int64, cols int, color bool) {
	fmt.Fprintln(w, dim(legend, color))
	if focal == 0 || len(rows) == 0 {
		fmt.Fprintln(w, dim("  (no active task for this window)", color))
		return
	}

	sortRows(rows)

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

	// Fixed prefix width = "I L NNNNNN P " = 13 cols (icon, type letter, the
	// 6-wide left-justified E-id, phase char, each single-spaced).
	const prefixWidth = 13
	blockSeg := blockSegWidth(bw)
	titleBudget := cols - prefixWidth - blockSeg
	if titleBudget < 10 {
		titleBudget = 10
	}

	for _, r := range rows {
		act := classify(r)
		line := fmt.Sprintf("%s %s %-6s %s ",
			act.icon(), typeLetter(r.TypeSlug), "E-"+strconv.FormatInt(r.ID, 10), phaseChar(r),
		)
		line += blockField(r, bw)
		line += runewidth.Truncate(collapse(r.Title), titleBudget, "…")
		fmt.Fprintln(w, colorize(line, r.Phase, isTerminal(r.Status), color))
	}
}

// classify maps a row to its action, applying the status canonicalization from
// the plan: revisit/unplanned/needs_plan → plan; verify/unverified → verify;
// underway/in_progress → working (→ orphan when not in-flight); ready → do
// REGARDLESS of plan text (ED-1522, confirmed by Mike). Focal/parent/in-flight
// decorations take precedence over status.
func classify(r monitor.SessionNextRow) action {
	switch {
	case r.IsFocal:
		return actThis
	case r.IsParent:
		return actParent
	case r.InFlight:
		return actDoing
	}
	switch r.Status {
	case "ready":
		return actDo
	case "unplanned", "needs_plan", "revisit":
		return actPlan
	case "verify", "unverified":
		return actVerify
	case "underway", "in_progress":
		return actOrphan
	default:
		return actOther
	}
}

func sortRows(rows []monitor.SessionNextRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aj := classify(rows[i]), classify(rows[j])
		if ai != aj {
			return ai < aj
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

func typeLetter(slug string) string {
	switch slug {
	case "epic":
		return "E"
	case "bug":
		return "B"
	case "research":
		return "R"
	case "brainstorm":
		return "Z"
	default:
		return "T"
	}
}

// phaseChar is the single-column phase indicator: ✓ for done-work (focal/parent
// rows can be terminal), else a per-phase glyph.
func phaseChar(r monitor.SessionNextRow) string {
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
func blockField(r monitor.SessionNextRow, bw int) string {
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

func isTerminal(status string) bool {
	switch status {
	case "confirmed", "assumed", "declined", "obsolete", "completed":
		return true
	}
	return false
}

func detectCols(override int) int {
	if override > 0 {
		return override
	}
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	if v := os.Getenv("COLUMNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return fallbackCols
}

func colorEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// collapse squeezes internal whitespace runs to single spaces so multi-line or
// padded titles render on one line (matches `endless session list`).
func collapse(s string) string {
	out := make([]rune, 0, len(s))
	prevSpace := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if !prevSpace {
				out = append(out, ' ')
			}
			prevSpace = true
			continue
		}
		out = append(out, r)
		prevSpace = false
	}
	return string(out)
}

// ANSI helpers. Phase-by-intensity: urgent bold, later/maybe dim, terminal rows
// dim, everything else normal. Kept to bold/dim (SGR 1/2) so it reads on any
// theme without color-profile guessing — lipgloss is reserved for the future
// TUI (E-859/E-1622), out of scope here.
const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
	ansiDim   = "\x1b[2m"
)

func colorize(line, phase string, terminal, enabled bool) string {
	if !enabled {
		return line
	}
	switch {
	case terminal:
		return ansiDim + line + ansiReset
	case phase == "urgent":
		return ansiBold + line + ansiReset
	case phase == "later", phase == "maybe":
		return ansiDim + line + ansiReset
	default:
		return line
	}
}

func dim(s string, enabled bool) string {
	if !enabled {
		return s
	}
	return ansiDim + s + ansiReset
}
