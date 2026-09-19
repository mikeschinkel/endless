# Design — the minimizer autoresearch loop

Synthesized in E-1959 (brainstorm). Governing decisions: ED-1555 (free-form
span-scoped labels), ED-1556 (user is a sensor, not a gate), ED-1557 (rewrite
and deduplicate, do not merely delete). Filed as ONE task per ED-1550; grow
children only if it actually stalls.

## What E-1953's planning session decided that now LAPSES

All of it was contingent on a human viewer being built. It is not:

- Side-by-side RAW/MINIMIZED renderer, and the table/code-fence wrapping problem
  it inherited.
- `task report` restructured into `submit` / `log` subcommands, and the
  seven-call-site rename that implied (hookcmd/claude.go, relay_gate.go, the
  handoff templates, task_cmd.py, the guide docs, plus two verify scripts).
- "No retroactive labelling" — still true, but for a different reason: there is
  no review surface to label from.

Do not resurrect any of it. The surviving human-facing surface is one command
(below).

## Objective of the minimizer (ED-1557) — ships as a change

Replaces the shipped `MINIMIZE` prompt in `src/endless/report_prompts.py`:

> Remove things the user can ALREADY see in `session status`; things ALREADY
> written in the task plan or analysis or anywhere else they will have to review
> anyway; replace jargon with clear English; remove or greatly minimize puffery;
> remove anything that is the agent "showing its work."

Deduplication against what the user already has is the novel half, and the half
that forces the fetch policy below.

Delete the deletion-only instruction (l.123-125) and the not-shorter instruction
(l.60).

## Ground truth — two channels, both in the flow of work

1. **Free-form span-scoped labels** (ED-1555), written into the user's ordinary
   replies. `$TOKEN "quoted span"`. Extraction is regex-shaped — something in
   the spirit of `^\$([A-Z]+)` — but **do not lock in that pattern**; it was
   composed ad-hoc as a conceptual example, not as a specification. Synonym
   grouping is the judge's job. Must not fire on a label token legitimately
   quoted inside a reply — same hazard `report_prompts.py:32-34` records for the
   denylist.

   **Consistency is leaned on, not enforced.** When a new token clusters near
   one the user has used before, the loop asks in-band whether to merge; the
   user may answer or ignore. Short-term scoring noise from a drifting
   vocabulary is the right trade — a vocabulary the user cannot recall in the
   moment produces NO label at all, and label supply is upstream of everything
   else in the loop. Drift becomes observable rather than silent.
2. **Paired A/B**: on a sampled fraction of turns, produce TWO minimizations and
   let the user pick. This is the only source of a real counterfactual, and it
   is the exact judgment promotion requires ("B beats A"), which an absolute
   label does not supply. Doubling the minimizer call on sampled turns is
   accepted cost.

The A/B sample rate is tuned by asking the user directly, in the reply itself:
each A/B presentation ends by asking whether they want to see these more often
or less often. No config knob — the only seat that can judge the
annoyance-versus-value trade sets it.

### How A/B is presented, and why

The Stop hook compares the **final message** against `task report`'s output, and
a tool call happens BEFORE the final message. So an agent cannot send the A/B
block as its reply and then ask which one won — the reply ends the turn. That
constraint, not aesthetics, picks the shape.

`AskUserQuestion` cannot carry the content either: labels are a few words,
descriptions a sentence. Its `preview` field could hold A and B, but a full
markdown reply rendered in a monospace box reads badly and multi-KB previews are
untested.

**Ship this (option a):** `task report` emits BOTH variants as one sanctioned
output, separated so they are unmistakable —

    ─[Option A of B]────────────────────
    …variant A…
    ─[Option B of B]────────────────────
    …variant B…
    ────────────────────────────────────

— the agent sends that verbatim, and the user picks in their next reply. No gate
change, no new UI, usable immediately.

The pick needs no new parser: `$A` / `$B` is already ED-1555's mechanism, and it
composes — `$B "this sentence"` means "B wins, and that span is still bloat."
Pick and critique in one line.

**Forward-compat worth taking now, cheaply:** make the gate accept ANY OF N
sanctioned texts rather than exactly one. That is the only blocker between
option (a) and both later shapes — `AskUserQuestion` previews with the agent
sending the winner, and a Go/BubbleTea left/right TUI. Explicitly deferred: the
TUI is a later enhancement, not part of getting something usable.

