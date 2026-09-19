# E-1616: Reject maybe-phase parent-child relations at the write path

## Context

Decision **ED-1510** ("Maybe-phase tasks must be parentless") establishes the invariant:
a task may **not** simultaneously have `phase = maybe` **and** `parent_id IS NOT NULL`.

Rationale: `maybe` = uncommitted; parent-child = scope binding. A parent with maybe-phase
children has *phantom scope* — it appears done while uncommitted commitments linger inside,
and it breaks epic auto-derivation (E-1541), which can't safely treat maybe children as part
of the parent's outcome. The `relates_to` link type preserves conceptual adjacency without
violating the invariant.

This task enforces that invariant as a **hard gate** (no `--force` escape) on every write
path that can produce the forbidden state.

## The single invariant

All described operations reduce to one rule, evaluated against the **resulting** row state:

> Reject when effective `phase == "maybe"` AND effective `parent_id` is not NULL.

This naturally covers the three operations in the task description and keeps the legal cases legal:

| Operation | effective phase | effective parent | Result |
|---|---|---|---|
| `task add --parent X --phase maybe` | maybe | X | **reject** |
| `task update <maybe-task> --parent X` | maybe (from DB) | X | **reject** |
| `task update <child> --phase maybe` (child has parent) | maybe | parent (from DB) | **reject** |
| `task move <maybe-task> --parent X` | maybe (from DB) | X | **reject** |
| `task update X --phase next --parent Y` (atomic promote+parent) | next | Y | legal ✓ |
| `task add --phase maybe` (no parent) | maybe | NULL | legal ✓ |
| `task add --parent X` (default phase `now`) | now | X | legal ✓ |

"Effective" = the value supplied in this operation if present, else the task's current DB value.
This mirrors how existing sibling validators already compute `effective_title` / `effective_type`
/ `effective_parent` in `update_plan` (`src/endless/task_cmd.py:3146-3219`).

## Decisions (confirmed with Mike)

1. **Cover `task move` too.** The description names only add/update, but `task move <id> --parent X`
   (emits `task.moved`) is a third reparenting path. Covering it closes the hole so this is a real
   gate, not bypassable via `move`.
2. **Enforce in BOTH Python and the Go executor** (defense-in-depth). Python gives a clean,
   immediate `click.ClickException`; the Go executor is the single-writer boundary that any event
   emitter (including direct `endless-go event emit`) must pass through, making it a true gate.
3. **No committed survey command.** It was a one-shot cleanup aid; running the query once is enough.
   The existing violations are already surveyed (below) for Mike to clean up manually.

## Existing violations (surveyed against `--db main`, read-only)

6 tasks currently violate the invariant (all `needs_plan`). Pre-existing data is **not** mutated
by this change — the gate only blocks *new* writes. Mike converts each to `relates_to` or makes it
standalone at his discretion:

- E-1123 (parent E-971) — Define branch-collision strategy for short-id session worktrees
- E-1183 (parent E-1182) — Add fish shell support to endless-sandbox enter shell-init
- E-1260 (parent E-1236) — Show project task on status row when cwd matches Endless project
- E-1262 (parent E-1031) — Add retention for session-to-task association after task confirm
- E-1321 (parent E-968) — Implement Stop hook to enforce session-status reflex
- E-1383 (parent E-1511) — Design team-decisioning state model for decisions table

## Implementation

### 1. Python gate — `src/endless/task_cmd.py`

Add one shared helper (placed near the other validators, e.g. after `_research_gate_check`):

```python
def _reject_maybe_with_parent(phase: str | None, parent_id: int | None) -> None:
    """ED-1510: a maybe-phase task cannot have a parent."""
    if phase == "maybe" and parent_id is not None:
        raise click.ClickException(
            "A maybe-phase task cannot have a parent. "
            "maybe = uncommitted; parent-child = scope binding, and mixing the "
            "two creates phantom scope. Promote it (--phase now/next/later) to "
            "place it under a parent, or link it with a relates_to relation instead."
        )
```
(No internal E-NNN IDs in the user-facing message, per house rules.)

Call it from the three write functions:

- **`add_item`** (`task_cmd.py:1716`): after `validate_description(...)`, call
  `_reject_maybe_with_parent(phase, parent_id)`. Both values are already in scope; no DB read.
- **`update_plan`** (`task_cmd.py:3089`): the current row is already fetched (`row`, line 3109).
  Compute effective values exactly like the research-gate block (3214-3218) and check:
  ```python
  effective_phase = phase if phase is not None else row[0]["phase"]
  if parent_id is not None:
      effective_parent = None if parent_id == PARENT_NONE else parent_id
  else:
      effective_parent = row[0]["parent_id"]
  _reject_maybe_with_parent(effective_phase, effective_parent)
  ```
  Place it after status validation, before the `fields`/`emit_event` block. No new DB read.
- **`move_task`** (`task_cmd.py:~4454`): the single-task path already fetches the row
  (`SELECT id, parent_id ...`, line 4458). Add `phase` to that SELECT, then before
  `emit_event(kind="task.moved", ...)` call
  `_reject_maybe_with_parent(row[0]["phase"], target_parent_id)`.
  (The `--children-of` bulk path moves already-parented children, so it cannot create a *new*
  maybe+parent violation — leave it untouched.)

### 2. Go executor gate — `internal/events/`

Add a shared helper alongside `ValidatePhase` in `internal/events/phase.go`:

```go
// ValidateMaybeParentless enforces ED-1510: a maybe-phase task may not have a parent.
func ValidateMaybeParentless(phase string, parentID *int64) error {
    if phase == "maybe" && parentID != nil {
        return fmt.Errorf("a maybe-phase task cannot have a parent " +
            "(maybe = uncommitted, parent-child = scope binding); promote it " +
            "or use a relates_to relation instead")
    }
    return nil
}
```

