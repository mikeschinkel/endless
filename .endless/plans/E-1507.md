# E-1507: Refit Python CLI and Go web queries for the decisions table

## Context

E-1378 landed the Go-side foundation: `decisions` and `decision_relations` tables in `schema.sql`, the data-movement change file (already applied to the real DB at land), all `decision.*` event kinds (executor + projector), `rebuild-db` extension. Nothing user-facing changes until this task lands the CLI and web reads against the new storage.

## Scope

**In scope:**

- New module `src/endless/decision_cmd.py` for decisions-table CLI logic.
- Refit existing `endless decision list/show/add/link/unlink` against the new tables and event kinds.
- New verbs: `endless decision accept <id>` and `endless decision reject <id> --reason "..."`.
- **Remove the `--decision` flag from `endless task add` and `endless task update`.** No deprecation, no shim — the canonical path is `endless decision add "..." --about <task_id>`. (Per the "no backward compatibility prior to initial release" principle.) Drop the Click option declarations in `cli.py` and the corresponding code paths in `task_cmd.py`.
- `endless task link`/`unlink` become target-kind-agnostic dispatchers (route by `target_kind`, validate per-pair type vocabulary).
- `endless decision link`/`unlink` become target-kind-agnostic dispatchers (write to `decision_relations` via events).
- `DECISION_ID` Click type that accepts `ED-42` or `42`.
- Echo-format output with kind labels (`• Linked: Decision ED-42 documents Task E-1199`).
- Drop the five `WHERE t.type != 'decision'` filters in `src/endless/task_cmd.py` and the matching exclusions in Go web queries (`internal/web/queries.go`).
- Tests at each layer (Python and Go).

**Out of scope** (already shipped via E-1378):
- Schema additions, change file, event executor + projector, `rebuild-db` extension.

## CLI surface — per-verb audit

### `endless decision *`

| Verb              | Current                                                                      | Change                                                                                                                       |
|-------------------|------------------------------------------------------------------------------|------------------------------------------------------------------------------------------------------------------------------|
| `decision list`   | Queries `tasks WHERE type='decision'` via `list_decisions()` in task_cmd.py  | Query `decisions` table directly. Same renderer shape (id, title, status, created).                                          |
| `decision add`    | `add_item(..., task_type='decision', status='confirmed')`                    | Emit `decision.created` via `event_bridge.emit_event(entity_type='decision', ...)`; default `status='proposed'`. `--about` emits `decision_relation.created` with relation_type='documents'. `--decides` emits a task-sourced relation with `target_type='decision'`. |
| `decision show`   | Delegates to `detail_item()` (task renderer)                                | New decision-shaped renderer: title, status, description, text, origin task, relations, created/updated. No phase, no worktree, no verify state. |
| `decision link`   | dec↔dec only via `require_decision_pair`                                    | Dispatcher: target = task or decision. Validates `relation_type` for the detected pair. Emits `decision_relation.created`. |
| `decision unlink` | Mirror                                                                       | Mirror of new `decision link`; emits `decision_relation.deleted`.                                                            |
| `decision accept` | **NEW**                                                                      | Emits `decision.accepted`. Errors clearly if status is already `accepted` or `rejected`.                                     |
| `decision reject` | **NEW**                                                                      | Emits `decision.rejected` with required `--reason "..."`. The executor stores the reason in `decisions.rejection_reason`.    |

### `endless task *` changes

