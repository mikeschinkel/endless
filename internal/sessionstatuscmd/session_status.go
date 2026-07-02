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

// legend is the fixed header line. Kept to a single line; revisit folds into
// "plan" and there is no "other" entry, so every glyph a user acts on is
// documented here. ↑ parent is the focal's real task-tree parent; ↩ from is the
// spawning session's active task (session lineage), split apart in E-1694.
const legend = "● this  ↑ parent  ↩ from  ⟳ doing  ▶ do  ✎ plan  ◷ orphan  ☑ verify | ⊗ blocked  ⏸ blocks"

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
	case actFrom:
		return "↩"
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
		// actOther catch-all: landed tasks (E-1693) and any unrecognized status.
		// ⁇ (U+2047) measures and renders single-width (verified with
		// go-runewidth), so it aligns in the width-aware table the same as the
		// silent · it replaces. Intentionally undocumented in the legend.
		return "⁇"
	}
}

// monitorInterval is the redraw cadence for the live monitor, matching the bash
// prototype's watch loop.
const monitorInterval = 2 * time.Second

// no-task hints are shown when no focal task resolves. They mirror the tmux
// status line's PaneStatusKind hints (internal/tmuxcmd/status_line.go) so the bar
// and this view agree about "nothing here" instead of the list inventing an
// unrelated task (E-1698). Leading spaces align with the (formerly placeholder)
// list body; unlike the bar's versions these are plain text, not tmux #[...]
// format strings.
const (
	hintClaimBind = "  no active task — claim or bind one:  endless task claim <id>  /  endless task bind <id>"
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
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}

	var focal, parentSession int64
	// noTaskHint is the message shown when no focal task resolves (focal == 0, or
	// the rare focal-with-no-rows case). It mirrors the tmux status line's
	// PaneStatusKind so the two surfaces agree (E-1698). Defaults to the claim/bind
	// message; the live path overrides it with the register-session variant when
	// the pane runs Claude with no session row.
	noTaskHint := hintClaimBind
	if *taskFlag > 0 {
		// Headless mode (E-1685 verify harness): the caller names the focal task
		// directly, so there is no live tmux pane / session to resolve — and no
		// reason to force the main DB. Skip PinMainDB so DB() honors whatever
		// context was already resolved in main.go (the self-detected per-worktree
		// sandbox, or an explicit --config-dir), which is what lets the verify
		// script exercise the dependents row-set against a seeded sandbox DB.
		// --from-session supplies the spawning session id the live path would read
		// from @endless_spawned_by, so the ↩ from row stays testable headless.
		focal = *taskFlag
		parentSession = *fromSession
	} else {
		// Normal path: session/pane state lives in the main DB regardless of cwd
		// (the hook pins its writes there), so pin main before resolving. Anchor
		// focal + parent ONCE, before any refresh loop, so the view stays pinned
		// to THIS window's task as other sessions come and go (matches the
		// prototype, which resolves the focal task before entering its watch loop).
		monitor.PinMainDB()
		pane := os.Getenv("TMUX_PANE")
		var err error
		var kind monitor.PaneStatusKind
		focal, kind, err = monitor.ResolveSessionStatusFocal(pane)
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		noTaskHint = noTaskHintFor(kind)
		parentSession = monitor.ResolveSessionStatusParentSession(pane)
	}

	// --tree is an IDs-only structural view: a single frame, no legend, no monitor
	// loop. It always considers the full do/plan set, so --all/--cols don't apply.
	// --tree wins over --monitor (the live loop only drives the table view), the
	// same way it short-circuited the prototype's watch loop.
	if *tree {
		rows, err := monitor.SessionStatusRows(focal, parentSession, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		if err := renderTree(os.Stdout, rows, focal, noTaskHint); err != nil {
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		return
	}

	color := colorEnabled()

	// --monitor only makes sense against an interactive terminal (the redraw uses
	// cursor-positioning escapes). When stdout is piped/captured, degrade to a
	// single frame so scripts and pipes don't hang on an endless loop.
	if *monitorMode && term.IsTerminal(int(os.Stdout.Fd())) {
		monitorLoop(focal, parentSession, noTaskHint, *all, *cols, color)
		return
	}

	if err := renderSnapshot(os.Stdout, focal, parentSession, noTaskHint, *all, detectCols(*cols), color); err != nil {
		fmt.Fprintln(os.Stderr, "session-status:", err)
		os.Exit(1)
	}
}

// renderSnapshot queries the current rows for the anchored focal/parent and
// renders one frame to w.
func renderSnapshot(w io.Writer, focal, parentSession int64, noTaskHint string, all bool, cols int, color bool) error {
	rows, err := monitor.SessionStatusRows(focal, parentSession, all)
	if err != nil {
		return err
	}
	// Flat view only: fill each row's Dirty flag from its worktree's git state so
	// the renderer can mark the landed-vs-worktree delta with ◆ (E-1701). --tree
	// takes a separate path and skips this git cost.
	monitor.AnnotateSessionStatusDirty(rows)
	renderTo(w, rows, focal, noTaskHint, cols, color)
	return nil
}

// monitorLoop redraws the view every monitorInterval until SIGINT/SIGTERM,
// repainting only when the rendered frame changes (so an idle view doesn't
// flicker). It hides the cursor for the duration and restores it on every exit
// path. Width is re-detected each tick so a terminal resize is honored. This is
// the live `session monitor` dashboard; it loops the same snapshot renderer
// `session status` prints once.
func monitorLoop(focal, parentSession int64, noTaskHint string, all bool, colsOverride int, color bool) {
	out := os.Stdout
	fmt.Fprint(out, "\x1b[?25l")                         // hide cursor
	restore := func() { fmt.Fprint(out, "\x1b[?25h\n") } // show cursor + trailing newline

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)

	fmt.Fprint(out, "\x1b[2J\x1b[H") // clear screen, cursor home
	ticker := time.NewTicker(monitorInterval)
	defer ticker.Stop()

	prev := ""
	for {
		var b strings.Builder
		if err := renderSnapshot(&b, focal, parentSession, noTaskHint, all, detectCols(colsOverride), color); err != nil {
			restore()
			fmt.Fprintln(os.Stderr, "session-status:", err)
			os.Exit(1)
		}
		if frame := b.String(); frame != prev {
			// Home, repaint each line (erased to end-of-line), then clear to
			// end-of-display so a now-shorter frame leaves no stale rows behind.
			fmt.Fprint(out, "\x1b[H"+eraseEachLineToEOL(frame)+"\x1b[J")
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

// eraseEachLineToEOL wraps a rendered frame so that repainting it over a prior
// frame leaves no stale characters. It appends an erase-to-end-of-line (\x1b[K)
// before every newline and one after the final line, so a row whose new title
// is shorter than the prior frame's on that row does not keep the old tail. The
// caller's trailing \x1b[J still clears whole rows below a now-shorter frame;
// \x1b[J alone cannot, because it only erases from the cursor's final position
// to end-of-display, never the tails of the overwritten lines above it (E-1699).
func eraseEachLineToEOL(frame string) string {
	return strings.ReplaceAll(frame, "\n", "\x1b[K\n") + "\x1b[K"
}

// renderTo writes the legend and rows to w. focal==0 (or no rows) prints the
// no-task hint instead of an empty table — a claim/bind message (or a
// register-session message) mirroring the tmux status line, NEVER an unrelated
// task's rows (E-1698).
func renderTo(w io.Writer, rows []monitor.SessionStatusRow, focal int64, noTaskHint string, cols int, color bool) {
	fmt.Fprintln(w, dim(legend, color))
	if focal == 0 || len(rows) == 0 {
		fmt.Fprintln(w, dim(noTaskHint, color))
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
		line := fmt.Sprintf("%s %s%s%-6s %s ",
			act.icon(), typeLetter(r.TypeSlug), dirtyMark(r), "E-"+strconv.FormatInt(r.ID, 10), phaseChar(r),
		)
		line += blockField(r, bw)
		line += runewidth.Truncate(collapse(r.Title), titleBudget, "…")
		fmt.Fprintln(w, colorize(line, r.Phase, isTerminal(r.Status), color))
	}
}

// classify maps a row to its action, applying the status canonicalization from
// the plan: revisit/unplanned/needs_plan → plan; verify/unverified → verify;
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
	// stays visible (still a non-terminal status) but routes to the ⁇ other?
	// catch-all so the monitor never offers it as a fresh actionable spawn. Checked
	// after the decorations (a landed task a live session is on still reads ⟳) and
	// before the status switch.
	if r.Landed {
		return actOther
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

func sortRows(rows []monitor.SessionStatusRow) {
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

// dirtyMark is the single-column separator between the task-type letter and the
// id: ◆ (U+25C6 BLACK DIAMOND) when the row's worktree diverges from main
// (unlanded work / changes since a land — E-1701), else a plain space. Both are
// width 1, so the fixed 13-col prefix and its alignment hold either way. There
// is no legend entry for ◆ (Mike, 2026-07-01). Distinct from --tree's leading
// focal marker — different view, different glyph, no clash.
func dirtyMark(r monitor.SessionStatusRow) string {
	if r.Dirty {
		return "◆"
	}
	return " "
}

func typeLetter(slug string) string {
	switch slug {
	case "epic":
		return "E"
	case "bug":
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