**The doubling.** On sampled turns the user reads the loser too. The minimizer
exists to cut what they read, and A/B doubles it. Two separate things follow —
one about measurement, one about display.

**Measurement: exempt A/B turns from length analysis.** Score each variant on
its own length; never count the doubled total against the minimizer. The
doubling is the cost of the experiment, not a failure of the prompt. Fair
because it is opt-out: a user who tunes the minimizer to their satisfaction
turns the optimizer off and never sees a pair again.

**Do NOT instead instruct the minimizer to aim shorter on A/B turns.** That is a
different lever and it poisons the result: both variants would be produced under
a length constraint production never applies, so the comparison would say which
prompt wins AT HALF LENGTH — promoting a winner never tested under the
conditions it will actually run in.

**Display: preview, then read in full on demand.** Show the first few lines of
each variant inline, and let the user open either in full, with color, via
`endless session turn A|B -p`. This reuses the command already being built
rather than inventing a terminal diff layout, and it needs no special case for
the hard scenario (A and B diverging broadly).

Considered and rejected: rendering the shared text once and duplicating only
divergent spans. Correct in principle, but substantial effort and hard to make
graspable in a terminal.

Consequence for the read command: `session turn` must address variants of the
PENDING turn, not only past turns — `A`/`B` join the offset as arguments.

## The judge

Scores every turn, labelled or not, from `(user_prompt, raw, minimized,
fetched_context)`. Dense and unlimited — the only thing that can evaluate a
variant over the whole corpus.

Human labels do NOT score prompts; they **calibrate the judge**. The judge
commits to a prediction BEFORE seeing the user's reaction (which of A/B they
will pick; whether they will flag the turn). Rolling prediction-vs-outcome
agreement is the health number. Per ED-1556, low agreement raises the A/B
sample rate and is announced in-band; it never halts promotion silently.

## Scoring — vetoes, not a weighted sum

1. **Mechanical invariants** — no model call. Fenced code blocks and tables
   survive byte-for-byte; a command the user must run survives. NOTE: the
   subsequence check (minimized ⊆ raw) **dies with deletion-only** — rewriting
   is the objective now, so only the byte-exact protected-content checks remain
   mechanical.
2. **Fidelity** — the judge.
3. **Compression** — free, saturating.

A variant buying compression at the cost of one invariant violation is not
better; a weighted average would make that trade. Vetoes, then maximize
compression subject to no fidelity regression.

Adding a self-scored compression target to the prompt is a legitimate variant
precisely BECAUSE invariants are enforced separately.

**Rejected as a score component: label count / label rate.** It measures the
user's engagement, not the prompt — turning the gate on will raise it while
quality is unchanged. Instrumentation health metric only.

## Monitoring vs. deciding — do not conflate

- **Monitoring**: rolling metrics over live traffic. "Are we drifting?"
  Confounded by corpus drift. Alarm, not verdict.
- **Deciding**: paired replay of variants over a FROZEN corpus; item difficulty
  cancels. The only thing that may gate a promotion.

## Search space — three axes, jointly, per task type

Variants are **generated by the optimizer**, not hand-written by an agent.
Seeded from known compression grammars and free to diverge: ASD-STE100, Attempto
Controlled English (ACE), Plain English Foundation Standards, CNL-P, RICECO/ICC,
PDL. Plus orthogonal style axes (all-bullets vs. prose).

1. **Prompt text**
2. **Fetch policy** (below)
3. **Bypass threshold** — length under which a reply skips the minimizer
   entirely. Latency and cost are a big factor. 256 was a guess; the shortest
   draft in the corpus is 396 chars and ZERO of 23 passed through untouched, so
   there is no evidence yet about behavior below any threshold.

**Split by task type NOW**, not later — variants keyed `(task_type, hash)`.
Coordinating a follow-up task later costs the user more than splitting up front.
Sample volume is not a constraint worth designing around.

## Fetch policy — and why the corpus row must grow

The dedup objective cannot be scored from the triple alone. Two proposals turned
out to be one mechanism:

- The minimizer **fetches** what it wants (`task show E-nnnn --text`, session
  status, sibling tasks, …), and
