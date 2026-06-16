# E-1566 — Add per-type handoff variants for task/bug/research/epic

Lands after **E-1565** (Go template renderer). Splits the single `templates/handoff.md.tmpl` into four per-type templates and teaches Python to select by `tasks.type_id` slug.

## Decisions

- **Directory layout.** `templates/handoff/{task,bug,research,epic}.md.tmpl` — one per slug. Old `templates/handoff.md.tmpl` deleted (replaced by `templates/handoff/task.md.tmpl` which inherits today's content).
- **Renderer takes path-style name.** `endless-go template render handoff/<type>`. The embedded FS preserves the directory; the verb's argument is the path under `templates/` without `.tmpl`. Same renderer code as E-1565 — only the embed pattern broadens (`templates/**/*.tmpl`).
- **Type resolution in Python, not Go.** `render_handoff(..., task_type: str | None)` joins `tasks.type_id` → `task_types.slug` at call sites. Unknown/null → `"task"` fallback (matches design "Unknown/null type falls back to task"). Renderer in Go stays type-agnostic.
- **Child-count is universal.** Per E-1552 outcome ("All handoffs include the child count and recommend `--children`"), `render_handoff` runs `SELECT count(*) FROM tasks WHERE parent_id = ?` and injects `{{.child_count}}`. Templates render a conditional line: `{{if gt .child_count 0}}This task has {{.child_count}} children — read them with `endless task show E-{{.spawned_id}} --children`.{{end}}`. Zero children = line absent.
- **Children-state breakdown deferred.** That richer payload comes in **E-1567**. This child ships `epic.md.tmpl` with a TODO marker comment (`{{/* E-1567: inject children-state breakdown here */}}`) so the renderer doesn't error and the implementer of E-1567 has a clear insertion point.
- **Per-variant framing.** Each template starts from `handoff/task.md.tmpl` (the verbatim today-content) and adds 1–3 lines of type-specific framing near the top:
  - **task** — verbatim, no addition. End-state: `verify`. (Today's content.)
  - **bug** — adds: *"Reproduce the bug first before changing anything. Link the failing test or the exact reproduction steps in your first reply."* End-state: `verify`.
  - **research** — adds: *"Findings are the deliverable. Write them into `tasks.outcome` via `endless task update E-X --outcome <file>`. End state is `completed` (research has its own gate, ED-1502), not `verify`."* Adjusts step 6 to instruct `--status completed --outcome <file>` instead of `--status verify`.
  - **epic** — adds: *"You're the coordinator on this epic. Don't implement directly — draft plans, dispatch children, verify their work."* Adjusts step 3 to point at children (`endless task show E-X --children`) and replaces step 6 with epic-completion guidance (`endless task update E-X --status completed` once all children are `confirmed`/`assumed`). Plus the E-1567 insertion marker.

## Files

### New
- `templates/handoff/task.md.tmpl` — today's content, with the universal `child_count` conditional inserted near the bottom of the boilerplate-rules block.
- `templates/handoff/bug.md.tmpl` — task variant + bug-framing line.
- `templates/handoff/research.md.tmpl` — task variant with research-framing block and step-6 substitution.
- `templates/handoff/epic.md.tmpl` — task variant with coordinator-framing block, step-3 + step-6 substitutions, and the E-1567 insertion marker.

### Changed
- `internal/templatecmd/template.go` — broaden embed to `templates/**/*.tmpl`; verify `render` accepts `handoff/task` etc. (no other code change — Go embed FS handles paths natively).
- `src/endless/task_cmd.py`:
  - `render_handoff(...)` gains `task_type: str | None` keyword arg. Adds `child_count` lookup. Builds template name as `f"handoff/{task_type or 'task'}"`.
  - `show_handoff` (the `endless task handoff` verb): join `task_types` in the SELECT, pass slug.
  - `spawn_plan`: same — already loads the task row, extend SELECT to include `t.slug`, pass to `render_handoff`.

### Deleted
- `templates/handoff.md.tmpl` — replaced by `templates/handoff/task.md.tmpl`.

## Tests

- `tests/test_handoff.py`:
  - Existing `test_render_handoff_includes_task_and_return_path` updated to pass `task_type="task"` and continues to assert task-flow content.
  - Existing `test_render_handoff_degrades_without_runtime_context` updated to pass `task_type=None` and continues to assert visible placeholders.
  - **New** `test_render_handoff_bug_variant`: asserts "Reproduce the bug first" present.
  - **New** `test_render_handoff_research_variant`: asserts "Findings are the deliverable" and `--status completed --outcome` present, `--status verify` absent.
  - **New** `test_render_handoff_epic_variant`: asserts "coordinator" framing present, references to drafting plans / dispatching children present.
  - **New** `test_render_handoff_unknown_type_falls_back_to_task`: `task_type="bogus"` resolves to task content.
  - **New** `test_render_handoff_includes_child_count_when_nonzero`: parametrized — 0 children = no child-count line, 3 children = "3 children" line.

## Sequence note

Depends on **E-1565**. Required by **E-1567** (children-state breakdown into epic.md.tmpl).

## Verification

1. `just build` and `just test` pass.
2. Manual: spawn a `bug`-type task, confirm "Reproduce the bug first" in the pasted prompt.
3. Manual: spawn a `research`-type task, confirm end-state guidance points at `completed --outcome`.
4. Manual: spawn an `epic`-type task with 3 children, confirm coordinator framing + child-count line.
5. Manual: spawn an unknown/legacy NULL-type task, confirm it falls back to task-shape handoff (no crash).
