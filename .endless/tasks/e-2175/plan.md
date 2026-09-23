# Obsoleting a task must say why, as declining already does

## The evidence: a natural experiment already ran

Same codebase, same users, two abandonment statuses — one guarded, one not.
Measured 2026-09-23 against the live ledger:

| status | total | no reason | span of the reasonless rows |
|---|---|---|---|
| `declined` (guarded) | 74 | 32 | 2026-06-10 → **2026-06-10** |
| `obsolete` (unguarded) | 223 | 74 | 2026-06-10 → **2026-09-23** |

Every reasonless `declined` row is from a single day, the seed import. Since
`_require_outcome_for_declined` (ED-1022) landed, not one decline has gone in
without a reason — three and a half months, zero leaks, across three call
sites.

`obsolete` is still producing reasonless rows today. The most recent was
created during the session that filed this task.

This is as close to a controlled comparison as a codebase offers: the guard is
the only difference between the two populations, and the guard is the whole
difference in outcome.

## The product already decided this for decisions

`decision obsolete` requires `--reason` (E-1920), stored in a dedicated
`obsolete_reason` column. Its docstring states the rationale, and it transfers
to tasks without modification:

> `--reason` is required for the same reason `reject --reason` is: this is the
> only field that distinguishes a rule deliberately retired from one that
> quietly stopped being mentioned, and it is what a reader hitting the decision
> later needs in order to stop re-litigating it.

Tasks got neither the column nor the requirement.

## The asymmetry, stated plainly

A task can be abandoned three ways. Two are required to record something:

- `declined` — refused without a reason (ED-1022).
- `superseded` — refused without a `replaced_by` relation, because the status
  names a successor and needs one to name.
- `obsolete` — requires nothing.

`obsolete` is both the least constrained and, at 223 rows, the most used.

## What to change

**Store the reason in `outcome`. No schema change.** Decisions needed a new
column only because `rejection_reason` already existed and answered a different
question ("why we said no" vs "what went away"). Tasks already funnel
`decline`'s reason into `outcome`; `obsolete` follows the same path.

**Generalize the existing guard rather than adding a second one.**
`_require_outcome_for_declined` (src/endless/task_cmd.py) is called from three
sites — `decline_item`, `update_item`, and `replace_task`. Widening that one
function to cover `obsolete` as well as `declined` extends the requirement to
every path at once, including `task replace`, whose replaced task defaults to
`obsolete`. Rename it to match what it then means.

That coverage is the reason `declined` has no leaks, and it is the property to
preserve: the requirement belongs to the STATUS TRANSITION, not to a verb. A
second guard bolted to one command would leak the moment someone reaches
`obsolete` by another route.

**`task replace` is included, deliberately.** It already accepts `--outcome`
("why this was replaced") but only enforces it for `--status=declined`. The
`replaced_by` relation records WHAT replaced the task; it does not record WHY,
and those are different facts a later reader needs both of. Decided by Mike,
2026-09-23, overruling the filing session's proposed exemption.

The one case that must stay exempt is structural rather than a carve-out:
`task replace` on work that ALREADY SHIPPED keeps the terminal it earned
(`confirmed`/`assumed`) and never reaches `obsolete`, so the guard does not
fire. That falls out of guarding the transition and needs no special case.

**Do not backfill the 74 existing rows.** Inventing reasons nobody remembers
would produce authoritative-looking fiction. A clean cutoff date is exactly how
`declined` arrived at its current state, and the seed-import rows there are
still reasonless and still fine.

## The risk, and why it is acceptable

Obsoleting is bulk backlog grooming — 223 rows against `declined`'s 74 — so a
mandatory field is felt more here than anywhere else, and mandatory fields are
where junk values ("stale", "n/a") get typed. A field that looks like an answer
but is not is worse than an empty one.

Two things blunt it. 149 of 223 obsoleted tasks (67%) already carry a reason
with nothing requiring it, so this formalizes what people do anyway rather than
imposing a new tax. And the reasonless third is precisely the population a
future reader cannot interpret — the rows where the requirement pays.

Accepted cost: a bulk grooming pass now types one reason per task. If that
proves painful in practice, the answer is a batch verb that takes one reason
for many tasks — not a weaker guard.

## Verification

- `task update <id> --status obsolete` with no outcome is refused, naming the
  flag that satisfies it.
- The same call WITH an outcome succeeds and stores it.
- `task replace <old> --by <new>` with no outcome is refused when the replaced
  task lands on `obsolete`.
- `task replace` on already-shipped work still succeeds with no outcome: it
  keeps its earned terminal and never reaches `obsolete`.
- `task decline` is unchanged — still refused without a reason.
- Reaching `obsolete` by every other code path that sets status is refused too;
  enumerate the call sites and assert each, since single-site enforcement is
  the failure this plan exists to avoid.
- Existing reasonless `obsolete` rows still read, render and query normally —
  the guard is on the transition, not on the row.
- `just test`, `just test-go`.


## Folded in: `task replace --status`'s help named the wrong default

Found while widening the guard, and fixed here rather than filed: the
`--status` flag on `task replace` still advertised `obsolete` as the default
status for the replaced task, and the command's own docstring repeated it. The
default became `superseded` when the two were separated — `obsolete` means
"nothing replaced it", which is the one thing `task replace` cannot be saying.

It is folded in because this change is what sends a reader to that flag: the
new requirement makes `--status obsolete` the case that now costs something, so
a help string telling them it is the default was the next question after the
refusal. One line of help text and one of a docstring, in a file the diff
already touches.
