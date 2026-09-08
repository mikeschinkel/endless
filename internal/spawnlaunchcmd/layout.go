package spawnlaunchcmd

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
)

// layout.go owns the canonical Endless working layout — the three-pane
// arrangement `task spawn` builds around the pane Claude runs in.
//
// It is here, and exported through a verb of its own, because spawn is no
// longer the only caller (E-2106). `session resume` takes over the CURRENT
// tmux window, `session goto --resume` opens a NEW one, and `task claim` from
// a plain shell pane takes that pane over; all three end up with a window
// holding exactly one Claude pane and want the same two panes beside it.
// Reimplementing the splits at each call site is how three copies drift, so
// the builder takes an anchor pane rather than being welded to the window
// spawn had just created.

// runSpawnLayout implements `endless-go spawn-layout --pane <id> --cwd <dir>`:
// the layout half of spawn-window, for a caller that already HAS the pane.
//
// Exits 0 even when tmux refuses a split. The callers are recovery paths — a
// resume, a re-claim — and the layout is a convenience around the session they
// are really there to start. A window that came up with fewer panes than
// intended is a worse window; a resume that refused to happen because a split
// failed is a lost session. buildLayoutAround reports what went wrong on
// stderr, same as it does inside spawn.
func runSpawnLayout(args []string) {
	fs := flag.NewFlagSet("spawn-layout", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	var (
		pane = fs.String("pane", "", "tmux pane id the layout is built around")
		cwd  = fs.String("cwd", "", "Working directory the window belongs to")
	)
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	if *pane == "" {
		fail("spawn-layout: --pane is required")
	}
	buildLayoutAround(*pane, *cwd)
}

// buildLayoutAround turns a window whose only pane is `anchor` into the
// canonical Endless working layout (E-1851): the anchor on the left at half
// width and full height, `endless session monitor` top-right, and a bare
// interactive shell bottom-right for ad-hoc endless commands. Focus ends on the
// anchor.
//
// The two right panes start in the PROJECT dir, not the caller's cwd — see
// projectDirFor for why the DB routing makes that the correct home for both.
//
// Split order matters. The SHELL pane is created first and the monitor is
// inserted ABOVE it (-b), rather than the reverse: `session monitor` shrinks its
// own pane to its frame on first paint (see sessionstatuscmd.fitPaneToFrame), so
// creating the monitor first and then splitting it would race that shrink and
// leave the shell with whatever few rows survived. Splitting the shell can't
// race anything. The monitor is therefore left at tmux's even split and sizes
// itself a moment later — which is also why nothing here has to guess a height
// it has no way to know.
//
// Best-effort throughout, matching the option-setting path: the anchor pane is
// the load-bearing part of every caller, so a tmux failure here surfaces on
// stderr and returns, never fails the launch.
func buildLayoutAround(anchor, cwd string) {
	paneDir := projectDirFor(cwd)

	shellPane, err := tmuxRunOut(splitWindowArgs(anchor, true, false, paneDir, 0, nil)...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "layout: shell pane: %v\n", err)
		return
	}

	if _, err = tmuxRunOut(splitWindowArgs(shellPane, false, true, paneDir, 0, monitorCommand())...); err != nil {
		fmt.Fprintf(os.Stderr, "layout: monitor pane: %v\n", err)
		// Fall through: a 2-pane window still wants focus back on the anchor.
	}

	if err = tmuxRun(selectPaneArgs(anchor)...); err != nil {
		fmt.Fprintf(os.Stderr, "layout: focus anchor pane: %v\n", err)
	}
}

// monitorCommand is the argv run in the layout's monitor pane. `endless session
// monitor` is the verb a user would type; it is resolved to an absolute path
// when possible so the pane doesn't depend on tmux's PATH matching the
// spawner's, and left bare (letting tmux's execvp report the failure in-pane)
// when the CLI isn't on PATH at all.
func monitorCommand() []string {
	bin, err := exec.LookPath("endless")
	if err != nil {
		bin = "endless"
	}
	return []string{bin, "session", "monitor"}
}
