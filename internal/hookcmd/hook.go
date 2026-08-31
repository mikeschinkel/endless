// Package hookcmd implements the `endless-go hook` subcommand. It is
// invoked by Claude Code's settings.json hook entries (PostToolUse,
// UserPromptSubmit, Stop, SessionStart, SessionEnd).
//
// The dispatcher (cmd/endless-go) handles two contracts before Run is
// called:
//
//   - E-1470: If ENDLESS_NO_HOOKS=true the dispatcher returns BEFORE
//     calling hookcmd.Run. Internal headless `claude -p` calls
//     (the verb-check) set ENDLESS_NO_HOOKS=true to suppress the
//     hook so the pane-collision rule (internal/monitor) does not mark
//     the live caller's session ended.
//
//   - E-1450/E-1429: The dispatcher calls monitor.PinMainDB() before
//     hookcmd.Run so hook-fired writes always target the real DB,
//     regardless of cwd or XDG_CONFIG_HOME, and the E-1429 worktree
//     gate is satisfied.
//
// Run owns the third contract, the one on the way out: a failure exits with
// the code that puts it in front of the AGENT rather than only the user. See
// halt.go — that grading is the whole of E-1661.
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
		fmt.Fprintln(os.Stderr, "Commands: prompt, claude, codex")
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
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", args[0])
		os.Exit(1)
	}

	if err != nil {
		// The log writer includes stderr, so this line IS the error the agent
		// or the user reads; haltNotice below only adds the instruction.
		log.Printf("%s: %v", args[0], err)
		code := hookExitCode(err)
		if code == exitBlocking {
			fmt.Fprint(os.Stderr, haltNotice())
		}
		os.Exit(code)
	}
}
