# E-1617: Remove verb-completability gate on epic-type tasks

## Context

The verb-completability gate (`_require_completable_verb_for_completed` in
`src/endless/task_cmd.py`, from E-1240/ED-1502) reserves the `completed` status
for findings-as-deliverable work by requiring the task title's lead verb to be
marked `completable: true` in `verbs.json` (audit, research, review, …). It keeps
implementation tasks on the `verify → confirmed/assumed` track.

Epics (E-1577 / ED-1503) are a different shape: a type=epic task's deliverable
**is** its outcome text (a coordination summary of what shipped in its children),
and the type gate already forces epics to terminate via `completed` (research/epic
reject `assumed`/`confirmed`). But an epic's title typically leads with an
implementation verb like "implement", which the verb gate rejects for `completed`.
The two gates contradict — a concrete deadlock was hit on **E-1564** (type=epic,
lead verb "implement"): the type gate demands `completed`, the verb gate forbids it.

**ED-1511** resolves this: when `tasks.type = 'epic'`, skip the verb gate — the
type gate alone is correct enforcement. Research is unaffected (its natural lead
verbs — research/audit/investigate — are already `completable`), so the decision
is scoped to epic only.

## Change

Make `_require_completable_verb_for_completed` type-aware and skip it for epics,
mirroring the existing `_require_terminal_allowed_for_type` pattern that already
takes a task type.

### 1. `_require_completable_verb_for_completed` — add `task_type` param (≈line 1918)

Add a third parameter and an early return for epics:

```python
def _require_completable_verb_for_completed(
    status: str | None,
    title: str | None,
    task_type: str | None = None,
):
    """E-1240: `completed` is gated to tasks whose title's lead verb is
    marked `completable: true` in verbs.json. … ED-1511: epics are exempt —
    an epic's deliverable IS its outcome text, and the type gate already
    requires `completed`, so the verb gate would only deadlock them."""
    if status != "completed":
        return
    if task_type == "epic":
        return
    ...
```

### 2. `mark_completed_item` call site (≈lines 2133–2144)

The pre-check query selects only `id, title, status` — add the type slug so the
gate can see it, then pass it through:

```python
row = db.query(
    "SELECT id, COALESCE(title, description) as title, status, "
    "       COALESCE((SELECT slug FROM task_types WHERE id = tasks.type_id), '') AS type "
    "FROM tasks WHERE id = ?",
    (item_id,),
)
...
_require_completable_verb_for_completed("completed", row[0]["title"], row[0]["type"])
```

(The `COALESCE((SELECT slug …), '')` subquery is the exact pattern already used in
`update_plan`'s query at line 3100.)

### 3. `update_plan` call site (≈lines 3135–3141)

`effective_type` is already computed here for `_require_terminal_allowed_for_type`,
but **after** the verb-gate call. Move that one line up so both gates share it:

```python
effective_title = title if title is not None else row[0]["title"]
effective_type = task_type if task_type is not None else row[0]["type"]
_require_completable_verb_for_completed(status, effective_title, effective_type)
# E-1577: research/epic tasks reject 'assumed'/'confirmed' terminals.
_require_terminal_allowed_for_type(status, effective_type)
```

## Tests — `tests/test_completed_status.py`

- Extend the `_add_task` helper with a `type_id: int = 1` parameter (epic = 4 per
  `internal/schema/schema.sql:178-182`) so tests can create epics.
- Add cases (the deadlock fix):
  - `mark_completed_item` on an epic with a non-completable verb ("Implement X")
    + outcome → succeeds, status becomes `completed`.
  - `update_plan(status="completed", outcome=…)` on an epic with "Implement X"
    → succeeds.
- Add a regression guard: a **non-epic** (`type=task`) with "Implement X" still
  raises (verb gate intact for implementation tasks) — the existing
  `test_completed_rejects_implementation_verb` already covers this; add one
  asserting the epic/non-epic distinction explicitly so the exemption can't
  silently widen.

## Verify script — `tests/tasks/e-1617-verify.sh`

Per the convention prototyped under E-1596 (`tests/tasks/e-1577-verify.sh`, et al.),
add an executable ad-hoc verification script that exercises the fix **end-to-end
through the CLI** against the worktree's sandbox DB. Reuse that file's harness shape
verbatim — same `endless() { uv run endless "$@" --db sandbox; }` wrapper, the
`add_task_get_id`, `assert_succeeds`, `assert_refused`, `assert_contains` helpers,
the `section`/`report_pass`/`report_fail`/`summary` reporting, `set -u`, color-when-tty,
`cd` to `git rev-parse --show-toplevel`, exit 0 on ALL PASSED / 1 on any failure.

Checks (the deadlock fix + regression guards that the exemption didn't widen):

- **Fix — epic via `task complete`:** create `--type epic` titled "Implement the foo
  subsystem", then `task complete <id> --outcome "shipped via children"` → **succeeds**
  (pre-fix: "requires a completable lead verb").
- **Fix — epic via `task update`:** epic "Implement bar", then
  `task update <id> --status completed --outcome "…"` → **succeeds**.
- **Regression — plain task still gated:** `--type task` titled "Implement baz",
  `task complete <id> --outcome "done"` → **refused**, output contains `completable`.
- **Regression — completable verb on plain task still works:** `--type task` titled
  "Audit qux", `task complete --outcome` → **succeeds** (verb gate intact, not disabled).
- **Other epic gates intact (the exemption is narrow):**
  - epic completed still requires outcome: `task complete <epic>` (no `--outcome`)
    → **refused**, output contains `outcome is required`.
  - epic still rejects `assumed`/`confirmed` (type gate, E-1577):
    `task update <epic> --status confirmed` → **refused**.

The script is committed with the change. It is reference-shape only (E-1596 will
formalize the framework); it must require no external tools beyond `uv` + the sandbox.

## Files

- `src/endless/task_cmd.py` — gate function + two call sites.
- `tests/test_completed_status.py` — helper + new cases.
- `tests/tasks/e-1617-verify.sh` — new executable end-to-end verify script (chmod +x).

No schema change, no verbs.json change.

## Verification

```sh
esu e-1617          # cd worktree + export ENDLESS_SESSION_ID
just build          # stale Go bins reject nothing here, but keep toolchain coherent
just test           # full Python suite; new + existing cases green
```

Targeted run: `just test -- tests/test_completed_status.py` (or pytest filter on
the new epic cases).

End-to-end (the committed verify script, sandbox-routed, run from inside the worktree):

```sh
./tests/tasks/e-1617-verify.sh
# → prints pass/fail per check, then "ALL PASSED" (exit 0)
```

This covers the epic-completes path, the plain-task regression guards, and the
still-intact outcome/terminal gates without touching the real DB.

## Closeout

`endless task update E-1617 --status verify --db main`, hand Mike the how-to-test,
and the return line. Do not land/drop without asking.
