E-1915 put the relation guard in `remove_item` (`src/endless/task_cmd.py`), so
it only covers `endless task remove`. Two other commands delete `tasks` rows and
neither passes through it:

- `endless task import <file> --replace` → `_do_import` (`task_cmd.py` ~line 852)
- `endless task import-json --clear`     → `import_json` (`task_cmd.py` ~line 2240)

Both emit `task.bulk_cleared`, which `execTaskBulkCleared`
(`internal/events/executor.go` ~line 831) executes as
`DELETE FROM tasks WHERE project_id = ? AND source_file = ?` — no relation
cleanup, no guard.

Reproduced in an isolated DB:

    endless task import PLAN.md          # creates task 1 from PLAN.md
    endless task link 1 --to E-8500 --type blocks
    endless task import PLAN.md --replace

    tasks WHERE id=1     -> 0     (deleted)
    task_deps            -> 1     (orphaned, still says "1 blocks 8500")

Same failure mode as the original bug: invisible until id 1 is reused, then
wrong with the authority of a computed fact.

## The open question

Whether E-1915's answer — REFUSE and make the operator unlink — is right here is
not obvious, which is why this is filed rather than folded in. `task remove` is
a deliberate act on one named task, so a refusal costs one command. A re-import
is a bulk refresh over a whole file; refusing it because one imported task
picked up a relation could deadlock the workflow, and the operator may not even
know which relation the guard means.

Options, not a decision:

- Same guard, same refusal. Consistent, possibly obstructive.
- Refuse but list, with an explicit opt-out flag on the import.
- Delete the relations along with the tasks. Rejected for `task remove` on the
  grounds that a severed relation is unrecoverable — but an imported task's
  relations may be judged cheaper.
- Leave it to the repair. E-1915's `reconcile` repair already sweeps these
  orphans up on the next `project list` / `project scan`, and inherits its
  ordering hazard: it only helps while the freed id is still free.

Note the last option means this is *partially* mitigated today — the window is
"between the re-import and the next reconcile", not forever.
