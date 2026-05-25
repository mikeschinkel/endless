# E-1469 — Render handoff from a template at spawn; drop `tasks.prompt`

## Context

Today a spawned session's opening prompt is **hand-authored per task** and stored in
`tasks.prompt`. Agents must write it, and because it sits right next to the plan
(`tasks.text`), the two drift — a prompt that contradicts the plan, or a plan
re-stated in the prompt. But the prompt is almost entirely boilerplate: orient,
read the guide + plan, default interaction rules,
return path, closing. The only per-task variables are the task id, title, and the
spawning pane.

This change deletes the stored field and **generates** the handoff from a single
template merged with task metadata + runtime context. Agents stop writing prompts
entirely, so prompt-vs-plan drift becomes structurally impossible. `tasks.text`
(the plan) remains the only authored, per-task narrative.

Decisions (confirmed with Mike this session):
- **Template lives in a standalone `.md` file**, loaded the same way `endless guide`
  loads `docs/guide/*.md` (a `__file__`-relative path; works because install is
  editable). No packaging work.
- **Lean template that delegates to `endless guide`.** Identity/origin + "read the
  guide + plan" + beacon + return path + brief closing. The drive-to-completion /
  loose-ends / tone rules stay in the guide as the single source.

Per the one-user / no-shipped-product rule, there is **no data migration** — existing
`tasks.prompt` content is throwaway and is dropped.

## The template

New file `docs/templates/handoff.md` (rendered with `string.Template`, `$field` syntax):

```
You're working on E-$spawned_id: $title.
Spawning session: E-$spawner_task — return to it with `tmux select-window -t $return_anchor`.

1. Run `endless guide` to learn the Endless workflow.
2. Run `endless task show E-$spawned_id --text` to read the plan.
3. The task is already claimed and you're in its worktree — just do the work.
4. When implementation is done: `endless task update E-$spawned_id --status verify`,
   tell me how to test, and print the return line above.
5. Don't run `endless worktree land`/`drop` without asking. File drive-by work as
   separate tasks (`--cleans-up E-$spawned_id`) and confirm before implementing.

Goal: drive this task to a state I can confirm and land cleanly. Don't leave loose ends.
```

Substitutions: `$spawned_id`, `$title` from the task row; `$return_anchor` = the
spawner's `$TMUX_PANE`; `$spawner_task` = the spawning session's active task id
(reuse existing session→task lookup). Use `.safe_substitute()`; when run outside
tmux / with no active task, substitute graceful fallbacks (`?` for the task, a
"(run from the spawning pane)" note for the anchor).

## New rendering code (`src/endless/task_cmd.py`)

- Add `render_handoff(spawned_id, title, return_anchor, spawner_task_id) -> str`:
  resolve the template path via `Path(__file__).resolve()...` (mirror the guide
  loader at `cli.py:259`), `string.Template(...).safe_substitute(...)`.
