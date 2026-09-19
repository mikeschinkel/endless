## The defect

End-of-turn handling is attached to ONE event when Claude Code has TWO.

Everything Endless does when a turn ends hangs off the `Stop` case in
`internal/hookcmd/claude.go`: `monitor.ParseTranscript`, then
`monitor.IdleSession`. That was correct when `Stop` was the only way a turn
could end. It is not any more, and may never have been.

Claude Code fires `StopFailure` INSTEAD OF `Stop` when a turn ends on an API
error — the two are alternatives in the per-turn loop, not a sequence.
Confirmed against the current hooks reference, where the event's matcher filters
on error type with documented values including `rate_limit`, `overloaded`,
`max_output_tokens`, `server_error`, `authentication_failed`, `billing_error`
and `invalid_request`.

So a turn that dies takes none of the end-of-turn handling with it. The
transcript is not parsed, and the session is never moved off `working`.

Nothing else moves it. `SessionEnd` fires on session termination, not on a
failed turn. `TouchSession` runs on every event but deliberately never clobbers
a live state — the only transition it owns is reviving an `ended` row. Liveness
does not help either: the pane is still there, so the session reads `live`,
which is true and useless. The row stays `working` until the session is resumed
or ends.

## Installing the event is necessary and not sufficient

Worth stating because the first reading of this task took "Endless does not
install StopFailure" to mean installing it is the fix.

It is not. `TouchSession` runs before the event switch, so merely installing the
event would refresh `last_activity` and bind the pane — leaving the session
reading `working` with a FRESHLY UPDATED activity timestamp, which is worse than
today: `project monitor`'s age column would show it as recently active while it sits
dead.

The switch itself has cases for SessionStart, UserPromptSubmit, PreToolUse,
PostToolUse, Notification, ExitPlanMode, Stop, PreCompact and SessionEnd, and no
`default`. An installed-but-unhandled event falls straight through — a process
spawned per failed turn that does nothing.

The event must be installed AND handled.

## Why this matters more than it looks

Two systems read `working` as a live claim, and both were built assuming the
state is true.

`project monitor` (E-1976) renders such a session as a `doing` row
indefinitely — the board asserting work is in flight on a task where the turn
died, which is the opposite of what a triage surface is for, and
indistinguishable from real work except by the age column that installing-only
would corrupt.

Auto-spawn's cap (E-1815 / E-1814) is the sharper case. It counts live sessions
to decide whether to spawn more, on the stated reasoning that it measures claims
on the user's attention. A session stuck `working` consumes cap capacity
forever, so the governor throttles new spawns against sessions that are gone.
That premise does not survive a state that can be false indefinitely.

Neither is a rare path. `rate_limit` and `max_output_tokens` are ordinary
outcomes of a long session.

## The state a failed turn lands in — no new state

The obvious move is a distinct `failed` state, and it is the wrong one. A turn
that died on a rate limit IS a session paused waiting on the user, which is what
`idle` already means under the definition settled for E-2091 (2026-09-03):
paused for input, question or not. Adding a state to record HOW it paused would
grow a vocabulary E-2105 has just finished giving a home, and would put a new
member in front of five group memberships for a distinction the state column is
not the right place to carry.

The failure belongs where failures already go. Endless has a machine-local fault
store — classified, clearable, ERR-NNNN-coded, and already rendered as a badge
on the very view this protects. A turn that died on an API error is exactly
that shape: not a property of the task, not a property of the session's
lifecycle, but an incident worth surfacing until someone has seen it.

Recorded as a decision rather than an open question, and open to being
overturned: the alternative reading — that a crashed turn deserves to be
visibly distinct from a finished one in `project monitor` itself — is reasonable, and
would mean a state after all.

## Severity follows the error type, because the badge shows one row

The badge renders a single line and the most severe open incident wins it. So
severity here decides PROMINENCE — which failure gets the row when several are
open — not longevity.

- Needs a person — `authentication_failed`, `billing_error`,
  `oauth_org_not_allowed`, `account_on_hold`: error. Nothing self-heals, and
  these should outrank a transient failure for the one row available.
- Transient, self-resolving — `rate_limit`, `overloaded`, `server_error`,
  `max_output_tokens`: warning.
- An unrecognised type: warning. An unknown failure is more likely transient
  than fatal, and the badge still carries it.

An earlier draft justified this mapping by the fault store's stale-warning
age-off, on the reasoning that a transient failure should stop being badged once
it had passed. Mike rejected the age-off itself (2026-09-15): a warning that
disappears on its own is one he never sees, and clearing is one command. That is
the same failure mode this whole area exists to fix — `project status` and `project monitor` were built
because unverified work vanished unseen, and the 34 stale `needs_input` rows
rotted for 25 to 71 days for exactly that reason. Removing the age-off is filed
separately; nothing in this task depends on it either way, because the mapping
above stands on prominence alone.

## Scope note

Deliberately NOT folded into E-2091, which reached neighbouring territory. That
task was underway in another session when this was found, and editing a live
task's plan underneath the session working it is not a thing to do. E-2091 has
since landed; this stands alone either way.

Two further hook events were examined and rejected as candidates, recorded here
so nobody re-derives them.

`WorktreeCreate` and `WorktreeRemove` fire only for Claude Code's own worktree
management — via `--worktree`, `isolation: worktree`, or a background session.
They do not fire for worktree operations invoked as shell commands through the
Bash tool, which is what Endless's removal refusal guards, so they do not
reinforce that guard.

`CwdChanged` fires after the directory has already changed and cannot block, so
it does not replace the cwd gate, which refuses at PreToolUse.
