# The primary defect: test 1 reads as a purity test

Bullet 1 — "Could it reasonably be done now, inside the work already underway?
Do it" — **was** in the handoff. It is not missing. It was read and rejected,
and the rejection was reasonable on the wording: "inside the work already
underway" is a **topical membership** question. Asked of a stray id in a guide
paragraph while the task was `verb update`, the honest answer is "no, that is
not part of this task's subject" — so the session falls to the next branch.

`docs/guide/tasks.md` then hardens the topical read rather than softening it:

> "Reasonably, inside the work already underway" is a real bound, not a
> license. A drive-by that is *unrelated* to what you are changing stays a
> separate task…

So the guide makes bullet 1 about **kinship** while ED-1550 makes filing about
**arithmetic** — the file-to-close ratio. For a cheap, unrelated fix those pull
opposite ways, and kinship wins because it is the test actually printed at the
decision point.

This is a repeat, not a one-off. The same reasoning — "it wasn't a *pure*
example of the other task" — has been given before as the justification for
filing rather than folding. Purity is the wrong axis. What matters is what the
fix costs to make and to review, and whether carrying it in this diff would
mislead a reviewer; not whether it is a clean instance of this task's category.

**Proposed:** bullet 1 states the axis and names the failure mode, e.g.

    - Could it reasonably be done now? Do it — note it in the commit message
      and say so in your reply. The test is COST, not kinship: how big is the
      fix, and would carrying it here mislead whoever reviews the diff. Do not
      let purity get in the way of proficiency — "not a pure example of this
      task" is not a reason to file. A one-line fix in a file you already have
      open is cheaper to make than the task row describing it.

That keeps the real bound (a large or risky unrelated change still splits out,
for diff, land-coupling and revert-blast-radius reasons) while removing the
category test that currently decides it.

# Folding in is not done until the plan records it

A session that folds discovered work into the task already underway must also
**amend that task's plan and/or outcome** to say so. The commit message and the
reply to the user are both transient: the commit is found only by someone
already reading that diff, and the reply is gone the moment the session is
archived. The task row is what posterity reads, and a task whose plan describes
only its original scope is a lie about what shipped under it — which is the
same defect that made "reopening shipped work forces a re-read of the whole
plan to find the new part" a rule in the first place.

So the do-it-now branch has three obligations, not two: do the work, note it in
the commit, **and record the grown scope on the task**.

**This has a trap that must be spelled out in the instruction.** Writing plan
text onto a task in a terminal status re-specs it:

    $ endless task update E-2114 --text-file plan.md
    • Status: assumed -> revisit
    E-2114 was 'assumed'; plan text changed → status set to revisit
    (pass --keep-status to suppress for a typo/formatting-only edit)

and `revisit` cannot go back: reachable from `revisit` are untriaged,
submitted, underway, declined, obsolete — **not** `assumed`. So a session that
obeys the new rule the obvious way destroys the verification state its user just
granted, and the only way back is a full claim/verify cycle the user has to
re-close. Hit live on E-2114 while writing this analysis.

The instruction must therefore carry `--keep-status`, not merely mention that
it exists — and it is worth asking whether recording delivered scope on a done
task should be a re-spec at all, or whether `--keep-status` is the wrong
default for a plan edit that only appends history.

# Secondary: the handoff drops the framing and the override

`docs/guide/tasks.md` opens the discovery section with **"Filing is one of four
answers, not the default"** and closes it with an override:

> One case overrides test 1's bound: a drive-by you genuinely cannot complete
> the task without — a broken build, **a test that fails for an unrelated
> reason**. Fix the minimum that unblocks you even when it is otherwise out of
> scope, and say so explicitly in your report.

`internal/templatecmd/templates/handoff/_mechanics.tmpl:28-36` compresses the
four tests into three bullets and keeps neither. `otherwise file it` is left as
the terminal branch, and nothing puts the filing *decision* in front of the
user.

# The missing branch: ASK, don't decide

The branch to add is not "tell your user and file nothing". A session that
decides unilaterally not to file is as wrong as one that files reflexively —
it just fails in the direction that loses real findings silently instead of the
direction that inflates the backlog. The agent has the context to explain a
finding; it does not have the standing to rule on whether that finding earns a
row.

So the branch is a **question**, and it has a shape:

- **Enough detail to understand the purpose, and no more.** What was observed,
  and why it would matter to someone who was not in this session. Not a repro
  transcript, not a diff, not a plan.
- **The case FOR filing.** What goes unfixed or unremembered if this is
  dropped; who trips over it next; whether it compounds.
