# Plan — give the second end-of-turn event the same handling as the first

The shape of the change: `Stop` and `StopFailure` are the two ways a turn can
end, and both must leave the session in an honest state. Today only one is
wired.

## 1. Install the event

Add `StopFailure` to `CLAUDE_HOOK_EVENTS` in `src/endless/setup.py`.

NOT in `SYNC_EVENTS`. The handler records and gates nothing, and the event
cannot block in any case — the hooks reference states that `StopFailure`
discards hook output entirely, ignoring the JSON on every exit code. A
synchronous entry would buy nothing and delay the turn's end.

Existing installs need nothing extra: `_repair_missing_hook_events` already adds
an endless-go entry for any hooked event that has none, which is why a machine
whose install predates an event still gains it.

## 2. Handle it

`case "StopFailure":` in `internal/hookcmd/claude.go`, doing what `Stop` does,
minus the parts that only make sense for a turn that completed:

- `monitor.ParseTranscript(payload.SessionID, payload.TranscriptPath)` — the
  turn produced assistant messages before it died, and they are as much part of
  the session's history as any other. This is the first half of the `Stop` case
  and it applies unchanged.
- `monitor.IdleSession(payload.SessionID)` — the session has stopped and is
  waiting on the user, which is what `idle` means.

NOT carried over from `Stop`: the report gate (`enforceReportGate`). It exists
to hold a turn open until the agent's final message satisfies the relay
contract, and a turn that died on an API error has no final message to judge and
cannot be held open — the event cannot block. `monitor.ReapWorktreesForProject`
is likewise skipped; `Stop` calls it opportunistically and a failed turn is not
the moment to start git work.

`claudePayload` gains the field carrying the error type, with the JSON tag the
hooks reference documents for this event's matcher.

## 3. Record the failure as a fault

A new catalog entry in `internal/faults/codes.go`, taking the next free number
(ERR-0013; numbers are never reused):

    ErrCodeTurnFailed — slug "turn-failed",
                        title "A turn ended on an API error"

Severity is not fixed on the code, because it depends on the error type and the
store already treats the two differently — a warning ages off after an hour of
active time, an error never does. Map it:

- warning: `rate_limit`, `overloaded`, `server_error`, `max_output_tokens`, and
  any type not listed below (the forgiving direction — a wrong warning ages off,
  a wrong error sits there permanently).
- error: `authentication_failed`, `billing_error`, `oauth_org_not_allowed`,
  `account_on_hold`.

If `faults.Code` cannot carry a per-occurrence severity, add two catalog entries
rather than mutating one — the catalog is documentation as much as data, and a
code whose severity varies invisibly is worse than two codes that each mean one
thing. Check `faults.Record`'s signature and follow whichever the store
supports; this is a lookup, not a decision.

`Source` is `"hook:stopfailure"`, matching the `job:<name>` idiom already in the
store. `Summary` names the error type so the badge says which failure it was.
`Fields` carries the session id and the task id when the session holds one.

## 4. What this does NOT change

No new session state, and no change to what `idle` means — see the analysis.

No change to `Stop`. The two cases stay separate rather than being merged behind
a shared helper: they overlap in two calls and differ in three, and a helper
with a `failed bool` parameter would put the difference inside a function whose
name says the cases are the same.

## 5. Verification

`.endless/tasks/e-2145/verify.sh`, sourcing `_harness.sh`:

1. Fail-fast unit gate: `internal/hookcmd`, `internal/monitor`,
   `internal/faults`, and the Python suite.
2. The defect, reproduced at the unit level: drive the handler with a synthetic
   `StopFailure` payload against a seeded session in `working`, and assert the
   session is `idle` afterwards. Without the fix this assertion fails, which is
   what makes it a regression test rather than a description.
3. Installing is not enough: assert the event switch has a `case "StopFailure"`.
   A test that only checked `CLAUDE_HOOK_EVENTS` would pass against a build that
   spawns a process per failed turn and does nothing.
4. The activity timestamp is not left lying: after a `StopFailure`, the session's
   state is `idle` — so the board cannot show a dead turn as recently active in
   a live state.
5. Severity mapping: a `rate_limit` payload records a warning, an
   `authentication_failed` payload records an error, and an unrecognised type
   records a warning.
6. The fault carries the error type in its summary, so the badge names which
   failure occurred rather than only that one did.
7. `setup.py` installs the event and does NOT place it in `SYNC_EVENTS`; and a
   settings file carrying the older event set is repaired to include it rather
   than reported as correct.
8. `Stop` is unchanged: its existing tests pass untouched, which is the witness
   that the two cases were not merged.
