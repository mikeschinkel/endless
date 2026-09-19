# Steer the deprecated `task detail` alias to `task show`, and fix its README refs

## Problem

`task detail` is a **silent live alias** of `task show`
(`cli.py:1244` — `task_cmd.add_command(task_show, name="detail")`). It runs
identically with no signal that `show` is the canonical form. Consequences:

- Nothing corrects a caller at the point of use, so stale instructions pointing
  at `detail` never self-correct through use — they just keep working.
- Stale references persist in `README.md:110` and `README.md:116`, which still
  document `endless task detail <task_id>`.
- The merge of detail→show already happened (E-725, confirmed), yet the alias
  and its docs linger.

## Deliverable

1. When `task detail` is invoked, print a one-line steer to stderr —
   "`task detail` is deprecated; use `task show`" — then run normally. stdout
   stays clean so piping/scripts are unaffected.
2. Update `README.md:110,116` to use `task show`.

## Open decision (resolve during planning)

Per the no-back-compat-aliases preference, the alternative to a deprecation
steer is to **remove the `detail` alias outright**. Weigh the one-line steer
(kept for muscle memory, self-correcting via the warning) against removal
(cleaner, matches no-legacy, but breaks any existing `detail` usage). Owner's
call at plan time.
