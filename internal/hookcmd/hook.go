// Package hookcmd implements the `endless-go hook` subcommand. It is
// invoked by Claude Code's settings.json hook entries (PostToolUse,
// UserPromptSubmit, Stop, SessionStart, SessionEnd) and by `claude -p`
// recap callouts.
//
// The dispatcher (cmd/endless-go) handles two contracts before Run is
// called:
//
//   - E-1470: If ENDLESS_NO_HOOKS=true the dispatcher returns BEFORE
//     calling hookcmd.Run. Internal headless `claude -p` calls
//     (verb-check, recap) set ENDLESS_NO_HOOKS=true to suppress the
//     hook so the pane-collision rule (internal/monitor) does not mark
//     the live caller's session ended.
//
//   - E-1450/E-1429: The dispatcher calls monitor.PinMainDB() before
//     hookcmd.Run so hook-fired writes always target the real DB,
//     regardless of cwd or XDG_CONFIG_HOME, and the E-1429 worktree
//     gate is satisfied.
package hookcmd

import (
	"fmt"
	"log"
	"os"

	_ "modernc.org/sqlite"
)

func Run(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: endless-go hook <command> [args...]")
		fmt.Fprintln(os.Stderr, "Commands: prompt, claude, codex, recap")
		os.Exit(1)
	}

	var err error
	switch args[0] {
	case "prompt":
		err = runPrompt(args[1:])
	case "claude":
		err = runClaude(args[1:])
	case "codex":
		err = runCodex(args[1:])
	case "recap":
		err = runRecap(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
		os.Exit(1)
	}

	if err != nil {
		log.Printf("%s: %v", args[0], err)
		os.Exit(1)
	}
}
