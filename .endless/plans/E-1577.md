# E-1577 Plan — Fix endless task lifecycle bugs

## Context

Three related lifecycle bugs surfaced during E-1552:

1. **Research/epic terminal gate missing.** Per E-1537 §3, research and epic tasks have `completed` as their only type-specific terminal (alongside the universal `obsolete`/`declined`). Yet `task assume`, `task confirm`, `task add --status`, and `task update --status` will happily set `assumed` or `confirmed` on a research or epic task. The `--cascade` variants of `task confirm` / `task assume` propagate the illegal terminal to research/epic descendants silently.

2. **Outcome-required check ignores existing DB value.** `task update --status completed` rejects the call when `--outcome` is omitted, even when `tasks.outcome` already holds a non-empty value. This forces a redundant re-pass when the workflow is "author outcome first → flip status later."

3. **`task show --outcome` renders inline.** It shows as a single-line label/value field (next to Title/Project/etc.) rather than as a `— Outcome —` block after the `— Text —` section. The current rendering is inconsistent with Description/Text, and outcome content is frequently multi-line.

**Note on `--cascade` (Mike asked):** `--cascade` on `task confirm` was added by Mike in commit `1b51c61` (2026-04-21, "Add --cascade to task complete") — not unilaterally introduced in an earlier session. It's user-spec.

**Note on E-1537 child overlap:** E-1537 has children E-1541 (epic auto-derivation) and E-1543 (epic-promotion validation on `--type` change). Neither covers the "epic rejects `assumed`/`confirmed`" gate this task needs, so doing it here is the right home.

## Changes

All edits in `src/endless/task_cmd.py` plus tests. No Go changes — the cascade gate runs in Python before the event is emitted.

### Bug 1 — Type-based terminal gate

Add helper near `_require_completable_verb_for_completed` (~line 1742):

```python
_TYPE_REJECTS_ASSUMED_CONFIRMED = ("research", "epic")

def _require_terminal_allowed_for_type(status, task_type):
    """E-1577: research and epic tasks reject 'assumed'/'confirmed'.
    Their only type-specific terminal is 'completed' (per E-1537 §3)."""
    if status in ("assumed", "confirmed") and task_type in _TYPE_REJECTS_ASSUMED_CONFIRMED:
        raise click.ClickException(
            f"Task type {task_type!r} cannot use status {status!r}. "
            f"Use --status completed (or `endless task complete`) with --outcome."
        )
```

Wire it into:

- `complete_item()` (~1779) and `assume_item()` (~1833): widen the SELECT to include `type` via `LEFT JOIN task_types`, call the helper. When `cascade=True`, also walk descendants and refuse loudly if any are research/epic, naming them (matches Mike's "loud failure on invalid state" rule).
- `update_plan()` (~2786): row at 2807 already exposes `type`. Compute `effective_type = task_type if task_type is not None else row[0]["type"]` (parallels the existing `effective_title` pattern at 2837), call the helper.
- `add_item()` (~1538): both `task_type` and `status` are local by 1559; gate right after.

### Bug 2 — Outcome-required uses merged value

In `update_plan()`:

- Delete the standalone call to `_require_outcome_for_completed` at line 2805 (the one that runs before the row fetch).
- After the row fetch (~2807), compute `effective_outcome = outcome if (outcome and outcome.strip()) else row[0]["outcome"]` and call `_require_outcome_for_completed(status, effective_outcome)`.
- Leave `_require_outcome_for_declined` at line 2804 unchanged. A decline's outcome is "the reason for declining"; auto-reusing an existing outcome from a prior status is wrong semantically.
- Helper signatures (`_require_outcome_for_completed`, `_require_outcome_for_declined`) stay param-only. Merging is a caller concern.
- `mark_completed_item` (~1887) is not changed — its CLI flag is `required=True`, so it never sees missing outcome.

### Bug 3 — `— Outcome —` section after `— Text —`

In `detail_item()`:

- Remove the inline render at lines 3080-3081.
- Add a section block after the `— Text —` block at lines 3102-3105, modeled identically:

```python
if item["outcome"] and (show_outcome or item["status"] in ("declined", "completed")):
    click.echo()
    click.echo(click.style("— Outcome —", fg="cyan"))
    click.echo(item["outcome"])
```

Visibility logic (`show_outcome OR status in (declined/completed)`) is preserved verbatim. Only the rendering location and shape change. Existing `--json` and `--llm` output paths are untouched.

## Tests

- `tests/test_research_gate.py` — add coverage for Bug 1 across all four entry points (`assume_item`, `complete_item`, `update_plan`, `add_item`) for both research and epic types. Include a cascade test that creates a research descendant under a non-research parent and asserts the parent's `confirm --cascade` refuses.
- `tests/test_completed_status.py` — add Bug 2 tests: existing outcome in DB satisfies the requirement on `update_plan(status="completed")`; blank existing+blank new still raises; new outcome overrides existing.
- `tests/test_outcome.py` — update `test_task_show_declined_always_shows_outcome` and `test_task_show_outcome_flag_renders_outcome` to assert the `— Outcome —` section header is present and renders after `— Text —`.

## Verification

```sh
cd /Users/mikeschinkel/Projects/endless/.endless/worktrees/e-1577
just build
just test
```

End-to-end smoke from the worktree's sandbox routing:

```sh
# Bug 1: research rejects confirm
./bin-sandbox/endless task add "Research X" --type research --justification "needs cross-system look"
# (note the new E-NNN from the output, substitute below)
./bin-sandbox/endless task confirm E-NNN     # expect clear refusal
./bin-sandbox/endless task assume E-NNN      # expect clear refusal
./bin-sandbox/endless task update E-NNN --status confirmed  # expect clear refusal
./bin-sandbox/endless task complete E-NNN --outcome "findings text"  # expect success

# Bug 2: existing outcome satisfies completed
./bin-sandbox/endless task add "Research Y" --type research --justification "..."
./bin-sandbox/endless task update E-NNN --outcome "draft findings"
./bin-sandbox/endless task update E-NNN --status completed  # expect success (no --outcome re-pass)

# Bug 3: outcome shows as section
./bin-sandbox/endless task show E-NNN --outcome    # expect "— Outcome —" block after Text
```