| Verb                            | Change                                                                                                                       |
|---------------------------------|------------------------------------------------------------------------------------------------------------------------------|
| `task add` / `task update`      | **`--decision` flag removed.** The inline "create a paired decision while authoring/updating a task" shortcut is gone. Use `endless decision add "..." --about <task_id>` instead. |
| `task link`                     | Dispatcher: target = task or decision. For task→decision, write to `task_deps` with `target_type='decision'` (the column finally reflects reality, set by E-1378's change file). For task→task, unchanged. Validates type vocabulary per pair. |
| `task unlink`                   | Mirror.                                                                                                                     |
| `task confirm` / `task assume`  | After E-1378's migration, decisions are no longer in `tasks`; `task confirm ED-42` resolves to "no task with id 42" via the `DECISION_ID` parser routing. Error message redirects to `decision accept`. |

### Filters to drop

In `src/endless/task_cmd.py`, remove the `WHERE t.type != 'decision'` predicates from these five queries:

- `show_plan()` WHERE clause
- `next_tasks()` WHERE clause
- `active_tasks()` WHERE clause
- `recent_tasks()` WHERE clause
- `search_tasks()` WHERE clause

And the legacy `list_decisions()` query — that whole function moves into `decision_cmd.py` as a decisions-table query.

In `internal/web/queries.go`, remove matching exclusions that filter `type='decision'` out of task views.

### Echo-format output (link/unlink)

```
• Linked: Decision ED-42 documents Task E-1199
• Linked: Task E-908 implements Decision ED-42
• Linked: Decision ED-42 reverses Decision ED-89
• Linked: Task E-908 blocks Task E-1199            (unchanged for task↔task)
• Unlinked: Decision ED-42 documents Decision ED-89
```

Bullet prefix retained for visual parity; kind labels (`Task` / `Decision`) make direction self-documenting. Symmetric for `task link`, `task unlink`, `decision link`, `decision unlink`.

## Relation-type vocabulary by pair

The CLI dispatcher validates `relation_type` against the legal set for the detected pair.

| Pair                | Legal `relation_type` values                                                |
|---------------------|-----------------------------------------------------------------------------|
| decision → task     | `documents`, `cleans_up_by`, `implemented_by`, `relates_to`                 |
| decision → decision | `reverses`, `modifies`, `documents`, `relates_to` (+ future `clarifies`, `reaffirms`) |
| task → decision     | `implements`, `cleans_up`, `documents`, `relates_to`                        |
| task → task         | `blocks`, `implements`, `replaces`, `documents`, `cleans_up`, `relates_to` (unchanged) |

Illegal type for the detected pair surfaces a `ClickException` listing the legal set.

## ID parser

Add a `DECISION_ID` Click type parallel to `TASK_ID`, accepting `ED-42` or `42`. Apply to every decision-targeting CLI argument. Display formatting in CLI and web uses the `ED-` prefix whenever a decision is shown.

## Verification

Verify against this worktree's sandbox (per-worktree throwaway DB the E-1281 sandbox machinery provisions). `--db sandbox` now routes both data writes AND binary execution to the worktree (E-1510 landed). Two CLI shapes depending on whether the verb is wrapped in the Python CLI:

- Python-wrapped (`endless db apply-change`, `endless sql`, all `endless decision *`, `endless task *`): `endless --db sandbox <subcommand>`.
- Not wrapped (`endless-go event emit`, `endless-go event rebuild-db`): `./bin/endless-go --config-dir "$(endless db path --db=sandbox)" event <verb>` — direct Go invocation with the explicit `--config-dir` flag the dispatcher consumes.

```bash
# Seed the sandbox with a real-data copy (E-1508 would replace this with
# `endless sandbox clone-db <this_id>`):
cp ~/.config/endless/endless.db \
   ~/.cache/endless/sandboxes/worktree-e-<this_id>/endless/endless.db

# E-1378's change file already landed to main and was applied to your real
# DB at land time; the sandbox copy already has the decisions /
# decision_relations tables populated.
```

End-to-end checks:

1. `endless --db sandbox decision list` returns existing decisions with `ED-` prefix display.
2. `endless --db sandbox task list`, `task next`, `task recent`, `task active` no longer surface decisions (the `WHERE type != 'decision'` filters were removed; no rows remain anyway post-E-1378).
3. `endless --db sandbox decision add "Test" --description "..."` creates with `status='proposed'`. `endless --db sandbox decision accept ED-<id>` flips to `accepted`. `endless --db sandbox decision reject ED-<id> --reason "..."` flips to `rejected` and stores the reason.
4. `endless --db sandbox decision link ED-42 --to E-1199 --type documents` writes a `decision_relations` row and prints `• Linked: Decision ED-42 documents Task E-1199`.
5. `endless --db sandbox task link E-908 --to ED-42 --type implements` writes a `task_deps` row with `target_type='decision'` and prints `• Linked: Task E-908 implements Decision ED-42`.
6. **`--decision` flag is gone:** `endless --db sandbox task add "X" --decision "Why X"` errors with Click's standard "no such option: --decision". Same for `task update`. The canonical replacement: `endless --db sandbox decision add "Why X" --about E-<task_id>`.
7. `endless --db sandbox task confirm ED-42` errors with a clear redirect: `Decisions don't have a confirm verb; use 'decision accept ED-42'.`
8. Type validation errors: `endless --db sandbox decision link ED-42 --to E-1199 --type blocks` → "blocks is not legal for decision→task; legal types: documents, cleans_up_by, implemented_by, relates_to."
9. **Rebuild round-trip:** `./bin/endless-go --config-dir "$(endless db path --db=sandbox)" event rebuild-db --project-root /Users/mikeschinkel/Projects/endless --confirm` produces a projection whose `decisions` and `decision_relations` counts match the sandbox DB.
10. All Python tests pass (`just test`); Go tests pass (`just test-go`).

## Open questions / deferred

- **Decision web view.** The dashboard's tasks page filters by type (which after E-1378 returns empty). A dedicated `/decisions` route is out of scope; file a follow-on if/when the view matters.
- **`accepted_at` / `rejected_at` columns.** Skipped for now; `status` + `updated_at` cover it. Add if a sort-by-acceptance query becomes load-bearing.

## Closure

- Do NOT mark this task `confirmed` — Mike confirms.
- When implementation passes verification: `endless --db main task update <this_id> --status verify` with explicit "To verify: ..." in the handoff.
- Sibling tasks under the E-1511 epic: E-1383 (team-decisioning state model, `maybe`), E-1389 (rename `task_deps` → `task_relations`). Don't expand scope into them.
