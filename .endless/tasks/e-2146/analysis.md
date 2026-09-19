## Where this came from

E-2144 corrected the definition of `obsolete` in the transition labels, the
guide's status table, the authority banner and the transition group name. While
verifying it, Mike pointed out that he had just retired an epic whose work had
shipped, and asked whether the gate that normally refuses that rests on invalid
criteria. It does.

## What is there now, verified 2026-09-15

**The gate.** `_refuse_obsolete_on_shipped_work` in `src/endless/task_cmd.py`
(defined ~3394, called from ~5347 on `task update --status` and ~7414 on `task
replace`). It refuses `obsolete` when the task's CURRENT status is in the
`shipped` group — `unverified`, `unreviewed`, `confirmed`, `assumed`,
`completed`. A hard gate, no `--force`.

**The stated premise**, from E-1956 and repeated in `internal/taskstatus/
transitions.go` and `docs/guide/tasks.md`: "`obsolete` reads as *never
happened*, which is simply false of work that ran."

That reading came from the gloss E-2144 deleted ("made irrelevant by other
changes — it never needed doing"). Under the corrected definition — Cambridge:
"no longer in use, out of date, or replaced by something newer and better" —
deleted code is the paradigm case of obsolete, not the excluded one. The premise
does not survive its own correction.

**The asymmetry.** The gate is Python-only, on the `task update` path. Epic
status is derived in Go, inside the executor transaction
(`internal/events/epic_derivation.go`), and never passes through it. So
`obsolete`-implies-never-shipped is not an invariant the status carries — it is
a property of six edges that epics bypass entirely. E-1421 is an epic that
landed twice (18fe0f0, 2026-06-01) and is `obsolete` right now.

**The other half of the table.** `declined` HAS inbound edges from all five
shipped statuses, labelled "declines — the shipped work is not being kept". So
the lifecycle already admits that shipped work can be abandoned; it just routes
it through the status that means an active decision not to do the work.

## The axis Mike named

- `declined` — an active decision not to DO something. Natural home: work that
  was never done.
- `obsolete` — no longer in use, out of date, superseded. Applies to BOTH work
  that shipped and work that never did.

That inverts which of the two is shipped-capable. It is a bigger change than
deleting the gate, and more coherent.

## Why the current workaround is not survivable

Closing E-1434/E-1437 as `declined` writes a false record: it asserts nobody
decided to do work that was designed, built, landed and used. Mike, 2026-09-15:
"declined as a workaround is actively causing the reason to be misconstrued."
This is the bug continuing under another name, not a mitigation.

## The concrete case that forces it

E-2142 removes the curated 'next' list, `task import`, and session task
ordering. Its code shipped:

- E-1434 `assumed`, landed 2026-06-10 (e10c4c9) — the five `project_next` tables
  and migration.
- E-1437 `assumed`, landed 2026-06-11 (dd2f933) — the urgent auto-add hook
  subscribers.
- Parent epic E-1421 `obsolete`, landed 2026-06-01 (18fe0f0).

Nothing replaces them. `task replace <old> --by <new>` has no `<new>` to name.

## What has to end up agreeing

Decide the axis first; the rest follows from it. In scope to reconcile:

- `internal/taskstatus/transitions.go` — the edge table: which statuses reach
  `obsolete`, which reach `declined`, and the labels on both. Then `just
  lifecycle-index` (regenerates `docs/status-lifecycle.mmd`, `README.md`,
  `docs/guide/index.md`).
- `src/endless/task_cmd.py` — `_refuse_obsolete_on_shipped_work` and its two
  call sites; whatever replaces it must be reachable from the Go path too, or
  be stated as deliberately Python-only.
- `internal/events/epic_derivation.go` — the derivation must not be able to
  write a status the gate would refuse, or the rule is not a rule.
- `src/endless/authority.py` — `for_task`. Both banners. `declined` currently
  says "an active decision not to do the work"; if `declined` stops being
  shipped-capable that stays true, and if it does not, it needs the same
  treatment `obsolete`'s just got.
- `docs/guide/tasks.md` — the section "Superseded work is `replaced_by`, never
  `obsolete`" (~419) and the duplicates passage (~826-841).
- `docs/guide/index.md` — the `obsolete` and `declined` rows of the status
  table.

## Open question for the plan

Does `replaced_by` remain the preferred record when something DID supersede the
work, with `obsolete` additionally allowed — or does supersession stop being a
status question entirely? The two are not exclusive; E-1956's instinct that the
supersession fact must not be lost is still right, and `task_landings` already
preserves the fact that it shipped independently of status.
