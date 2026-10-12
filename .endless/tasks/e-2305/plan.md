The open questions ARE this task's deliverable. Each needs an answer from Mike
before any of this can become a plan.

## Already decided, Mike 2026-10-12

No blanket "belt and suspenders" install. Asked directly whether to install the
rest for safety, the answer was no: each event is paired with a handler that
does something, or it is left out. The reason is measurable — see Q2.

## Q1. Which inferred signals get replaced by a dedicated event?

For each of the three, the options are: replace the inference, add the event
alongside it as a second source, or leave it inferred.

- the cwd gate, today Bash command text, candidate `CwdChanged`
- the worktree-removal refusal, today a regex over Bash command text,
  candidates `WorktreeCreate` and `WorktreeRemove`
- a session waiting on the user, today `Notification` with matcher
  `permission_prompt`, candidates `PermissionRequest` and `PermissionDenied`

What makes this a real question rather than an obvious yes: a refusal gate has
to fire BEFORE the thing it refuses. `PreToolUse` is where a refusal can block;
a `WorktreeRemove` event may report a removal that is already underway, in which
case it is a better signal for recording but a worse one for refusing. The
payload schemas, unread so far, decide this.

## Q2. What does installing an event cost, and does a frequent one need an opt-out?

Every hook invocation runs the common path before the dispatch switch: the
throttled activity record and `monitor.TouchSession`, which writes the process
and `last_activity`. An installed event whose handler does nothing therefore
still refreshes the session's activity each time it fires. For a frequent event
— `MessageDisplay`, `FileChanged` — that would make a session read as active
while nothing is happening, which is the failure the `StopFailure` paragraph in
`src/endless/setup.py` already warns about. Does a handler need a way to opt out
of the common path, or does that rule out frequent events entirely?

## Q3. Do matchers enter the picture?

Endless writes entries with no matcher, so they fire for every occurrence.
Several events define matcher values: `PreCompact` has manual and auto,
`Notification` has permission_prompt, idle_prompt and auth_success,
`StopFailure` has rate_limit, overloaded, authentication_failed, billing_error
and unknown. Installing per-matcher would let a handler skip work it cannot use;
it also multiplies the entries that `_repair_missing_hook_events` has to reason
about, and that function's current notion of "this event is installed" is a
substring test that knows nothing about matchers.

## Q4. Which untracked events are worth a handler at all?

`SubagentStart` / `SubagentStop`, `PostCompact`, `PostToolUseFailure`,
`ConfigChange`, `TaskCreated` / `TaskCompleted`, `PreModelSwitch` /
`PostModelSwitch`, `Setup`, `InstructionsLoaded`, `Elicitation`. Each needs a
use that Endless has today, not one that could be imagined. Subagent activity is
the strongest candidate: the session model has no idea a subagent is running,
and auto-spawn's concurrency cap counts sessions.

## Q5. Does the answer change where the expected set lives?

E-2285 already records that the set moves to Go with Python reading it from the
binary. A per-matcher install or a per-event opt-out from the common path would
make the set a structure rather than a list of names. Worth settling here so
E-2285 builds the right shape once.

## Out of scope

Installing `PreCompact` and deleting the unreachable `ExitPlanMode` case. That
is the related bugfix, already filed and planned.
