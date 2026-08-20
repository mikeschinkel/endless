# E-1917 post-land defects

Two defects in the landed implementation. The first was found by another session
working on E-1898; the second surfaced while verifying the first.

## Defect 1 — suppression silences exactly the session that needs the notice

**Symptom.** The user changes a task's status from a shell pane. Every session
holding that task is notified EXCEPT the one actually working on it.

**Evidence** (`session_notices`, task 1898): every row is stamped
`changed_by_session = 1034`, and 1034 — the session holding E-1898 — is a
recipient of none of them except id 625, which session 1046 authored.

```
620  →1026  actor=1034  {"status":{"before":"unverified","after":"assumed"}}
621  →1054  actor=1034  {"status":{"before":"unverified","after":"assumed"}}
                        (no row for 1034)
```

**Root cause.** `evt.Actor.SessionID` answers *"which session should this command
be credited to"*, not *"which session physically made this change."* E-1917's
suppression consumed it as if it answered the second.

`_current_endless_session_id` (`src/endless/task_cmd.py:3425`) resolves in four
layers, and two of them attribute a HUMAN's command to an agent session:

- **Layer 4** (E-1294): a shell pane with exactly one sibling Claude pane in the
  same tmux window is attributed to that sibling. This is deliberate and
  documented — "lets a shell pane in a Claude-using window transparently
  attribute commands to its sibling Claude session."
- **Layer 1**: `ENDLESS_SESSION_ID`, which `esu` / `endless shell-init` exports
  into the user's own shell.

Only **layer 2** (`CLAUDECODE=1` + `CLAUDE_CODE_SESSION_ID`) means the mutating
process IS the agent.

So in the tmux layout the user actually works in, "the user edits from a bare
terminal" does not produce a NULL actor. It produces the adjacent agent's
session id. The suppression rule then works exactly as designed on wrong input.

**Where the plan went wrong.** E-1917's own plan asserted: *"A NULL actor (the
user editing from a bare terminal) suppresses nobody, which is correct — that is
the case this feature exists for."* That assumption was never validated against
the resolver. It is false in the primary scenario, so the feature is defeated
precisely where it was meant to help.

**Fix options.**

- **(a) Remove suppression.** Every holder is notified, including the session
  that made the change. Cost: one redundant confirmatory line on the turn after
  a session changes something itself. Cannot silence the wrong session. Smallest
  change; safe immediate revert.
- **(b) Suppress only on in-process agent identity.** Suppress when the mutating
  process is the agent (layer 2); notify for layers 1, 3 and 4, which are all
  humans at a keyboard. Keeps the original intent without the failure mode.
  Requires the event to carry HOW the actor was resolved, not just the id —
  `Actor` gains an in-process flag that the Python resolver sets only on layer 2.

Recommend (b), with (a) available as an immediate stop-gap if the fix needs to
wait. Note (b) touches the event Actor contract, so it wants review.

## Defect 2 — undelivered notices accumulate forever for dead sessions

**Symptom.** 553 of 679 notice rows (82%) are undelivered, and the largest
holders are sessions that have ended.

```
session  state        undelivered
994      needs_input  34
996      ended        32
614      ended        27
794      needs_input  26
738      idle         25
1029     ended        22
534      ended        20
```

**Root cause.** The trigger fans out to every `session_tasks` row for the task.
That table deliberately has no FK to `sessions` and outlives them, so notices are
written for sessions that ended long ago. Nothing filters on session state at
write time, and nothing reaps. An ended session never takes another turn, so its
rows are permanently undeliverable.

**Consequence.** Unbounded growth, and the table's contents stop being a useful
signal — you cannot tell a genuine backlog from dead weight.

**Fix.** Filter the trigger's fan-out on session state so ended sessions are
never written to, and reap existing rows for ended sessions. Excluding only
`ended` is the conservative choice: `idle` and `needs_input` sessions may still
take another turn, and their notices are legitimately pending.

