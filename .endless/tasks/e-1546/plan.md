# E-1546 — Update endless guide to surface noun-aliased verbs as AI-facing preferred form

## 1. Scope

Update guide examples and any "common patterns" sections to surface noun-aliased verbs (`endless decision`, `endless epic`) as the preferred AI-facing form, replacing `endless task --type X` patterns where a noun alias exists.

**Out of scope** (filed as sibling task): the broader guide staleness cleanup (references to the non-existent `--decision` flag, stale `--type` value lists). That cleanup lives in a separate sibling task to keep E-1546 focused on noun-aliased verb surfacing only.

## 2. What changes

The bias toward `task --type X` examples in the guide pulls AI sessions to that form even when noun-aliased forms exist. Replace `task --type` examples with noun-aliased forms where a noun alias exists. Keep `task --type X` for cases where no noun alias exists (`task --type research`, `task --type bug`).

Known noun aliases:
- `endless decision` (already exists)
- `endless epic` (lands via E-1540)

Replacements:
- `endless task add --type epic ...` → `endless epic add ...`
- `endless task list --type epic` → `endless epic list`
- `endless task update <id> --type epic ...` → `endless epic update <id> ...`
- `endless task show <id> ... --type epic ...` → `endless epic show <id>`
- Any decision-related noun-form replacements are coupled to the staleness cleanup (sibling task) since the existing `--decision` flag references are dead and need replacement anyway.

## 3. Files to edit

- `docs/guide/index.md`: replace `task add --type epic` examples in "Common patterns" with `endless epic add` form. Mark with "(epic noun-form available when E-1540 lands)".
- `docs/guide/tasks.md`: in the `--type` example block (around line 82), update the comment to show `endless epic add` as the preferred form for epics.
- `docs/guide/decisions.md`: noun-aliased forms here are coupled to the sibling task's `--decision` flag cleanup; minimal change in this task (one cross-reference is enough).

## 4. Patterns intentionally kept

- `--type research` and `--type bug` (no noun alias exists).
- `task link --type <relation>` and `decision link --type <relation>` (relation-type `--type`, not task-type — unrelated concept).

## 5. Implementation sequence

1. Re-verify the available noun-aliased CLI surface by running `endless decision --help` and `endless epic --help` (once E-1540 lands).
2. Edit `docs/guide/index.md` and `docs/guide/tasks.md` per §3.
3. Run `just guide-index` to refresh the auto-generated cross-reference block in `index.md`.
4. Run `just guide-check` to validate.
5. Run `endless guide`, `endless guide tasks` and eyeball output.

## 6. Dependency note

This task's `endless epic` examples require E-1540 to have landed (the alias must exist). If E-1540 hasn't landed when this is picked up, either gate the examples with a "(lands via E-1540)" annotation or sequence the work after E-1540.

## 7. Critical files

- `docs/guide/index.md`
- `docs/guide/tasks.md`
- `docs/guide/decisions.md` (minimal cross-reference only)
