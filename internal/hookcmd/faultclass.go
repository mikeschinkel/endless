package hookcmd

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"

	"github.com/mikeschinkel/endless/internal/faults"
)

// Classifying a hook failure, and recording it (E-1887).
//
// # The failure this closes
//
// A hook that could not write to the database reported it to NOBODY. Run logs
// the error and exits with a code chosen so a broken hook never blocks a tool
// call (see halt.go), and Claude Code discards hook stderr — so the failure is
// invisible BY CONSTRUCTION rather than by oversight. Session ES-1055 ran that
// way for four weeks: every PreToolUse died inside monitor.TouchSession with
// "table sessions has no column named process", `sessions.process_id` stayed
// NULL, and the only symptom was a blank tmux status bar, which is also what a
// pane with no task looks like.
//
// The non-blocking contract stays. What changes is that the failure stops being
// silent: it lands in the fault row `session status` already renders.
//
// # One recording site, four codes
//
// Recording happens at ONE place — the error sink in Run — because that is the
// single point every error ending a hook invocation already passes through, so
// a handler added later is covered without anyone remembering to add a call.
// Recording at the TouchSession call site instead would cover exactly the
// failure we happened to hit, which is how this class of bug survives its own
// fix.
//
// But one code for everything that sink catches would file a malformed harness
// payload and a schema-drifted database under one title, and a report that says
// only "a hook failed" has told its reader nothing they can act on. So the
// error is CLASSIFIED where it is raised — dbWriteFailed, dbReadFailed,
// payloadUnreadable below — and the sink records whichever code the
// classification names. Nothing is sniffed out of an error string, which is the
// other way this could have been done and the brittle one: the wrapping text at
// a call site is prose, and prose gets edited.
//
// An unclassified error is not dropped. It raises ErrCodeHookFailed, whose
// detail line still carries the full error text.
//
// # What does NOT reach the sink
//
// A deliberate gate decision — the declaration gate, the relay gate, the
// revisit gate — calls blockToolUse, which writes to stderr and exits 2 on the
// spot. Those never return an error and so never reach Run's sink, which is
// what makes recording there safe: everything the sink sees is a genuine
// failure, not a policy answer. A fault per blocked Write would be pure noise.

// classedError pairs an error with the catalog code that classifies it.
//
// It is applied where the error is RAISED and read once, at the sink. Wrapping
// rather than replacing, so every existing errors.Is/As against the cause still
// works — including the eventError tag that grades the exit code, which wraps
// this in turn on the way out of runClaude.
type classedError struct {
	code faults.Code
	err  error
}

func (e *classedError) Error() string { return e.err.Error() }
func (e *classedError) Unwrap() error { return e.err }

// classed labels err with the code that classifies it. Nil in, nil out, so a
// call site can wrap a bare return value without a guard.
//
// An error already carrying a class keeps it: the innermost classification is
// the specific one, and a broader wrapper re-labelling it on the way out would
// be the string-sniffing problem in a different costume.
func classed(code faults.Code, err error) error {
	if err == nil {
		return nil
	}
	var already *classedError
	if errors.As(err, &already) {
		return err
	}
	return &classedError{code: code, err: err}
}

// dbWriteFailed labels a failure to WRITE the row a hook exists to write — the
// sessions upsert that binds a session to its pane, the throttled activity
// record, the session lifecycle transitions.
//
// The most consequential class: a write that did not happen leaves state behind
// that every later read believes.
func dbWriteFailed(err error) error {
	return classed(faults.ErrCodeHookWriteFailed, err)
}

// dbReadFailed labels a failure to READ what a hook needs before it can act —
// the project enclosing its cwd, the activity throttle, the active-task query.
//
// Same causes as a write failure, narrower consequence: the hook returns before
// touching anything, so it did no work for that event rather than leaving a
// wrong answer behind, and the next event retries from scratch.
func dbReadFailed(err error) error {
	return classed(faults.ErrCodeHookReadFailed, err)
}

// payloadUnreadable labels a failure to read or parse the event envelope the
// harness sent on stdin. Not a database problem at all, and its remedy has
// nothing in common with the two above — which is the whole reason it is a
// separate code.
func payloadUnreadable(err error) error {
	return classed(faults.ErrCodeHookPayloadUnreadable, err)
}

// faultCodeFor returns the code an error was classified with, or the catch-all
// when it carries none.
func faultCodeFor(err error) (code faults.Code) {
	var ce *classedError

	code = faults.ErrCodeHookFailed
	if errors.As(err, &ce) {
		code = ce.code
	}
	return code
}

// recordHookFault files one occurrence of a hook failure.
//
// Called from Run's error sink, beside the log.Printf that stays. The log line
// is what a developer tailing hook.log reads; the fault is for the user who is
// not tailing anything, and the two are not substitutes.
//
// It cannot change the hook's exit code and cannot fail it: faults.Record never
// returns an error and never panics by contract, so this is additive to every
// path. TestRecordHookFault_LeavesTheExitCodeAlone pins that.
func recordHookFault(hook string, err error) {
	var event string
	var session string

	event, session = hookEventContext(err)

	faults.Record(faults.Fault{
		Code:   faultCodeFor(err),
		Source: "hook:" + hook,
		// Fingerprinted on the error TEXT, not on the session or the pane —
		// the same choice runStatusLine made, for the same reason. One stale
		// binary fails identically on every event in every pane; keyed by
		// session it would raise one incident per session and bury the shared
		// cause. Keyed by the error, a four-week outage is ONE incident with a
		// rising count.
		Fingerprint: hookFaultFingerprint(err),
		Summary:     hook + " hook: " + err.Error(),
		Detail:      err.Error(),
		Fields:      hookFaultFields(event, session),
	})
}

// hookFaultFields is the structured context that must NOT be in the
// fingerprint: which event fired, which session, which pane, and which binary
// ran.
//
// The binary earns its place from the incident that motivated this: the whole
// diagnosis was "that endless-go is older than the schema", and the first
// question any reader asks is which endless-go wrote the line. Empty values are
// omitted rather than recorded as blanks — an error raised before the payload
// was parsed knows no event and no session, and saying so with a key whose
// value is "" reads as a value rather than as an absence.
func hookFaultFields(event, session string) (fields map[string]any) {
	fields = make(map[string]any, 4)

	if event != "" {
		fields["hook_event"] = event
	}
	if session != "" {
		fields["session_id"] = session
	}
	if pane := os.Getenv("TMUX_PANE"); pane != "" {
		fields["pane"] = pane
	}
	if exe, err := os.Executable(); err == nil {
		fields["binary"] = exe
	}

	return fields
}

// hookFaultFingerprint groups hook failures by CAUSE rather than by message
// instance. Derived from the error text, which is also what the summary embeds,
// so the derivation matches the default — it is passed explicitly anyway so
// that rewording the human-facing summary later cannot silently re-partition
// open incidents.
func hookFaultFingerprint(err error) (fp string) {
	sum := sha256.Sum256([]byte(err.Error()))
	fp = hex.EncodeToString(sum[:])[:16]
	return fp
}
