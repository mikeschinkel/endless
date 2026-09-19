# Plan — E-1915: refuse to remove a task that still has relations

`task remove` orphans `task_deps` rows, and reused ids resurrect them. The fix is
a guard, not a cascade.

## The decision: deny on all six, no exemptions

`task remove` REFUSES while any `task_deps` row references the task, and names
the rows so the operator can clear them. It does not delete relations as a side
effect of deleting a task.

Deny rather than cascade because a severed relation is unrecoverable and a
refusal costs one command. Cascading can be revisited if the friction proves
real; the reverse migration (discovering months later that a chain was silently
severed) has no fix.

All six types, including `relates_to`, with no per-type exemption. `relates_to`
is the weakest relation and was the obvious candidate to auto-delete, but one
rule that always holds beats two rules with a judgment call at the boundary —
and every "harmless" exemption is a second code path that can orphan rows again.
It is one `task unlink` away. If it proves annoying in practice, relax that one
type then, with evidence.

This matches what `task remove` already does with children: it RAISES (not
warns) and tells you to pass `--cascade`. Refusing on relations is the existing
shape, not a new kind of behavior.

## No new flag. `--cascade` below is the one that already exists

`endless task remove` already has `--cascade` ("Also remove all descendants"). It
is about CHILDREN and predates this task. Nothing here adds a relations
equivalent — there is deliberately no way to remove a task and its relations in
one command, because that is the cascade this plan rejects.

It is named below only to settle WHICH tasks get checked:

- `task remove E-100` deletes one task — check E-100's relations.
- `task remove E-100 --cascade` deletes E-100 and every descendant — check all of
  their relations too.

The second case is not a nicety. If only the root were checked, the guard would
be bypassed by removing a parent instead of the child that holds the link, and
the descendants would be deleted with their relations orphaned exactly as today.

## Implementation

`remove_item` in `src/endless/task_cmd.py` (currently ~line 2260).

1. After the existing child-count check, collect the relation rows for every id
   about to be deleted:

   - Without `--cascade`, that is the one task.
   - With `--cascade`, that is the task PLUS its full descendant set. Reuse the
     `WITH RECURSIVE tree(id)` query already in the function (it is computed
     today only to print a count). This is the case most likely to have
     relations, and checking only the root would let a parent removal bypass the
     guard entirely.

   ```sql
   SELECT id, source_id, target_id, dep_type FROM task_deps
    WHERE (source_type = 'task' AND source_id  IN (<ids>))
       OR (target_type = 'task' AND target_id IN (<ids>))
    ORDER BY id
   ```

2. If any row comes back, raise `click.ClickException` listing each relation as
   the exact `task unlink` command that clears it, using the CANONICAL_DEP_TYPES
   display name and the correct source/target order:

   ```
   Task E-1914 has 3 relations. Removing it would orphan them —
   task_deps rows survive a task delete, and ids are reused, so a later
   task inheriting E-1914 would inherit these too. Unlink them first:

       endless task unlink 1914 --to 1911 --type cleans_up
       endless task unlink 1914 --to 1900 --type relates_to
       endless task unlink 1899 --to 1914 --type blocks
   ```

   Under `--cascade`, attribute each row to the descendant it belongs to, or the
   operator cannot tell which of several tasks is holding up the removal.

3. Raise BEFORE `emit_event(kind="task.deleted", ...)`. That event currently
   fires ahead of the delete; a refusal after it would publish a deletion that
   never happened.

## Also fix: the rows already orphaned

The guard stops new orphans; it does not clean up the ones the ledger has
accumulated. Add a repair that reports and deletes rows whose task endpoint no
longer exists:

```sql
SELECT * FROM task_deps
 WHERE (source_type = 'task' AND source_id NOT IN (SELECT id FROM tasks))
    OR (target_type = 'task' AND target_id NOT IN (SELECT id FROM tasks))
```

Fold it into whatever `endless reconcile` already does for consistency repair
rather than adding a new verb. Print what it removed — a silent repair of silent
corruption teaches nothing.

Note the ordering hazard: this query only finds orphans whose id is still FREE.
Any orphan whose id has already been reused is indistinguishable from a genuine
relation and cannot be recovered by query. Run the repair as part of landing
this, and accept that anything already resurrected is lost.

## Audit in the same pass

Every other table keyed on a task id has the same reuse exposure and should be
checked for the same gap — `task_landings` and `session_task_relations` at
minimum. Report what is found; fix in this task only if the fix is small,
otherwise file it.

