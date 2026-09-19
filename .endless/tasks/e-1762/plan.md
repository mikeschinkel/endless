# E-1762 — Auto-set a done task to `revisit` when its plan text is edited

## Context
Editing `tasks.text` (the plan) on a task already in a *done* state adds unshipped
scope: the done-status becomes a lie AND the task stays hidden from session monitor.
Today the fix is manual — an agent must remember to run `task update <id> --status
revisit`. Across ~40 projects that depends on every agent/user remembering, which is
the wrong layer. Make the CLI do it: when `task update` *actually changes* a done
task's plan text, auto-transition it to `revisit` and announce why. This is the
behavior the interim memory `feedback_revisit_resurfaces_in_monitor.md` reinforced;
that memory is pruned on completion.

Surfaced by E-1744 (status `assumed`), whose plan gained new scope and had to be
hand-flipped to `revisit`.

## Design decisions (confirmed with Mike)
1. **Trigger statuses** — the completed-successfully set `{assumed, confirmed,
   completed}`. Reuse the existing `_REOPENABLE_TERMINAL_STATUSES` frozenset
   (`task_cmd.py:2947`) — same set, already used by `reopen`. Abandoned states
   (`obsolete`/`declined`) are excluded (editing them is doc cleanup, not revival).
2. **Trigger field** — `tasks.text` ONLY. Editing `--outcome`/`--analysis`/
   `--description` on a done task is normal result-recording and must NOT reopen it.
3. **Only on real change** — diff incoming text vs the existing row value; an
   identical re-write does not flip.
4. **Escape hatch** — `--keep-status` flag suppresses the auto-flip (typo/formatting
   edit). An explicit `--status` in the same update also overrides (intent wins), so
   `--keep-status` only matters when editing text with no `--status`.
5. **Epics excluded** — a `completed` epic's text edit never auto-flips (epics reject
   assumed/confirmed, so `completed` is their only done-state). Prevents a plan tweak
   from silently tripping the E-1542 pause-on-revisit gate for every in-flight
   descendant session. Flip a completed epic by hand if its strategy truly changed.

## Implementation

### `src/endless/task_cmd.py` — `update_plan` (fn at line 3490)
The existing `db.query` at line 3510 already fetches `text`, `status`, and `type`.
Add the auto-flip decision after the row fetch / status-validation block, computed
from values already in hand:

```python
# E-1762: auto-reopen a done task whose plan text is actually edited.
effective_type = task_type if task_type is not None else row[0]["type"]
text_changed = text is not None and text != (row[0]["text"] or "")
auto_revisit = (
    not keep_status
    and status is None                 # explicit --status wins → no auto-flip
    and text_changed
    and effective_type != "epic"       # decision #5: never auto-flip epics
    and row[0]["status"] in _REOPENABLE_TERMINAL_STATUSES
)
```

Apply it in the fields-build section (the `if status is not None: _add("status",
status)` block at line 3585). When no explicit status is given but `auto_revisit` is
true, add the status change so it rides the *same* `task.fields_updated` event and the
same `_emit_field_changes` render:

```python
if status is not None:
    _add("status", status)
elif auto_revisit:
    _add("status", "revisit")
```

**Announcement** — after `_emit_field_changes(...)` (line 3669), when `auto_revisit`
fired, print one explanatory line naming the cause and the escape hatch (the generic
render already shows `Status: <old> -> revisit`; this line says *why*):

```python
if auto_revisit:
    click.echo(
        f"{task_id_display(item_id)} was '{row[0]['status']}'; plan text "
        f"changed → status set to revisit "
        f"(pass --keep-status to suppress for a typo/formatting-only edit)."
    )
```

Add `keep_status: bool = False` to the `update_plan` signature (line 3490).

### `src/endless/cli.py` — `task_update` (options at 1601–1638, fn at 1639)
Add the flag and thread it through the `update_plan(...)` call (line 1649):

```python
@click.option("--keep-status", is_flag=True,
              help="Keep the current status when editing plan text on a done task "
                   "(suppresses the auto-revisit).")
```
Add `keep_status` to the `def task_update(...)` params and pass
`keep_status=keep_status` in the `update_plan(...)` call. `--keep-status` applies to
every id in the `for item_id in item_ids` loop, matching the other flags.

## Deliverable — verify script `tests/tasks/e-1762-verify.sh`
Shape per `tests/tasks/e-1744-verify.sh` (`#!/usr/bin/env bash`, `set -u`, the
`endless() { uv run endless "$@" --db sandbox; }` wrapper, `add_task_get_id`,
`assert_*`, `section`/`summary` harness). Assertions drive the real CLI against the
sandbox DB and check exit code + stdout text. Cases:

1. **Flip + announce** — `assumed` task, edit `--text` to new content → status
   becomes `revisit` and output contains the announcement (`status set to revisit`).
   Verify via `endless task show <id>` reflecting `revisit`.
2. **Identical re-write no-op** — set text, then `--text` the *same* content again →
   status stays put (still the done state; no `revisit`).
3. **`--outcome` on a done task does not flip** — edit `--outcome` only → status
   unchanged.
4. **`--keep-status` suppresses** — done task + `--text <new>` + `--keep-status` →
   status unchanged, no announcement.
5. **Non-done unaffected** — `underway` task + `--text <new>` → stays `underway`.
6. **Explicit `--status` wins** — done task + `--text <new>` + `--status confirmed`
   → ends `confirmed`, not `revisit`.
7. **Epic excluded** — a `completed` epic + `--text <new>` → stays `completed`
   (no auto-flip; decision #5).

Reaching fixtures: create with `endless task add`, drive to `assumed`/`confirmed`
via `task update --status`; for the epic, `--type epic` + a completable-verb title +
`--outcome`, then `--status completed`. Test each shape during implementation and
adjust the exact transition commands to whatever the gates require (fail-fast).

## On completion
- `endless task update E-1762 --status unverified --db main`.
- Prune `feedback_revisit_resurfaces_in_monitor.md` and its `MEMORY.md` line — the
  behavior is now automatic, so the interim reinforcement is obsolete.
- Hand off exactly: `esu && ./tests/tasks/e-1762-verify.sh`.

## Regression
Run the project-wide suite (`just test`, lint, `just build`) and report the result in
one line.
