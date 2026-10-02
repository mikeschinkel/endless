// Package sessionprimecmd implements `endless-go session-prime`: the last step
// of a read-in, where a session started ahead of need marks itself `primed`
// and holds for its user (E-1994).
//
// The session is named by Claude Code's own CLAUDE_CODE_SESSION_ID and nothing
// else. Priming is something a session says about ITSELF, from inside its own
// turn; a sibling shell pane or another session priming it on its behalf would
// be asserting a read-in it did not watch happen. So there is no flag to name a
// session, and no fallback to pane resolution.
//
// Pinned to the main database in cmd/endless-go/main.go, beside `hook`: the row
// it writes is the one the session's hooks write, and those are pinned there.
//
// Exit codes:
//
//	0  primed
//	1  refused — not a Claude session, or not working on a task
//	2  usage error
package sessionprimecmd

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

// sessionEnv is the variable Claude Code sets in every tool subprocess.
const sessionEnv = "CLAUDE_CODE_SESSION_ID"

// Run executes the subcommand.
func Run(args []string) {
	out, code := run(args, os.Getenv(sessionEnv), os.Stdout)
	if out != nil {
		out.Exit(code)
	}
}

// run returns the refusal to print and its exit code, or (nil, 0) after
// printing success to stdout. Split from Run so tests need no process exit.
func run(args []string, sessionID string, stdout io.Writer) (*refusal.Error, int) {
	if len(args) > 0 {
		if args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
			fmt.Fprint(stdout, usageText())
			return nil, 0
		}
		return refusal.NoReport(
			"endless-go session-prime takes no arguments",
			"Run it with none, from the session's own Bash tool",
		).Command("session-prime").Text(usageText()), 2
	}
	if sessionID == "" {
		// NO_REPORT: the fix is where it is run from, not anything the user
		// decides.
		return refusal.NoReport(
			"Not inside a Claude Code session ("+sessionEnv+" is unset); nothing was primed",
			"Only a session can prime itself: run endless session primed from that session's own Bash tool",
		).Command("session-prime"), 1
	}
	err := monitor.PrimeSession(sessionID)
	switch {
	case errors.Is(err, monitor.ErrNotPrimable):
		return refusal.NoReport(
			"Cannot prime this session: "+err.Error(),
			"A session primes itself at the end of a read-in, while it is working on the task it read; if this session is not a read-in, do not prime it",
		).Command("session-prime"), 1
	case err != nil:
		return refusal.Fault(fmt.Errorf("cannot prime this session: %w", err)).Command("session-prime"), 1
	}
	fmt.Fprintln(stdout, "Primed. This session is holding for its user; end your turn now.")
	return nil, 0
}

func usageText() string {
	return "Usage: endless-go session-prime\n" +
		"  Mark the calling Claude session `primed` (E-1994). Reads " + sessionEnv + ".\n"
}