## Verification — `tests/tasks/e-1915-verify.sh`

- Removing a task with a `blocks` relation is refused, and the message contains
  the exact `task unlink` command that clears it.
- Refused for each of the six types independently, `relates_to` included — the
  no-exemption rule is the decision, so it is the assertion.
- Refused when the task is the TARGET as well as the source (the `1899 blocks
  1914` direction), which is the half a naive `source_id = ?` check misses.
- `--cascade` is refused when a DESCENDANT holds the relation and the root holds
  none, and the message names the descendant.
- After `task unlink`, the same removal succeeds and leaves zero `task_deps`
  rows referencing the id.
- The reconcile repair deletes a hand-seeded orphan row and reports it.
- Regression: removing a task with no relations still works, and removing a task
  with children still raises the existing `--cascade` message.





---

# Second landing — folding in E-1927 and E-1928

Both were found while verifying the first landing and filed as follow-ups. Both
are folded back in here rather than tracked separately: neither needed a design
call once stated, and both reuse helpers the first landing already added.

## E-1927 — the other way a task gets deleted

The first landing put the guard in `remove_item`, so it only covered
`endless task remove`. Two commands delete `tasks` rows without going near it:

- `endless task import <file> --replace` → `_do_import` (`task_cmd.py`)
- `endless task import-json --clear`     → `import_json` (`task_cmd.py`)

Both emit `task.bulk_cleared`, which `execTaskBulkCleared`
(`internal/events/executor.go`) runs as
`DELETE FROM tasks WHERE project_id = ? AND source_file = ?` — no relation
cleanup, no guard. Reproduced before the fix: import a plan, link one of its
tasks, re-import with `--replace` — task gone, `task_deps` row still there.

**Decision: same rule, same refusal.** The reason to hesitate was that refusing
a *bulk file refresh* is more obstructive than refusing one named `task remove`
— the operator may not know which relation the guard means. That is answered by
the message rather than by a weaker rule: it names the file, groups every row
under the task holding it, and gives the exact `unlink`. An imported task that
has since been linked to is not disposable just because a file regenerated it,
and the relation is unrecoverable however the task went away.

No new flag. Re-import without `--replace` / `--clear` if the refresh matters
more than the relation.

Implementation: `_refuse_bulk_clear_with_relations(project_id, source_file)`
resolves the tasks that clear would delete and hands them to the existing
`_relations_referencing()`. Raised before the emit, like the remove-side guard.
The message body is now shared — `_orphan_refusal()` builds it for both call
sites, so the rule reads identically however the caller got here.

## E-1928 — `--cascade` always reported 0 descendants

Pre-existing, not introduced by the first landing; found while verifying the
cascade guard. `remove_item` emitted `task.deleted` and only THEN ran a
recursive query to count the descendants it had just deleted. `emit_event`
executes the delete synchronously, so the CTE always seeded from an empty table:

    $ endless task remove 8600 --cascade      # parent + child + grandchild
    • Removed E-8600 and 0 descendant(s): P

Fix: `_removal_id_set(item_id, cascade)` — added by the first landing for the
guard — already computes exactly that id set, before the delete. `remove_item`
now calls it once, passes it to the guard, and reports `len(removal_ids) - 1`.
The post-delete query is gone; no query was added to replace it.

## Verification — the same `tests/tasks/e-1915-verify.sh`

Folded in as new checks rather than a second script:

- Section E gains an assertion that the cascade success line reports the real
  descendant count (`and 2 descendant(s)`), not 0.
- New section E2: a re-import with `--replace` is refused while an imported
  task holds a relation; the message names the file and the clearing command;
  the task and its relation both survive the refusal; after `unlink` the same
  re-import succeeds and leaves nothing orphaned; a relation on a task the
  import does not own never blocks the refresh.
- `tests/test_task_remove_relations.py` gains unit coverage for
  `_refuse_bulk_clear_with_relations` (per-file scoping, multi-task
  attribution, the `json_import` pseudo-source) and for `_removal_id_set`
  returning the whole subtree.

## Still open — E-1926 only

The FK-free tables (`session_tasks`, `session_hidden_tasks`, `session_notices`,
`project_next_tasks` / `_pending`) stay out. They are not relations, there is no
`unlink` to demand, and their schema comments say outliving the task is the
point — so the guard would be unclearable rather than merely strict. That needs
a decision about what a freed task id means, which is E-1926's job.
