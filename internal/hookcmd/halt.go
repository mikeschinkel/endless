package hookcmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mikeschinkel/endless/internal/monitor"
)

// E-1661: a failing hook has to reach the AGENT, not only the person watching.
//
// Claude Code grades a hook by its exit code, and exactly one code is fed back
// into the model's context. Exit 2 blocks the call on PreToolUse and hands the
// model stderr as feedback; on PostToolUse the tool has already run, so it just
// hands the model stderr. EVERY other non-zero code is a "non-blocking error":
// the harness shows the user a notice and lets the turn continue, and nothing
// about the failure ever enters the transcript the model reads.
//
// Every hook failure used to exit 1. So an agent told to stop on errors could
// not: Endless would fail to record the session, say so to a human who was
// probably not looking, and the agent would carry on writing code against a
// database that was no longer tracking it. That is the whole bug — not the
// failures themselves, which are rare, but their invisibility to the one party
// able to act on them.
const (
	// exitBlocking is the only code Claude Code feeds back to the model.
	exitBlocking = 2

	// exitNonBlocking is the harness's "non-blocking error" — a notice to the
	// user, and the turn proceeds. Every hook failure used to exit this.
	exitNonBlocking = 1
)

// stopEvents are the events where exit 2 does not mean "tell the agent". It
// means "refuse to let the turn end", and the harness answers by starting
// another model turn immediately — no user input, no cap. A hook that exits 2
// on every Stop because the database is unreachable therefore puts the session
// into a turn-per-iteration loop that only Esc can leave, and the model is
// never told why, because Stop's stderr goes to the user rather than into the
// context. The loop guard the Stop gate normally relies on is a counter in the
// database (monitor.BumpReportBounce) — precisely what is unavailable when the
// database is why the hook failed.
//
// So these keep the old non-blocking exit, and nothing is lost by it: the
// agent is told on its very next tool call, and PreToolUse and PostToolUse are
// the only two events whose stderr reaches the model at all.
var stopEvents = map[string]bool{
	"Stop":         true,
	"SubagentStop": true,
}

// eventError labels an error with the harness event it happened on.
//
// Which exit code reaches the agent is a property of the event, and the event
// is known only where the payload was parsed. Carrying it on the error means
// the single exit in Run can grade a failure raised twenty call frames away
// without re-deriving anything.
type eventError struct {
	event string
	err   error
}

func (e *eventError) Error() string { return e.err.Error() }
func (e *eventError) Unwrap() error { return e.err }

// taggedWithEvent labels err with the event that fired. Nil in, nil out, so a
// caller can wrap a bare return value or a deferred result without a guard.
func taggedWithEvent(event string, err error) error {
	if err == nil {
		return nil
	}
	return &eventError{event: event, err: err}
}

// hookExitCode grades a hook failure into the code Claude Code should see.
//
// An error carrying no event was raised before the payload could be parsed —
// unreadable stdin, malformed JSON — or by a hook that is not a Claude event
// at all, such as `hook prompt` firing from a shell prompt. With no event there
// is no way to know whether 2 would block a tool call or trap a Stop, so it
// stays non-blocking. Guessing in the blocking direction is the one mistake
// that can hang a session.
func hookExitCode(err error) int {
	var ee *eventError
	if !errors.As(err, &ee) {
		return exitNonBlocking
	}
	if stopEvents[ee.event] {
		return exitNonBlocking
	}
	return exitBlocking
}

// haltNotice is the instruction half of a blocked hook failure, and only that
// half: the error itself is already on stderr, because Run logs it and the log
// writer includes stderr. This adds what the error cannot say — that the agent
// must stop, and what a person has to do to clear it.
//
// Deliberately generic about the remedy. Endless is a binary installed on
// somebody's machine against a database somewhere else on it, and the fix is
// "make those two agree again" whichever way that machine installs software.
// Naming a build recipe here would be right in exactly one checkout.
//
// The two paths are the diagnosis. Almost every failure this blocks on is a
// disagreement between them — a binary upgraded while the database was not, or
// pointed at a database another binary owns — and a reader who can see both
// can tell which one is stale without knowing anything about Endless.
func haltNotice() string {
	var b strings.Builder
	b.WriteString("\nBLOCKED: an Endless hook failed. Session and task tracking are\n")
	b.WriteString("unreliable until it is fixed.\n\n")
	b.WriteString("STOP. Do not continue the task and do not work around this.\n")
	b.WriteString("Report the error above to the user verbatim, then wait.\n\n")
	if exe, err := os.Executable(); err == nil {
		fmt.Fprintf(&b, "  hook binary: %s\n", exe)
	}
	fmt.Fprintf(&b, "  database:    %s\n\n", monitor.DBPath())
	b.WriteString("The usual cause is a stale binary: one of those two was upgraded and\n")
	b.WriteString("the other was not, so the code and the schema disagree — a column the\n")
	b.WriteString("query names and the table does not have, or an enum mirror failing its\n")
	b.WriteString("integrity check. Reinstall or rebuild endless so both come from the\n")
	b.WriteString("same version, then start a new session.\n")
	return b.String()
}