- the corpus row **records what was requested and what came back**.

A tool-using minimizer is nondeterministic in its INPUTS, which breaks replay:
A/B over a frozen corpus is meaningless if each run re-fetches live state that
has since changed. Replay must serve context from the record, never re-fetch.
So fetching forces recording; they are the same mechanism.

The set of things it MAY fetch is large and declared. **What it actually fetches
is a JSON policy document the optimizer maintains** — the third tunable axis.
"Give it everything every turn" fails on cost and latency, not principle, which
is exactly what makes the policy worth optimizing rather than fixing.

**Not backfillable.** Rows written before this exists can never gain the context.

## The one surviving human surface

A command that dumps the RAW draft for a turn, verbatim — **no diff, no
columns**. The user keeps the minimized text in an adjacent tmux pane and
compares by eye. This replaces E-1953's side-by-side renderer entirely.

Shape:

    session turn [<offset>] [-p]

- **0-based, counting back, no negative sign.** `session turn` = `session turn
  0` = most recent. If the latest turn is #503, `session turn 3` is #500.

  Considered and rejected: a negative offset (`session turn -2`). Same mental
  model — "now minus N" — but a leading `-` collides with flag parsing, forcing
  `--` or special-casing, which is boilerplate on every invocation. The
  direction is already implied by "back", so the sign carries nothing.

  **The help text is what makes 0-based read naturally: say "N turns back", not
  "the Nth turn."** Under the second wording 0-based is surprising; under the
  first it is obvious. This was very nearly mis-designed as 1-based on the
  strength of bad phrasing alone.

- `A` / `B` are also valid arguments, selecting a variant of the pending turn
  (see the A/B display note above).
- `-p` paginates, matching `task show -p`.
- Deliberately NOT under `task report`, which is agent-facing.

Two things it needs that are easy to miss:

- **A session selector.** The reviewer is reading a PANE's session, not their
  own. Default to the session bound to the current tmux pane, with an explicit
  `ES-nnnn` override.
- **Real-ledger access from a plain shell.** `task report --raw` does not
  qualify: it resolves the CALLING session and so can never reach another
  session's draft.

## Storage and versioning

- **Corpus stays in the ledger DB.** The concern was duplicated copies of the
  same text, not the table. Versioned history of discussions is wanted. Revisit
  if secrets leaking into a version-controlled ledger becomes the operative
  worry — that, not bloat, would be the reason to move it.
- **No embedded git repo for prompt versioning.** Content-address variants:
  `(hash, text, parent_hash, created_at, note)`. Every eval run must reference
  its variant inside the store regardless, so adding git creates two identities
  for one object plus a sync problem. `parent_hash` gives lineage — the only
  thing git supplied. Diff at read time. Rollback of a bad auto-promotion is a
  pointer move, which is what makes removing the approval step safe.

## Config

- `report_gate` → **`minimizer`**, as an object:
  `{"enabled": true, "optimizer": true}`. Old name is cryptic inside-baseball;
  the object admits the second switch. Needs a fallback read for the old key.
- Set `enabled: true` in this repo as part of this work. The original exemption
  (a session tuning the prompt should not be governed by the prompt it edits)
  weakens once tuning is automated.

## Scheduling

Rides the **existing fire-once job runner**. A dedicated scheduler is a later
question, if ever.

## Corpus state at design time

23 real triples (`raw_draft IS NOT NULL`) of 83 rows; the rest are E-1901-era
relay checkpoints carrying only `sanctioned_text`. Zero labelled — expected,
since the gate is off here. Keep-ratio by draft size: 256-512 → 0.81,
512-1k → 0.67, 1k-2k → 0.62, 2k+ → 0.86. The 2k+ figure is the defect ED-1557
fixes.

## The earliest observable that the loop is learning

This is NOT an experiment to specify and run. The loop is a continuous learning
process, not a numbered series someone designs in advance — nothing here should
be read as "define experiment 1, then experiment 2."

It is simply the first thing that will become visible: keep-ratio by draft-size
bucket, re-measured as the rewriting objective takes hold. The 2k+ bucket
drifting down from 0.86 toward the 0.62 of smaller drafts is the cheapest early
sign the loop is doing something. Watch it; do not steer by it.