- Replace `show_prompt()` (`task_cmd.py:2882-2897`) with `show_handoff(item_id)`:
  fetch `id, title` for the task, resolve runtime context ($TMUX_PANE, current
  session's active task), call `render_handoff`, `click.echo` the result. No
  "prompt not set" error path — a handoff always renders.

## Spawn flow (`src/endless/task_cmd.py` `spawn_plan`, 2920-3110)

- SELECT: drop `p.prompt` from the query (2946).
- Delete the "no prompt set" validation (2958-2962) — handoff is always available.
- Capture the spawner's pane (`os.environ["TMUX_PANE"]`) and resolve its active
  task **before** creating the new window.
- Replace the "write `item['prompt']` to temp file" block (3021-3027) with
  `render_handoff(...)` → temp file. Everything downstream (load-buffer/paste) is
  unchanged.

## CLI surface (`src/endless/cli.py`)

- `task prompt` (1214-1219) → rename to **`task handoff`**; call `show_handoff`.
  Update the docstring ("Render the spawn handoff for a task").
- `task show`: remove `--prompt`/`show_prompt` (801-802) and its threading
  (811, 817-818). No `--handoff` flag added — `task handoff <id>` covers inspection.
- `task search`: remove `--prompt`/`search_prompt` (909-910) and its threading
  (918, 925).
- `task update`: remove `--prompt`/`prompt_file` (1011-1012) and its threading
  (1033, 1048).
- `task spawn`: update `--no-plan` help (1227) and docstring (1239) — no longer
  "the task's prompt".

## task_cmd.py field plumbing

- `_FIELD_LABELS`: remove `"prompt": "Prompt"` (159).
- `_format_field_value`: change `if name in ("text", "prompt")` → `if name == "text"` (188).
- `update_plan`: remove `prompt_file` param (2567), remove `prompt` from the SELECT
  column list (2584), delete the prompt-file handling block (2649-2653).
- `detail_item`: remove `show_prompt` param (2722), remove `t.prompt` from SELECT
  (2731), remove the JSON (2761), LLM (2800-2801), and human (2860-2863) prompt
  branches.
- `search_tasks`: remove `search_prompt` param (3120) and the `prompt LIKE` clause
  (3170-3172).

## Go / schema

- `internal/schema/schema.sql`: delete `prompt TEXT,` (98) so fresh DBs omit it.
- `internal/events/executor.go`: remove `"prompt": "prompt"` from `allowedFields`
  (315). New events won't carry `prompt`; consistency.
- `internal/events/projector.go`: remove `"prompt": "prompt"` from `allowedFields`
  (246). **Load-bearing:** on `rebuild-db`, historical `task.fields_updated` events
  still carry `prompt`; the projector's `if !ok { continue }` (252-255) then *skips*
  them instead of issuing `UPDATE tasks SET prompt=?` against the dropped column.
- New change file `internal/schema/changes/e-1469-drop-tasks-prompt.sql`:
  `ALTER TABLE tasks DROP COLUMN prompt;` (modernc.org/sqlite v1.48.2 supports
  DROP COLUMN; `prompt` is in no trigger/index/FK, so the drop is clean). This runs
  once, at `just land` time, against the populated real DB where the column exists.
  The sandbox (`endless-sandbox init --mode empty`) and tests build from the edited
  `schema.SQL` and never apply change files, so there is no fresh-DB "column absent"
  error to guard against.

## Docs (`endless guide`)

- `docs/guide/orchestration.md`: delete "Prerequisite: set the prompt"; in the spawn
  step list change "Reads the task's prompt; refuses if absent" to "Renders the
  handoff from the template"; replace the whole "Authoring the prompt" section with a
  short "The handoff is auto-generated; inspect it with `endless task handoff <id>`";
  fix `task prompt` / `task show --prompt` references.
- `docs/guide/index.md` and `docs/guide/tasks.md`: drop `--prompt` from the
  `task show ... --text --prompt` examples; mention `task handoff` where relevant.
- `docs/private/agent-view-bundle/guide-orchestration.md` is a manual mirror (not
  wired to any generator); update it to match if Mike keeps it current — otherwise
  leave to a separate hygiene pass.

## Critical files

- `src/endless/task_cmd.py` — render function, `spawn_plan`, `detail_item`,
  `update_plan`, `search_tasks`, field maps.
- `src/endless/cli.py` — `task handoff`/`show`/`search`/`update`/`spawn` flags.
- `internal/schema/schema.sql`, `internal/schema/changes/e-1469-drop-tasks-prompt.sql`
- `internal/events/projector.go`, `internal/events/executor.go`
- `docs/templates/handoff.md` (new), `docs/guide/{orchestration,index,tasks}.md`

## Verification (in the worktree sandbox)

1. `just build` — Go (schema embed, executor, projector) + Python compile clean.
2. `just test` — Python suite green (no existing tests target the prompt flag/command;
   add a `render_handoff` unit test asserting the rendered text contains the task id,
   title, beacon line, and the `tmux select-window -t <pane>` return line).
3. CLI smoke against the sandbox DB:
   - `endless task handoff <id>` prints the rendered handoff (no "prompt not set").
   - `endless task show <id>` / `task search <q>` / `task update <id> ...` no longer
     accept `--prompt` (flag is gone).
   - `endless task spawn <id>` (inside tmux) opens a window and pastes the rendered
     handoff; confirm the return line names the spawner's pane.
4. Ledger-replay safety: `endless-event rebuild-db` against a copy of a DB whose
   ledger contains historical `prompt` field events — rebuild succeeds (prompt
   fields skipped), no "no such column: prompt" error.
5. At land: `just land E-1469` applies `e-1469-drop-tasks-prompt.sql` to the real DB;
   `endless db ...` confirms the column is gone.

## Out of scope / notes

- No backward-compat reader, dual-read, or data preservation for `tasks.prompt`.
- Resolving the spawner's active task id for `$spawner_task` reuses the existing
  session→task lookup; if it can't be resolved the clause degrades gracefully.
- `spawn_plan`/`show_handoff` continue to use the existing `db.query` reads for the
  task row (title); migrating those specific reads to Go is not part of this task.
