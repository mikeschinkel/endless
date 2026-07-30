// Package spawnlaunchcmd implements the `endless-go spawn-window` /
// `endless-go spawn-launch` subcommands — the multiplexer seam through which
// `endless task spawn` launches Claude.
//
// Two entry points, dispatched on the first arg:
//
//	spawn-window  Outer orchestrator (the only verb Python calls). Writes a
//	              JSON launch-spec file, then creates the tmux window whose
//	              command is `endless-go spawn-launch --spec <path>`. In attach
//	              mode it instead runs `claude attach <short-id>` as the window
//	              command. Returns once the window exists.
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
	"fmt"
	"os"
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
	default:
		fmt.Fprintf(os.Stderr, "endless-go: unknown spawn command %q\n", verb)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprintln(w, "Usage: endless-go spawn-window|spawn-launch [flags]")
	fmt.Fprintln(w, "  spawn-window  Create the tmux window that launches Claude on a task")
	fmt.Fprintln(w, "  spawn-launch  (internal) Set window options and exec claude inside the window")
}
