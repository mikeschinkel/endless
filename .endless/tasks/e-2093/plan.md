# Plan — restore the wake edge, then make the gate ask the right question

Five arms. Arms 1-3 are one subject — the session-state machine and the verbs
that lie about it — and Arms 1 and 2 should land together. Arm 4 is unrelated in cause and can land in
either order. Arm 5 is documentation and lands with whatever it describes.

## Arm 1 — a turn that starts marks its session working

`Stop` sets idle. Nothing sets working again. Add the missing half.

- On turn start, a session that HOLDS A TASK (`sessions.task_id IS NOT NULL`)
  and is `idle` becomes `working`. Holding a task is the precondition: this
  restores a declaration already made, it must never manufacture one.
- `needs_input` is NOT woken by this. It means a human was asked something and
  has not answered; only the human answering ends it. Waking it would erase the
  one state that says a session is waiting on a person.
- `ended` is not woken either. Reviving a dead row is `bind`'s job (E-1686) and
  it deliberately revives to `needs_input`, not to `working`.

**Where the edge goes.** In ONE place that every turn reaches, not in
UserPromptSubmit: a turn begun from a `!` bash-input fires no
UserPromptSubmit, so waking there alone reproduces this bug in a narrower form
that is harder to see. Put it where the session row is already touched each
turn, so the wake cannot be missed by a path nobody enumerated — the same
argument that makes one write-once trigger better than auditing every writer.

The rule is "a session holding a task, observed acting, is working". Express it
that way in the code, not as "on prompt submit", so a future entry point
inherits it.

## Arm 2 — the gate admits a declared session, and stops misdescribing it

The gate admits only `state == 'working'` and sends every other state to a
branch announcing "BLOCKED: No active work session", which is false for an idle
session that holds a task.

- Admit a session that holds a task and is `working` OR `idle`. A write from an
  idle session is by definition mid-turn — writes only happen inside turns — so
  the state is stale, not the agent. With Arm 1 in place this is belt-and-braces
  rather than the primary fix, and it is what keeps a missed wake from stranding
  anyone again.
- Keep refusing `needs_input` and a session with no task. Those are the cases
  the gate exists for, and the second is the only one whose message is currently
  accurate.
- The refusal must name a command that works from the state that produced it,
  and must distinguish "you have not declared a task" from "your session is in a
  state that cannot write". Today one message serves both and is wrong for one.

**Do NOT reach for `--force`** anywhere in this arm. Repairing a session field
by demoting a task is the trade this task exists to remove.

## Arm 3 — retire `--force` on claim; two named flags in its place

`--force` spells two unrelated decisions, and only one of them is documented.
Its help text describes the settled-status demotion alone; claiming when NO
Claude session can be resolved is an undocumented second behaviour of the same
flag, met only when session resolution fails at runtime. One flag, two
decisions, one of them invisible, is why it is reached for as the answer to "I
cannot write" — which is neither of them.

Both halves get named. `--force` is removed.

**`--unattended` — keep this half.** Claim a task with no Claude session bound:
manual work, a plain shell, cron. Real and currently unreachable except by
accident.

Do NOT name it `--no-session`. That spelling is already a GLOBAL flag, consumed
in any position, meaning "no Claude session to attribute events to". The two are
adjacent enough to be confused and different enough to matter. Trace whether the
global flag already reaches claim's session resolution and record what it does
there; if it does reach it, that is a third undocumented behaviour and it is in
scope for this arm.

**The settled-status half is removed, with no replacement flag.** Re-claiming a
settled task demotes it to `underway`; the route to that is `endless task update
<id> --status revisit` followed by an ordinary claim, which says what it is
doing and leaves an auditable status transition instead of a silent demotion
riding a flag on an unrelated verb. Claim's refusal names that route.

This is the posture the guide already states twice — `task update --status` has
no `--force` ("the fix is to correct the call"), and the removal refusal has
none because "`--force` is exactly the flag a mistaken caller appends after
reading a refusal". Claim's demotion bypass is the same shape and goes the same
way.

