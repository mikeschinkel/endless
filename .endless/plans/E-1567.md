# E-1567 — Compute and inject children-state summary into epic handoff at render time

Lands after **E-1566** (epic.md.tmpl exists with the insertion marker).

## Decisions

### Children-state computation
Python helper `_children_state(parent_id: int) -> str` returns the pre-formatted breakdown string directly (template just substitutes):

```sql
SELECT status, count(*) AS n
FROM tasks
WHERE parent_id = ?
GROUP BY status
```

- **Terminal collapse:** `confirmed`, `assumed`, `declined`, `obsolete` all roll into `terminal` (matches the design language "4 terminal").
- **Order in template:** lifecycle progression — `needs_plan` → `ready` → `in_progress` → `verify` → `terminal`. Zero-count keys are omitted in the rendered string.
- **Zero children:** returns `"no children yet"`.

Examples:
- 0 children → `"no children yet"`
- mixed → `"2 needs_plan, 3 ready, 1 in_progress, 4 terminal (10 total)"`
- single bucket → `"3 ready (3 total)"`

### Always compute, pass to all renders
`render_handoff(...)` calls `_children_state(spawned_id)` unconditionally and adds `children_state` to the vars dict for every render — not branched on task type. Go templates silently ignore unreferenced vars; only `epic.md.tmpl` references `{{.children_state}}`, so task/bug/research renders consume the var as a no-op. Keeps `render_handoff` uniform across types and matches Go template idiom.

The DB query is a single `GROUP BY` against `tasks(parent_id)` (already indexed) — cost is negligible per spawn.

### Template change
The marker in the shipped `epic.md.tmpl` at line 18 (`{{/* E-1567: inject children-state breakdown here */}}`) gets replaced with:

```
Children: {{.children_state}}.
```

Plus the "operational mode" guidance block from E-1552 outcome, embedded verbatim under the children line — the coordinator reads the breakdown and self-selects mode. The block lists the six modes (zero / all needs_plan / all ready / all in_progress / all terminal / mixed) with the action each implies. Static text, not parameterized.

The existing `{{- if .child_count}}` block (epic.md.tmpl lines 26–29, which counts total children) becomes redundant once `children_state` is present — drop those lines in the same edit.

## Files

### Changed
- `internal/templatecmd/templates/handoff/epic.md.tmpl` — replace the E-1567 marker with the children-state line + operational-mode block; drop the now-redundant `child_count` block.
- `src/endless/task_cmd.py`:
  - New helper `_children_state(parent_id: int) -> str` — runs the SQL, formats the string per the rules above.
  - `render_handoff(...)` calls `_children_state(spawned_id)` and adds `children_state` to the vars dict for all renders.

### Tests
- `tests/test_handoff_children_state.py` (new):
  - `_children_state` formatting: empty → `"no children yet"`; single-bucket; mixed; terminal-collapse verified across all four terminal statuses.
  - Render epic handoff with each shape, assert breakdown line present + operational-mode block present.
  - Render task/bug/research handoff with children present in the DB → renders successfully, no broken-template error, no children-state text in output (those templates don't reference the var).

## Sequence note

Final feature in the epic before docs (E-1573). Lands after E-1566 (epic.md.tmpl exists with the marker).

## Verification

1. `just test` passes.
2. Spawn an epic with children in various states; the rendered handoff names them: `Children: 2 needs_plan, 3 ready, 1 in_progress, 4 terminal (10 total).`
3. Spawn an epic with no children; rendered handoff says `Children: no children yet.` plus the "drive decomposition" guidance.
4. Spawn a non-epic task that happens to have children (legacy state); rendered handoff is the task/bug/research variant unchanged — children-state is computed but not displayed.
