// Package spawnlaunchcmd implements the `endless-go spawn-window` /
// `endless-go spawn-launch` subcommands — the multiplexer seam through which
// `endless task spawn` launches Claude.
//
// Two entry points, dispatched on the first arg:
//
//	spawn-window  Outer orchestrator (the only verb Python calls). Writes a
//	              JSON launch-spec file, then creates the tmux window whose
//	              command is `endless-go spawn-launch --spec <path>`. Returns
//	              once the window exists.
//	spawn-layout  The layout half of spawn-window, for a caller that already
//	              has the Claude pane: `session resume` (current window),
//	              `session goto --resume` (new window) and `task claim` from a
//	              shell pane all reach it (E-2106).
//	spawn-launch  Inner — runs inside the freshly created window. Sets the
//	              @endless_* window options (BEFORE exec, so SessionStart's
//	              option reads never race), reads+deletes the handoff and spec
//	              files, then `syscall.Exec`s the real claude binary with the
//	              handoff as its positional prompt argument.
//
// Why this shape (rather than send-keys / paste): keystroke injection is
// fragile (a stray tmux prefix keystroke mid-spawn corrupts input) and a footgun
// for the planned multiplexer-driver refactor. Launching Claude as the window's
// *command* removes every send-keys call from spawn. All tmux specifics live in
// tmux_driver.go so a future backend (Herdr / a built-in multiplexer) can be
// swapped in without touching the argv/exec logic. Parameters travel in a
// launch-spec file, not tmux `-e`, so no transient value (handoff path, task
// title) leaks into the session environment and nothing needs shell-quoting onto
// a command line.
//
// This subcommand touches no DB: the session→task binding is still written by
// the spawned session's SessionStart hook (internal/hookcmd), which reads the
// @endless_* options this launcher sets.
package spawnlaunchcmd

import (
	"strings"

	"github.com/mikeschinkel/endless/internal/refusal"
)

// Run dispatches on the top-level verb (`spawn-window` or `spawn-launch`),
// which the endless-go dispatcher passes through verbatim. Unlike the nested
// subcommands (`tmux apply`), the two spawn verbs are sibling top-level names,
// so the dispatcher hands the verb here rather than folding it into args.
func Run(verb string, args []string) {
	switch verb {
	case "spawn-window":
		runSpawnWindow(args)
	case "spawn-launch":
		runSpawnLaunch(args)
	case "spawn-layout":
		runSpawnLayout(args)
	default:
		// Nobody normally reads this: cmd/endless-go/main.go dispatches here
		// only for the three verbs above, so a user's typo is refused there and
		// never arrives. Reaching it means main.go routes a fourth verb this
		// package does not implement — the two halves of one binary disagreeing
		// about its own command set, which is Endless broken rather than
		// anything the reader typed.
		refusal.Faultf("endless-go: unknown spawn command %q", verb).
			Detail(usageText()).Exit(2)
	}
}

// usageText is the verb list, as a string rather than a writer, because the
// only place it goes now is Detail — the refusal carries it to whichever
// audience is reading instead of each caller choosing a stream.
func usageText() string {
	return strings.Join([]string{
		"Usage: endless-go spawn-window|spawn-layout|spawn-launch [flags]",
		"  spawn-window  Create the tmux window that launches Claude on a task",
		"  spawn-layout  Build the standard pane layout around an existing Claude pane",
		"  spawn-launch  (internal) Set window options and exec claude inside the window",
	}, "\n") + "\n"
}
