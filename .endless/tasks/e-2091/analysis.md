## What is missing

Endless installs six Claude Code hooks — PreToolUse, SessionStart,
UserPromptSubmit, PostToolUse, Stop, SessionEnd. None reports that Claude Code is
asking the user for permission, so a session sitting on a prompt reads `working`,
indistinguishable from one doing work. `Notification` is the event that reports
it and was never wired.

E-1976 built the display side and left the producer out: `actWaiting` is the
board's first rank, wears the warning glyph, renders bold, and `classify()`
already routes a state to it. The rank has no producer. That is this task.

## What "waiting on the user" means here

Decided with Mike (2026-09-03): a session is waiting on the user whenever it has
paused for input, whether or not it asked a question. A finished turn and a
question are the same thing for this purpose — the ball is in the user's court —
which is exactly what his tmux window flag already signals and has proven
accurate in practice.

That definition is what keeps the task small. There is no question-versus-
finished classification, no inspection of the turn's final message, and no model
call. `idle` already means "the turn ended", so the work is to add the one
paused-for-input case Endless cannot currently see — a permission prompt — and to
make the vocabulary say that both are the same kind of waiting.

Explicitly not coupled to the minimizer. It is disabled until further notice,
will be renamed when it returns, and nothing on this task's critical path may
depend on it.

## What the current hooks reference says

Checked against the live documentation rather than recall, which was two months
and twenty-four events out of date.

Claude Code exposes 33 hook events. `Notification` carries a `notification_type`
field whose documented values include `permission_prompt` and `idle_prompt`.

Two findings that shaped the design:

- `agent_needs_input` exists but is scoped to agent-team teammates about to go
  idle, alongside a `TeammateIdle` event. It is NOT a main-session "the agent
  asked you something" signal, and designing on it would have been wrong.
- `PermissionRequest` is now a first-class event carrying `tool_name`,
  `tool_input` and `tool_use_id`. It is nevertheless the wrong signal for this
  task: it fires whenever a permission DECISION is needed, which an allowlist or
  another hook can resolve without the user ever seeing anything.
  `Notification` with `permission_prompt` fires when the user is actually
  prompted, which is the fact this task records.

## Why a new state rather than `needs_input`

`needs_input` looks like the obvious home and is not. It is ENFORCED:
`hookcmd.sessionMayWrite` refuses file writes from a session in that state under
`enforce` tracking, with the message "you asked your user something and the
answer has not arrived. There is no command for you to run."

That state means the AGENT asked the USER something. A permission prompt is the
reverse — the agent is trying to act and the harness is asking — and routing one
to the other would refuse the session's next write after the user approved.

The near miss is worth recording because it was not carelessness. The gate's
admission set was a Go `switch` with a silent `default: return false`, so a new
state joined the refused set without anyone deciding it should. That is why
E-2105 came first: with `MayWrite` a named group, a state cannot be added
without the question being answered.

## Why `prompted` may write

Decided with Mike (2026-09-01), from the gate's own stated rule rather than from
preference. The gate admits a session that "has DECLARED what it is working on
and is live-and-acting", and already glosses `idle` as admissible because "a
write from an idle session is by definition mid-turn". A prompt-blocked session
is mid-turn by exactly that reasoning, and the tool it waits on is its own.

The decisive argument was the asymmetry of failure modes. Both readings need a
clearing transition. If the clear misses under this one, a stale glyph sits on
the board until the next Stop — cosmetic, self-correcting. If it missed under the
other, every prompted session would be refused its next write and told there is
no command to run, which is the failure E-2093 was cleaning up after.

## The `needs_input` writers are the real defect underneath

Two writers put `needs_input` in the database and neither means a person is being
waited on: `monitor.InitSession` writes it as a row's initial value at
SessionStart, and the revive-an-ended-row CASE writes it on any incoming hook
(E-1686). Nothing else writes it.

Measured while E-1976 was built: 34 rows in `endless` carrying it, every one last
active 25 to 71 days earlier, none on a pane that still existed — sessions that
registered and never had a turn.

E-1976 responded by excluding the state from the board. Mike's correction
(2026-09-03): that is backwards. Hiding them is how they rotted unseen, the board
already caps each rank at ten rows with a footer naming the remainder — machinery
built for exactly this — and revealing them is what provides the mechanism to
resolve them.

So this task reveals the rows and fixes the writers, and deliberately does not
retire the existing 34. They are surfaced to be dealt with, not migrated away in
the dark. Once drained, `needs_input` has no writer and no rows, and means only
what the declaration gate says it means.