`task spawn --force` demotes a done-ish task the same way and follows claim
through the same door: its demotion bypass goes, and its refusal names the
revisit route. Two verbs disagreeing about what `--force` means is the
condition that made this flag reachable as a catch-all in the first place.

Whichever way that lands, `--force` warns for one release and is then gone. It
must not survive as an alias for either half: an alias that still spells two
decisions is the defect.

Folded in here rather than filed separately, per ED-1550: the finding belongs in
an open task on the same subject.

## Also fix while here

`bind`'s docstring says "for status-bar display only". It sets
`sessions.task_id`, which ED-1560 makes the ownership record. Say what it does.

## Arm 4 — the handoff says where a verify suite goes

One sentence appended to the `handoff_verify` partial, plus its render test:

    ... folding this task's own tests into that one suite as a fail-fast check.
    The suite goes at `.endless/tasks/e-NNNN/verify.sh` and sources
    `../_harness.sh` as its first statement; see `.endless/tasks/CLAUDE.md`.

Edit the shared partial, not the wrappers — it reaches all ten handoffs at once,
the pattern E-1947 and E-2073 established. Confirm the wording against
`.endless/tasks/CLAUDE.md` as it reads when this is done rather than copying the
sentence above: a handoff teaching a stale rule is worse than one teaching none.

## Arm 5 — the guide and the handoffs stop teaching the old behaviour

Every arm above changes something an agent is told elsewhere. A change that
lands without these is a change that keeps being undone by its own
documentation.

- `docs/guide/orchestration.md` documents `endless task bind` as first-set-only;
  it must also say what bind does and does not do to session state, now that the
  distinction is load-bearing.
- The same file lists `endless task spawn --force` as "allow spawn on a done-ish
  task (demotes status)" and states that "`--force` governs only the status
  demotion". Both statements move with Arm 3.
- Wherever the guide describes the write gate, it must describe what is actually
  refused — a session with no task, or one in `needs_input` — and not repeat the
  claim that an idle session has no work session.
- `handoff/_mechanics.tmpl` and `handoff/claim.md.tmpl` carry the one-session-
  one-task and cwd-gate rules the spawn and claim texts share. If a stranded
  session no longer needs a command to recover, the handoffs must stop implying
  it does. Edit the shared partials, not the per-type templates.
- `.endless/tasks/CLAUDE.md` is untouched by this task; do not edit it here.

Arm 4's sentence lands in the same partial tree, so do Arms 4 and 5 together and
render once.

## Boundaries

- Does NOT extend the gate to `cat > file`. Both stranded sessions finished
  their work that way; worth knowing, not this task.
- Does NOT change what `idle` means or when Stop sets it.
- Does NOT touch `sessions.task_id`'s write-once rule (ED-1560).
- Does NOT reach sessions already running when Arm 4 lands.

## Verify

1. A session that has completed a turn can write on its next turn, with no
   command run and no task status changed.
2. The same for a turn begun from a `!` bash-input.
3. A session in `needs_input` is still refused, and is not woken by Arm 1.
4. A session holding no task is still refused, and its message still says so.
5. The refusal a stranded session sees names a command that works from that
   state — verified by running it, not by reading it.
6. Reporting `unverified` does not, by itself, change what a session may do.
7. `--unattended` claims with no Claude session and does nothing else; it is
   distinct from the global `--no-session`, and neither silently does the
   other's job.
8. `endless task claim --force` warns and still works for one release, then is
   gone; it survives as an alias for neither half.
9. `task update --status revisit` + an ordinary claim covers every case the
   removed settled-status bypass covered, and claim's refusal names that route.
10. `task spawn --force` no longer demotes, and its refusal names the revisit
    route, exactly as claim's does.
9. All ten rendered handoffs carry the suite-location sentence, and the render
   test fails if the partial loses it.
