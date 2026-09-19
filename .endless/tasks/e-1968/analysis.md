# Evidence

Observed 2026-08-13. `endless task reopen E-1859` destroyed ES-1046's
session→task binding without saying so, and neither the user nor the agent that
ran it knew until the loss was investigated hours later.

## What it does

`_reopen_task_core`'s own docstring: "Validates eligibility, releases any
lingering session→task binding, and emits `task.status_changed`."

So the release is intentional. What is not intentional is that it is invisible.
The command printed only:

    Updated E-1859 (Implement the triage job that evaluat...) (text: present):
    • Status: assumed -> revisit

No mention of the released binding. The only record is
`~/.config/endless/log/user-machine.jsonl`:

    ts 2026-08-13T10:05:20Z  session 6b6413b7-...  old_active_task_id 1859
    reason "release"  caller "events.execTaskReleased"

plus a `task.released` event in the ledger.

## The help text says the opposite

`task reopen --help`: "Metadata-only: no worktree creation, no session
binding." That reads as "does not create a binding." It also destroys one.

## Why it matters beyond the surprise

Two consequences, both realised in the incident:

1. `session resume E-NNNN` (the task-keyed form, `monitor.resumeByTask`)
   resolves through `active_task_id`. Clearing it makes the prior session
   unfindable by task. Resume by session id still works, but only if you
   already know the id.

2. Reopen moves the task to `revisit`, which is NOT in
   `_CLAIM_REQUIRES_FORCE`. On `assumed`, `task spawn` refuses with:

       E-NNNN is 'assumed'; pass --reopen to reopen-and-spawn, or run
       `endless task reopen E-NNNN` first.

   `--reopen` inherits the most-applicable prior ended session by default —
   the correct outcome. The other branch that same message offers, running
   `task reopen` first, drops the binding AND moves the status out of the
   guarded set, so a subsequent plain `spawn` proceeds silently. The error
   message routes the user into the trap.

## Mike's design call

Keep the ability to release, but require `--force` for it.

Whatever the gate, the release must be announced — one line naming the session
and task released. A silent destructive side effect on a command documented as
"metadata-only" is the core defect; the flag is the secondary fix.

Consider also whether that error message should stop offering the bare
`task reopen` branch, given where it leads.



---

# 2026-08-20: ED-1560 supersedes §1's design

§1 gates `task reopen`'s release behind `--force` and makes the announcement
unsuppressible. ED-1560 (accepted) removes the operation being gated:
`sessions.active_task_id` is write-once — set at claim, never NULLed and never
repointed. A `--force` flag that permits an unbind is a permission slip for
something the invariant forbids.

Revised shape for §1: `_reopen_task_core` stops emitting `task.released`
entirely. No flag, no announcement, nothing to gate — reopening a task changes
task state and leaves the session binding alone. The misleading help text ("no
session binding") still needs fixing, and the refusal draft in §1 becomes
unnecessary rather than reworded.

## Why this is not theoretical

Traced on E-1917 while diagnosing why its session could not be resumed:

```
task.claimed                                            2026-08-07
task.landed          86b4740                            2026-08-08   ← binding survives
task.released                                           2026-08-15T06:48:31Z
task.status_changed  assumed → revisit   (same instant)
task.fields_updated  analysis := "# E-1917 post-land defects…"
```

One `task reopen` invocation released the binding, flipped the status and wrote
the defect report. Afterwards resuming by task ref and by session ref both fail, and resume
reports that the session never claimed a task —
it reads only the current `active_task_id`, so it cannot tell *released* from
*never claimed*. The session's transcript was intact the whole time; only the
pointer to it was gone. An announcement at reopen time would not have helped: the loss is
discovered a week later, by someone who was not at that terminal.

## Open question for §5 and the `task release` verb — needs Mike

§5 has `task release` move an `underway` task to `revisit`. But `release`'s
defining act is clearing `active_task_id`, which ED-1560 forbids outright. So the
verb's future is a design call, not an implementation detail, and it is not
settled here. Roughly: does `release` become status-only (leaving the binding,
so the session stays reachable), or does it go away, or does ED-1560 carve out an
explicit exception for a user-invoked release? §5 cannot be built until that is
answered.

## Same defect, two more call sites

`monitor.CompleteTask` clears `active_task_id` on completion, and
`monitor.StartChatSession`'s `ON CONFLICT … DO UPDATE SET active_task_id=NULL`
clears it for an already-known session id. Both need whatever §1 settles on.
Out of scope: the task-removal clear and the FK's `ON DELETE SET NULL` — the task
itself is gone in those cases.



## Sequencing — E-1969 enforces this with a trigger

E-1969's write-once addendum (added 2026-08-15 from E-1952's session) installs
`sessions_task_id_write_once`, a BEFORE UPDATE trigger that RAISE(ABORT)s on any
change to a non-NULL `task_id` — which covers NULLing as well as re-pointing,
since `NEW.task_id IS NOT OLD.task_id` is true when NEW is NULL.

That makes §1 load-bearing rather than cosmetic. E-1969's own note: "E-1968
(movement verbs) lands with or before this trigger, or every re-pointing verb
starts aborting on contact." `task reopen`'s release is exactly such a verb, so
if §1 still emits `task.released` when the trigger lands, reopen begins failing
at the DB. Removing the emission is therefore the only shape of §1 that works;
`--force` would produce a flag whose success path aborts.