- **The case AGAINST filing.** Review and scheduling cost; whether an existing
  task already owns the area; whether it is a symptom of something already
  filed; whether it is small enough that noticing it again later costs less
  than carrying the row.
- **A recommendation**, so the user is ratifying a judgement rather than doing
  the triage themselves.

The user answers; the session files or drops on that answer. This keeps
ED-1550's arithmetic — filing stays the exception — without making "drop it"
a decision the agent makes alone.

# Third: the override's condition contradicts its own example

The condition is "you genuinely cannot complete the task without"; the example
is "a test that fails for an unrelated reason". Those are not the same set — an
unrelated red test usually does *not* block completion. E-2114 landed clean
with `test_no_self_dev_ids` red for a reason that had nothing to do with it.

So even a session that reads the full guide finds the example matching and the
condition not matching, and falls through to filing. The condition wants
rewording around the *regression the session is obliged to run and report*,
not around being blocked.

# Live case

E-2114's project-wide regression turned up a stray self-dev id in
`docs/guide/orchestration.md` — one sentence, in a tree already open, in a
suite the session had just run. It was filed as E-2119 instead of folded in,
which cost an extra worktree, an extra task row, a partial-state `task claim`
error, and two round trips with Mike. E-2119 was then obsoleted and the fix
folded into E-2114 by hand.

# Constraint

Keep test 1's real bound intact. The guide is right that a *large* unrelated
drive-by inflates the reviewed diff, couples two changes into one land, and
widens a revert's blast radius. The fix is to change what the bound is measured
in — cost and reviewer confusion, not topical kinship — and to route the
residue to the user as a question rather than straight into `task add`.

---

# Second root cause: the status-change surface trains narration

Same session, different file, same shape — what the agent is told at a decision
point determines what it does next.

Every status-affecting `task update` prints two things: the field render
(`• Status: assumed -> revisit`) and an advisory naming `--keep-status`. Both
are addressed to the agent. The agent relays them to the user as if they were
news, which spends the user's attention — the scarcest resource in the loop —
on a completed, correct transition it can do nothing about.

This is not a first offence and not a wording problem. A comment beside the
`auto_untriage` emitter in `task_cmd.py` records a previous session diagnosing
the identical loop — *"observed four times in one session"* — and fixing it by
**rewording** the advisory. It did not take: the reworded line was relayed twice
more in this session alone. The stimulus is the presence of agent-addressed
output, not its phrasing, so a third rewrite would fail the same way.

## Fix, two halves, landed together

**1. Audience-gate the output.** Keep everything exactly as it is for a human
running the command interactively — the status change and both advisories are
genuinely useful there. For an agent, print only the field changes it asked
for. Nothing about status it did not ask about, and no advisory.

Reuse `agent_help.agent_view()`. That predicate is already the consolidated
answer to "is an agent reading this" — E-1966, E-2006 and E-2097 each folded a
competing spelling into it, so a fourth spelling is the specific mistake this
module's history warns against. `--help` and refusals already gate on it; the
status-change output becomes a third caller.

**2. Narrow the transition that fires under it.** A plan-text edit on a
terminal status (`confirmed` / `assumed` / `completed`) must not move the
status. Today it auto-flips to `revisit`, inferring "the work needs redoing"
from an edit that, on a finished task, usually records what shipped — which is
exactly the obligation the fold-in branch above now imposes.

No new flag for the inverse: `{From: Assumed, To: Revisit, Actor: ActorUser,
Label: "reopens — shipped work found wrong"}` already exists in
`internal/taskstatus/transitions.go`, alongside the same edge from `Confirmed`
and `Completed`. Reopening is already user-owned and already spelled
`--status revisit`; the defect is the system firing a user's edge on an
inference. Non-terminal statuses keep today's behaviour, where the auto-flip is
cheap and usually right.

## Why both, not either

Half 1 alone hides a transition the agent caused: it would silently knock a task
off `assumed` and not know. Half 2 alone leaves the field render and the
advisory in place for the agent to narrate. Together there is no wrong
transition to hide and nothing agent-addressed left to relay.

`--keep-status` itself survives untouched. Its other job — skipping the
re-triage model call on a cosmetic description edit — is a real spend control
and unrelated to this.

## Live case

Recording E-2114's grown scope in its plan — obeying the rule this very task
adds — flipped it `assumed → revisit`, and `revisit` cannot reach `assumed`
again. The verification Mike had granted an hour earlier was destroyed by the
act of documenting what shipped, and had to be walked back through
`underway → unverified` for him to re-close by hand.
