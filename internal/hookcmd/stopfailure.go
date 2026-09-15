package hookcmd

import (
	"fmt"

	"github.com/mikeschinkel/endless/internal/faults"
	"github.com/mikeschinkel/endless/internal/monitor"
)

// The `StopFailure` hook (E-2145).
//
// End-of-turn handling was attached to ONE event when Claude Code has TWO.
// `Stop` and `StopFailure` are ALTERNATIVES in the per-turn loop, not a
// sequence: the harness fires `StopFailure` INSTEAD OF `Stop` when the turn ends
// on an API error. So everything hanging off the `Stop` case — the transcript
// parse, the move off `working` — took none of it with it when a turn died.
//
// Nothing else moved the session either. `SessionEnd` fires on session
// termination, not on a failed turn; `TouchSession` never clobbers a live state;
// liveness sees a pane that is still there, which is true and useless. The row
// stayed `working` until the session was resumed or ended — and two systems read
// `working` as a live claim: `project monitor` renders a dead turn as work in
// flight, and auto-spawn's cap throttles new sessions against ones that are
// gone.
//
// Installing the event without this handler would have been WORSE than leaving
// it uninstalled. `TouchSession` runs before the event switch, so a bare install
// refreshes `last_activity` and binds the pane, leaving the session reading
// `working` with a timestamp saying it was active seconds ago. The switch has no
// `default`, so an installed-but-unhandled event is a process spawned per failed
// turn that does nothing.
//
// Not in setup.py's SYNC_EVENTS, and it could not usefully be: the hooks
// reference is explicit that Claude Code does not read this hook's output on any
// exit code. Nothing here can block, gate or answer — it records, and that is
// the whole contract.

// fatalTurnErrorTypes names the `error_type` values that will not clear
// themselves. Each is a fact about the account rather than a passing condition,
// so retrying cannot change the answer.
//
// Membership decides which catalog code the fault carries, and therefore its
// severity — which buys PROMINENCE, not longevity: the fault badge renders a
// single line and the most severe open incident wins it. A needs-a-person
// failure should take that line from a rate limit that will have passed by the
// time anyone looks.
//
// Everything else is transient, INCLUDING an error type not listed in either
// set. `invalid_request`, `model_not_found`, `cloud_credential_error` and
// `unknown` are documented values that land there today, and so will whatever a
// future Claude Code adds. That is the forgiving direction on purpose: an
// unrecognised failure is more likely to be a passing one, and grading it red
// would let a guess outrank a real error for the badge's one row.
var fatalTurnErrorTypes = map[string]bool{
	"authentication_failed": true,
	"billing_error":         true,
	"oauth_org_not_allowed": true,
	"account_on_hold":       true,
}

// unreportedTurnErrorType is what a `StopFailure` payload carrying no
// `error_type` is recorded as. `unknown` is a value Claude Code documents and
// emits itself, and an absent field says the same thing — the harness did not
// name the failure — so collapsing the two into one incident is the honest
// grouping rather than a lost distinction.
const unreportedTurnErrorType = "unknown"

// turnFailureCode picks the catalog entry for an error type.
//
// Two codes rather than one carrying a per-occurrence severity: severity is a
// property of the Code in internal/faults, deliberately, so that two callers
// raising one condition cannot disagree about whether the user sees yellow or
// red. A code whose colour varied with its payload would be the first exception
// to that, and invisible in both the catalog and `endless errors codes`.
func turnFailureCode(errorType string) (code faults.Code) {
	code = faults.ErrCodeTurnFailedTransient
	if fatalTurnErrorTypes[errorType] {
		code = faults.ErrCodeTurnFailedFatal
	}
	return code
}

// handleStopFailure gives the second end-of-turn event the handling the first
// has had all along, minus the two parts that only make sense for a turn that
// finished.
//
// NOT carried over from `Stop`: the report gate (enforceReportGate), which
// exists to hold a turn open until the agent's final message satisfies the relay
// contract — a turn that died on an API error has no final message to judge and
// cannot be held open, because this event cannot block. Nor
// monitor.ReapWorktreesForProject: `Stop` calls it opportunistically, and a
// failed turn is not the moment to start git work.
//
// The two cases stay separate rather than being merged behind a shared helper.
// They overlap in two calls and differ in three, and a helper taking a
// `failed bool` would put the difference INSIDE a function whose name claimed
// the cases were the same.
func handleStopFailure(projectID int64, payload claudePayload) (err error) {
	// The turn produced assistant messages before it died, and they are as much
	// part of the session's history as any other. This is the first half of the
	// `Stop` case and it applies unchanged.
	monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)

	// Before idling, not after: faults.Record never fails and never returns, but
	// IdleSession does. Recording first means a database that cannot take the
	// state write still leaves the report behind rather than losing both.
	recordTurnFailure(projectID, payload)

	// The session has stopped and is waiting on a person, which is what `idle`
	// already means — including for a turn that died on a rate limit. No new
	// session state: how a session came to be paused is an incident, and the
	// fault above is where incidents live.
	err = monitor.IdleSession(payload.SessionID)
	if err != nil {
		err = fmt.Errorf("idling session: %w", err)
	}

	return err
}

// recordTurnFailure files the incident behind an ended-in-error turn.
//
// Fingerprinted by the summary's default, which embeds only the error type: a
// session hitting one rate limit twenty times is ONE incident with an occurrence
// count of twenty, and a different failure opens its own row. Deliberately not
// per-session — the badge would then carry one line per session on a machine
// where a shared rate limit hit every one of them at once.
//
// The session and task go in Fields instead, which reach the JSONL detail log,
// so `errors show --detail` still answers "which session, on what" per
// occurrence without splitting the incident.
func recordTurnFailure(projectID int64, payload claudePayload) {
	errorType := payload.ErrorType
	if errorType == "" {
		errorType = unreportedTurnErrorType
	}

	fields := map[string]any{
		"session_id": payload.SessionID,
		"error_type": errorType,
		"cwd":        payload.CWD,
	}
	// Best-effort: an unbound session, or a lookup that fails, costs the task
	// label on the detail line and nothing else. The incident is about the turn,
	// not about the task, so it must not depend on there being one.
	if session, serr := monitor.GetActiveSession(payload.SessionID); serr == nil &&
		session != nil && session.TaskID != nil {
		fields["task_id"] = *session.TaskID
	}

	faults.Record(faults.Fault{
		Code:      turnFailureCode(errorType),
		Source:    "hook:stopfailure",
		Summary:   fmt.Sprintf("a turn ended on an API error: %s", errorType),
		ProjectID: projectID,
		Fields:    fields,
	})
}
