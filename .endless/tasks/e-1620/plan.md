# E-1620: Render hierarchical task context in bg agent labels for Agent View

## Context

When `endless task spawn --bg` dispatches a background Claude agent, it labels it
two ways: the `--name` arg to `claude --bg` and the opening identity line of the
rendered handoff (which Claude's own title-inference may pick up). Both currently
encode only the bare task id (`E-<id>`), with no parent/epic context. In the
global Agent View, a child of an epic is therefore indistinguishable from a
standalone task, and there is no native epic-scoped filtering to compensate.

This change makes every bg-agent label self-identify by hierarchy:

- root task (no parent — standalone or epic) → `E-<id>: <title>`
- any task with a parent (child of an epic OR of any other task) → `E-<parent>/E-<id>: <title>`

The single distinguishing rule is **parent_id present → prefix with
`E-<parent>/`**, regardless of the parent's type. The spec's "standalone vs epic
vs child of an epic" framing collapses to this: roots (standalone and epic) share
one shape; anything with a parent gets the parent-prefixed shape. Decisions
confirmed with Mike:
the text after the prefix is the task **title** (not `description`), and both id
segments carry the `E-` prefix (`E-1564/E-1620`).

## Reproduction (current behavior)

E-1620 is a child of epic E-1564. Today:
- `--name` is built as `f"E-{item_id}"` → `E-1620` (`src/endless/task_cmd.py:4172`).
- Handoff line 3 renders `- E-1620: <title>.` (`internal/templatecmd/templates/handoff/*.md.tmpl:3`).

Neither shows the parent epic. After the change the same dispatch yields
`E-1564/E-1620: <title>`.

## Implementation

### 1. New helper — `src/endless/task_cmd.py`
Add a pure function near `task_id_display` (line 385):
```python
def _hierarchical_label_prefix(item_id: int, parent_id: int | None) -> str:
    """E-<parent>/E-<id> when the task has a parent, else E-<id> (E-1620)."""
    if parent_id:
        return f"{task_id_display(parent_id)}/{task_id_display(item_id)}"
    return task_id_display(item_id)
```

### 2. Thread `parent_id` into the spawn path — `src/endless/task_cmd.py`
- The `spawn_plan` task query (line ~3811) already selects the task row in
  Python; add `p.parent_id` to its SELECT list (one extra column on an existing
  read — no new `db.query` site, consistent with the function's current pattern).
- Pass `item["parent_id"]` to `_spawn_bg_dispatch` (new param).

### 3. `_spawn_bg_dispatch` — `src/endless/task_cmd.py` (~4137)
- Accept `parent_id: int | None`.
- Compute `prefix = _hierarchical_label_prefix(item_id, parent_id)` once.
- `--name`: change `f"E-{item_id}"` → `f"{prefix}: {title}"` (full label, matching
  the spec shape; line 4172).
- Pass `parent_id` through to `render_handoff` so the handoff opening line matches.
- Update the "Backgrounded …" echo (line ~4209) to use `prefix` instead of
  `task_id_display(item_id)` for consistency.

### 4. `render_handoff` — `src/endless/task_cmd.py` (~3610)
- Add `parent_id: int | None = None` param.
- Build `label_prefix = _hierarchical_label_prefix(spawned_id, parent_id)` and add
  `"label_prefix": label_prefix` to `vars_payload`. (Falls back to `E-<id>` when
  `parent_id` is None, so existing non-bg callers keep working unchanged.)
- Update `show_handoff` (~3673): add `t.parent_id` to its SELECT and pass it
  through, so `endless task show --handoff` previews match dispatch output.

### 5. Handoff templates — `internal/templatecmd/templates/handoff/{task,bug,research,epic}.md.tmpl`
- Line 3 only: change `- E-{{.spawned_id}}: {{.title}}.` →
  `- {{.label_prefix}}: {{.title}}.`
- Leave every other `E-{{.spawned_id}}` reference intact (e.g. the
  `endless task show E-{{.spawned_id}} --text` lines) — those want the bare id.

### 6. Tests
- **Python** `tests/test_spawn_bg.py`: assert the dispatched `--name` carries the
  hierarchical label. Add a case for a parented task (`parent_id` set →
  `E-<parent>/E-<id>: <title>`) and a root task (`E-<id>: <title>`). The fixtures
  mock `claude`/`endless-go`; assert on the argv passed to `subprocess.run`.
- **Python** `tests/test_handoff.py`: add a case rendering with `parent_id` and
  assert the opening line contains `E-<parent>/E-<id>`; confirm the no-parent path
  still renders `E-<id>` (existing assertions should still hold).
- **Go** `internal/templatecmd/template_test.go`: add `"label_prefix"` to the
  payloads that render handoff templates (otherwise line 3 renders `<no value>`,
  since the renderer uses no `missingkey=error`). Add/adjust an assertion for the
  prefixed opening line.

## Out of scope
- Foreground tmux window naming (`_spawn_window_name`) — Agent View is the bg
  surface; not touched.
- Any Go-side port of the spawn/handoff DB reads (E-1486) — the path is already
  Python+`db.query`; this change only extends two existing SELECTs by one column.

## Verification
1. `just build` in the worktree (templates are embedded; rebuild `endless-go`).
2. `just test` — Python suite green, including the new spawn/handoff cases.
3. `go test ./internal/templatecmd/...` — Go template test green.
4. Manual handoff render for a child of an epic:
   ```sh
   endless task show E-1620 --handoff --db main   # opening line: E-1564/E-1620: ...
   ```
   and for a root/epic task confirm it stays `E-<id>: ...`.
5. Confirm dispatch label without launching a real agent: the new
   `test_spawn_bg.py` assertions verify the `--name` argv directly.
