## What happens

`endless task remove <id>` deletes the `tasks` row and leaves every `task_deps`
row that references it — in both directions. The orphan is invisible while the
id stays free, because every consumer reaches deps through a join to `tasks`,
and an id nothing points at joins to nothing. Task ids are reused, so the moment
a later task takes the freed id, it silently inherits the dead relations.

## Observed during E-1911

A follow-up was filed with `--cleans-up E-1911`, then removed a minute later on
the user's instruction. Row 947 (`1914 cleans_up 1911`) survived the removal.
Another session then created a genuine E-1914 for unrelated work, and

    endless task report E-1911

printed "Follow-ups you filed: E-1914". The whole point of that line is that it
is computed rather than typed — an agent cannot dress it up — so it carries more
authority than prose, and it was false. Cleared by hand with

    endless task unlink 1914 --to 1911 --type cleans_up

leaving row 948 (the real `1914 cleans_up 1912`) intact.

## Blast radius

Everything that joins `task_deps` to `tasks`: the successors in `task report`,
`task deps`, `task relations`, blocked-by resolution, and the relation rendering
in `session status`. The failure mode is the bad one — no error, no warning, and
a wrong answer that only appears once an unrelated id is reused, by which point
the removal that caused it is long out of anyone's memory.

## Why no schema fix is available

`task_deps` has no foreign keys at all. `source_id` and `target_id` are bare
INTEGERs discriminated by `source_type` / `target_type`, because a relation
endpoint may be either a task or a decision, and SQLite cannot express an FK
whose target table varies by row. `PRAGMA foreign_keys=ON` is already set in
both `db.py` and `db.go` and buys nothing here. The guard has to be
application-level.

## Which relations a task removal can actually touch

Six of the eight stored types. `reverses` and `modifies` are decision-to-decision
only, and there is no `decision remove` command — decisions are never deleted —
so a task removal can never orphan those.

Of the six, the damage is not uniform:

- `blocks` — a category of its own. Deleting a blocker does not merely lose
  information, it CHANGES WHAT IS TRUE: work that was gated becomes startable
  and `task next` will hand it to someone. Nothing else on the list does that.
- `replaces`, `cleans_up`, `implements`, `documents` — destroy provenance that
  cannot be reconstructed. E-1911 replaced E-1876; delete the replacer and
  E-1876 is obsolete with no surviving explanation. `cleans_up` is the chain
  that actually broke here. `implements` leaves a decision looking unimplemented.
- `relates_to` — symmetric, cheapest to file, means no more than "see also".

The severity spread was considered as grounds for exempting `relates_to` from the
guard and deleting it silently. Rejected: see the plan.
