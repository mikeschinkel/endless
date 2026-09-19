# Why a session loses the ability to write, and cannot get it back

Observed twice, independently: ES-1159 finishing E-2023, and ES-1163 finishing
E-1661 (filed as E-2096, since marked a duplicate). In both, every Write/Edit
was refused for the rest of the session, and the work was completed through Bash
heredocs.

## The state machine is missing its wake edge

`IdleSession` has exactly one non-test caller: the Stop hook. So a session is
marked idle when a turn ends — which is correct and worth keeping.

Nothing marks it working again. `TouchSession` preserves the existing state by
design (its conflict branch revives only an `ended` row), and the only writers of
`working` are `BindSessionToTask`, reached from the hook's claim path, and
`StartChatSession`. Neither runs on a following turn. A session that completes
one turn cleanly is already stranded; it discovers this at its next write.

Reporting `unverified` is not the trigger, despite the correlation — it is
simply what a session tends to do last before a clean Stop.

## The gate asks a question that stops being true

The PreToolUse gate admits exactly `state == 'working'`. Every other state falls
through to a branch that announces "BLOCKED: No active work session", which for
an idle session holding a task is false: the session exists and has declared its
task. Idle was never judged and rejected — it is unenumerated, and meets a
default deny that then misdescribes it.

What the gate actually wants to know is whether this session has declared what
it is working on. `sessions.task_id` answers that and stays true. `state ==
'working'` is a proxy for it that expires at the end of every turn.

This also means an idle session attempting a Write is, by definition, mid-turn:
writes only happen inside turns. The state is lying, not the agent. So admitting
a session that holds a task is not a weakening — the declaration the gate exists
to compel has already happened.

## Why the refusal's own advice cannot work

The refusal names `endless task claim <id>`. That command cannot resolve it:

- On a task past `underway`, claim refuses on STATUS before reaching any
  question of who holds the task.
- `--force` clears that check but then demotes the task to `underway` — damaging
  a task's status to repair a session field.

`endless task bind` is the documented non-demoting alternative and is what
claim's refusal points at. It cannot resolve it either, for a different reason:
its event executor deliberately preserves live state, so a successful bind sets
`task_id` and leaves an idle session idle.

## What that preservation is worth

It is not purely an oversight. It protects `needs_input`: a session genuinely
waiting on a human must not be flipped to `working` because someone else bound a
task to it. For `idle` it protects nothing — but a bind-side fix is treating the
symptom, because the session would strand again at the end of the next turn.

## `bind` is larger than its own description

Its docstring says "for status-bar display only". It sets `sessions.task_id`,
which ED-1560 makes the ownership record. Setting ownership is not display, and
the understatement is why the verb reads as too small to be the answer here.

## `--force` on claim carries two unrelated meanings

One bypasses the settled-status demotion; the other claims when no session
exists at all ("manual work, no Claude assistance"). ED-1560 constrains neither.
A single flag spelling two decisions is why it keeps being reached for as the
answer to "I cannot write", which is neither of them.

## An adjacent observation, not this task's problem

The gate covers Write/Edit and not `cat > file`, so both stranded sessions
finished their work through heredocs. It inconveniences a compliant agent
without stopping a determined one.

---

# Second item: a spawned session is never told where its verify suite goes

Unrelated in cause — it shares a session and a size with the item above.

The `handoff_verify` partial tells a spawned session how to RUN its suite and
never says where the suite FILE lives or that it must source the shared harness.
A session writing one from scratch reaches for the shape visible throughout git
history and `.endless/LESSONS.md`: the `tests/tasks/e-NNNN-verify.sh` path
E-2023 retired.

`.endless/tasks/CLAUDE.md` states the rules, but a directory-level CLAUDE.md
only reaches a session already working in that directory — the thing these
sessions fail to do. The rules are in a room the session never enters.

The cost is not the wrong path: it is that correcting the session sends it into
a long investigation of E-2023 rather than a two-line fix.
