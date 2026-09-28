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
//     hookcmd.Run so hook-fired writes always target the main DB,
//     regardless of cwd, and the E-1429 worktree gate is satisfied.
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

	"github.com/mikeschinkel/endless/internal/monitor"
	"github.com/mikeschinkel/endless/internal/refusal"
)

func Run(args []string) {
	// Logging is set up HERE rather than in an init(), so it happens for the
	// hook and for nothing else. See initLog.
	initLog()

	if len(args) < 1 {
		refusal.NoReport(
			"Usage: endless-go hook <command> [args...]",
			"Re-run with a command: prompt, claude or codex",
		).Command("hook").Detail("Commands: prompt, claude, codex").Exit(1)
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
		refusal.NoReport(
			fmt.Sprintf("Unknown command: %s", args[0]),
			"Re-run with a command: prompt, claude or codex",
		).Command("hook").Exit(1)
	}

	if err != nil {
		// E-2020: a schema refusal — a database ahead of this binary, a forward
		// migration that failed, a worktree build aimed at main — is a silent
		// no-op plus ONE recorded fault, and nothing else: no log line, no
		// halt, exit 0, empty stdout, which is how a hook says "no action".
		// E-1962's reasoning transfers unchanged: a hook that errored here
		// would surface as a Claude Code hook failure on every event on every
		// session, turning one mismatch — every land's window, at minimum —
		// into a stream of identical errors for the user to chase. The fault is
		// fingerprinted on the mismatch, so fifty events are one incident with
		// a count of fifty.
		if monitor.RecordSchemaRefusal("hook:"+args[0], err) {
			return
		}

		// The log file is the durable record; it no longer tees to stderr, so
		// the refusal below is what a reader actually sees.
		log.Printf("%s: %v", args[0], err)

		// E-1887: and this is for the user who is not reading either. Claude
		// Code discards hook stderr, and a non-blocking failure exits 0 by
		// contract, so until this line a failing hook reported to nobody and
		// the only symptom was state that silently stopped being written.
		//
		// The single sink, deliberately: every error that ends a hook
		// invocation passes through here, so a handler added later is covered
		// without anyone remembering to. See faultclass.go for why the code it
		// records is classified at the raise site rather than chosen here.
		//
		// It is additive and cannot change what follows — faults.Record never
		// returns an error and never panics — so the exit code below is
		// exactly what it was.
		recordHookFault(args[0], err)

		code := hookExitCode(err)

		// E-2159: and this is the one for whoever the EVENT routes to. A hook
		// failure is a fault by construction — every error reaching here is a
		// wrapped internal failure, and no retry by the agent changes any of
		// them — so refusal.From keeps a class an error chose for itself and
		// faults the rest, which is the right default in exactly this place.
		//
		// The prefix is kept, the timestamp is not. `endless-go hook: <ts>
		// claude: ` came from the standard logger, which used to tee to stderr;
		// the part that told a reader WHICH hook failed is worth keeping, and
		// the timestamp is a log artifact that belongs in the log.
		//
		// The fault row above and this line are not redundant, and the
		// readerNobody case below is why: on an async event nothing printed
		// here reaches anybody, and the fault row is then the only place the
		// failure exists. That is the gap E-1887 was filed for.
		notice := refusal.From(err).
			Command("hook " + args[0]).
			Text(fmt.Sprintf("endless-go hook %s: %v", args[0], err))
		if code == exitBlocking {
			notice = notice.Detail(haltNotice())
		}

		// Who reads it is the event's business, not the environment's.
		switch hookReader(err) {
		case readerAgent:
			notice.ToAgent().Print()
		case readerHuman:
			notice.ToHuman().Print()
		case readerNobody:
			// Discarded by the harness. Writing here would put a refusal into a
			// stream nobody is reading; the log line above is the record.
		}
		os.Exit(code)
	}
}