Wire it into the three handlers in `internal/events/executor.go`:

- **`execTaskCreated`** (line 240): after `ValidatePhase(p.Phase)`, call
  `ValidateMaybeParentless(p.Phase, p.ParentID)` — both fields are in the payload.
- **`execTaskFieldsUpdated`** (line 412): when `"phase"` or `"parent_id"` appears in `p.Fields`,
  read the current row for whichever field is absent, compute effective values, and validate.
  Skip entirely when neither field is in the update (don't block unrelated edits, e.g. a title
  change on a task that already violates). The handler already does conditional `db.QueryRow`
  reads (lines 471-473), so this fits the existing shape:
  ```go
  _, phaseSet := p.Fields["phase"]
  _, parentSet := p.Fields["parent_id"]
  if phaseSet || parentSet {
      var curPhase string
      var curParent sql.NullInt64
      _ = db.QueryRow("SELECT phase, parent_id FROM tasks WHERE id = ?", taskID).
          Scan(&curPhase, &curParent)
      effPhase := curPhase
      if v, ok := p.Fields["phase"].(string); ok { effPhase = v }
      var effParent *int64
      if curParent.Valid { effParent = &curParent.Int64 }
      if parentSet {
          effParent = nil
          // parent_id arrives as JSON number or null; nil/0 → root
          switch v := p.Fields["parent_id"].(type) {
          case float64: if v > 0 { id := int64(v); effParent = &id }
          case nil:     effParent = nil
          }
      }
      if err := ValidateMaybeParentless(effPhase, effParent); err != nil { return nil, err }
  }
  ```
  (Confirm the JSON type `parent_id` deserializes to and that `null`/`0` mean root, matching the
  Python `PARENT_NONE` convention, during implementation.)
- **`execTaskMoved`** (line 528): when `p.NewParentID != nil`, read the moving task's current
  `phase`; if `maybe`, reject. Place beside the existing circular-reference guard (lines 536-552).

### 3. Tests

- **Go** — add to `internal/events/executor_test.go` (or a new `maybe_parent_test.go`): created /
  fields_updated / moved each rejecting the violating combo, plus the legal atomic
  `phase=next + parent=Y` combo and unrelated-field edits passing. Model on existing executor tests.
- **Python** — new `tests/test_maybe_parent_gate.py`, modeled on `tests/test_research_gate.py`:
  `task add --parent X --phase maybe` fails; `task update <maybe> --parent X` fails;
  `task update <child> --phase maybe` fails; `task move <maybe> --parent X` fails; atomic
  promote+parent succeeds; no-parent maybe and default-phase parented add both succeed.

### 4. Per-task verify script (E-1596 convention)

Create `tests/tasks/e-1616-verify.sh`, modeled directly on the established
`tests/tasks/e-1577-verify.sh` prototype (copy its header, color/`section`/`report_pass`/
`report_fail`/`summary` scaffolding, the sandbox-routed `endless()` wrapper, `add_task_get_id`,
and the `assert_refused` / `assert_succeeds` helpers — all already present there). The gate maps
cleanly onto `assert_refused` (CLI exits non-zero AND output contains the expected text).

The script exercises the gate end-to-end against the **worktree sandbox DB** (`--db sandbox`),
prints pass/fail per check plus a summary, and exits 0 on all-passed / 1 on any failure. Checks:

- `assert_refused` — `task add --parent <P> --phase maybe`
- `assert_refused` — `task update <maybe-task> --parent <P>`  (create a parentless maybe first)
- `assert_refused` — `task update <child> --phase maybe`       (create a parented task first)
- `assert_refused` — `task move <maybe-task> --parent <P>`
- `assert_succeeds` — `task update <child> --phase next --parent <P>` (atomic promote+parent)
- `assert_succeeds` — `task add "..." --phase maybe`            (no parent)
- `assert_succeeds` — `task add "..." --parent <P>`             (default phase `now`)

`<P>` is a throwaway parent task created at the top of the run. Match the reference's `main()`
footer (git-root cd, `uv`/`command -v` guard, banner, run check groups, `summary`). Make it
executable (`chmod +x`).

This is an ad-hoc per-task verify script in the spirit of E-1596 (which is formalizing the
convention in a parallel session) — reference-shaped, not a claim on that epic's deliverables.
It doubles as the how-to-test handoff: Mike (or the spawning session) runs one command.

## Build & verify

```sh
esu                        # cd worktree + export ENDLESS_SESSION_ID
just build                 # templ/tailwind/go — rebuild bin/* so Go gate is live
just test                  # Python suite, incl. new gate tests
go test ./internal/events/...   # Go executor gate tests
./tests/tasks/e-1616-verify.sh  # per-task verify script — expect ALL PASSED, exit 0
```

The `e-1616-verify.sh` script IS the manual smoke test (sandbox-routed, self-contained). It
replaces hand-typed CLI checks: one command, pass/fail per case, `ALL PASSED` on success.

## Handoff

When green: `endless task update E-1616 --status verify --db main`, report how-to-test, and
return to the spawning session. Do **not** land/drop without asking.

## Critical files

- `src/endless/task_cmd.py` — `add_item` (1716), `update_plan` (3089), `move_task` (~4454) + new helper
- `internal/events/phase.go` — new `ValidateMaybeParentless` helper
- `internal/events/executor.go` — `execTaskCreated` (240), `execTaskFieldsUpdated` (412), `execTaskMoved` (528)
- `internal/events/executor_test.go`, `tests/test_maybe_parent_gate.py` — tests
- `tests/tasks/e-1616-verify.sh` — new per-task verify script (model: `tests/tasks/e-1577-verify.sh`)