Open question for the fix: `sessions.state` at WRITE time is not the same as at
delivery time. A session that ends between write and delivery still leaves a
dead row, so a reaper is needed regardless of the write-time filter.

## Not a defect — notice 625

The E-1898 session observed that notice 625 was `notified = 1` yet "never
surfaced in my transcript." That is expected: `additionalContext` is injected as
a system-reminder into the model's context, not as visible transcript text, so
its absence from the transcript is not evidence it was never injected.
`.endless/logs/session-notices.jsonl` records every delivery and is the place to
confirm or refute injection.



---

# Third post-land defect: `esu` makes the user's own edits self-suppress

Found 2026-08-20 while working E-1997. The user confirmed that task from the
CLI while a session held it, and the session was never told — it went on to
report the task as `unverified` two turns later.

## The chain, each link verified

1. `tasks.changed_by_session` for E-1997 named the session that was *holding*
   the task, not the actor. The user made the change.
2. It was stamped that way because the user's shell had `ENDLESS_SESSION_ID`
   exported by `esu`. The same land run printed "Derived task ID from session: E-1997",
   so the routing was demonstrably active.
3. `tasks_notify_sessions` ends with
   `AND st.session_id IS NOT NEW.changed_by_session`, so the holding session was
   excluded from the fan-out.
4. `SELECT ... FROM session_notices WHERE task_id = 1997` returns **zero rows**.
   The notice was never created — nothing was lost in delivery.
5. Reproduced against the trigger directly (sandbox DB, synthetic task + one
   holding session):
   - `changed_by_session = NULL` (bare terminal) → **1** notice
   - `changed_by_session = <the holding session>` → **0** notices

## Why the premise is wrong

`stampTaskActor`'s comment states the assumption outright:

> An empty Actor.SessionID stamps NULL, which suppresses nobody. That is
> correct, not a fallback: a NULL actor is the user editing from a bare
> terminal, which is the case this whole feature exists for.

The bare terminal is not the common case, because Endless tells the user not to
have one. `endless guide` instructs `eval "$(endless shell-init)"` then `esu`,
whose entire purpose is to export `ENDLESS_SESSION_ID` so subsequent commands
route through the session's worktree. Following the documented workflow converts
every user-side task edit into a change attributed to the agent.

The result is inverted: the notices that DO get through are the low-value ones
(background triage, actor NULL), and the one that matters most — the human just
changed the task you are holding — is the one guaranteed to be suppressed.

## The distinction the schema is missing

`ENDLESS_SESSION_ID` answers "which session's worktree should this command route
through". Self-suppression needs "which agent made this change". Today one value
serves both. A human at a shell prompt with `esu` active is a different actor
from the agent running in that session, and only the second should suppress.

Worth considering (not a decision): stamp the actor from the hook-observed agent
session (the session whose harness ran the command) rather than from the routing
env var; or have the CLI distinguish an interactive TTY invocation from one made
by an agent's tool call, and stamp NULL for the former.

## Related but separate

Two notices for E-1998/E-1999 (background triage, actor NULL) were rendered,
logged to `.endless/logs/session-notices.jsonl` at 08:48:14Z, and marked
`notified = 1`. The session did not act on them. Whether they reached the
session's context is unresolved: the transcript's user message at 08:48:13.986Z
carries no injected text, but it is not established that Claude Code records
injected `additionalContext` in the transcript at all, so that is not proof of
non-delivery. Worth settling before treating it as a delivery bug — the code's
own comment says marking a notice delivered that was not injected is the one
thing that must never happen.

The hook side is confirmed working. Driving the real global binary with a
UserPromptSubmit payload for this session returns exit 0 and exactly:

```json
{"additionalContext":"Active task: E-1997 (confirmed · now) — Fix unsupported-harness banner leaking into unrelated Bash tool output on Claude Code Desktop."}
```

Correct shape per the Claude Code hooks contract (top-level `additionalContext`
for UserPromptSubmit and SessionStart; nested under `hookSpecificOutput` only for
PostToolUse), and it carries the current status. So the injection payload was
right; only the notice was missing.


