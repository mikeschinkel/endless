# Derive an event's project from its entity

Revised with Mike, 2026-10-06, replacing the 2026-10-05 plan (which had not
been vetted). Answers: EQ-3..EQ-7. Cross-project link storage stays in E-2247.

## Scope

Entities whose events take their project from the entity: tasks, decisions,
task questions. Not in scope: links (task_dep, decision_relation — they may be
cross-project), sessions (not project-specific), glossary terms and lessons
(not ledger events today). Creation events (`task.created`, `task.imported`,
`decision.created`) keep `--project`, else cwd.

## 1. Go derives the project at emit (`endless-go event emit`)

- One lookup, `monitor.EntityProject(entityType, entityID) (name, root)`:
  task → `live_tasks.project_id`; decision → `decisions.project_id`;
  task_question → its task (`task_questions.task_id`). Root via
  `ProjectPath`/`ResolvedProjectPath`. Under the storage split (E-2215) only
  this function changes.
- In `run()` (internal/eventcmd/event.go) and `emitQuestionsAsked`
  (internal/eventcmd/question.go): for a non-create event on one of those
  entity types, stamp the looked-up project and use the looked-up root for
  `NewWriter`, `CommitLedgerSegment` and `makeDerivedEmitter`. `--project` and
  `--project-root` become optional for these events and are ignored when
  present. An entity that does not exist is refused, naming it.
- No refusal for a disagreeing `--project`: the entity decides (EQ-3).

## 2. Python stops sending the cwd project for those events

- `event_bridge.emit_event`: `project` optional; when omitted, neither
  `--project` nor `--project-root` is passed.
- Drop `_resolve_project(None)` before the emit at every existing-entity call
  site: task_cmd.py remove/complete/assume/mark_completed/decline/
  `_emit_ratings`/submit/approve/`_perform_claim_work`/bind/`_reopen_task_core`/
  update_plan/supersede/move; session_cmd.py `_emit_task_status_change`;
  worktree_cmd.py `_record_landing`, `_record_only_landing`. Decision and
  question call sites already pass the entity's project; they may keep it
  (ignored) or drop it.
- Effect: `task update E-N` works from any directory, including one with no
  registered project, and never refuses for lack of one.

## 3. No cross-project parent (EQ-4)

- Refuse a parent in another project, in Go at the executor so every path is
  covered: `task.created`/`task.imported` with `parent_id`, `task.moved`,
  `task.fields_updated` with `parent_id`. Message names both tasks and
  projects. Links stay allowed.
- `task move --children-of` writes `parent_id` with a raw UPDATE and no event
  (task_cmd.py ~8198). Replace with one `task.moved` emit per child so the
  guard and the ledger both see it.
- With parents same-project, `epic.status_derived` sharing the triggering
  event's project and writer is correct; no second writer.

## 4. Home directory and the ignore list (Mike: yes, 2026-10-06)

- Registration (Go auto-register `ensureAutoRegisteredProject`, Python
  `project register`/`init`) refuses `$HOME` always, and any directory on the
  global config's `ignore` list.
- The cleanup (§5) runs `endless project unregister mikeschinkel` (path `~`;
  this DELETES the projects row — it holds 0 tasks, decisions and sessions)
  and removes `~/.endless` once its one line has moved (Mike: yes).

## 5. One-time cleanup of the existing ledgers (EQ-6, EQ-7)

- A one-time script, not a product command. Mike gave explicit OK to edit the
  real ledgers for this.
- Scan every registered project's ledger; an event about a task/decision/
  question whose stamped project differs from the entity's project is
  misattributed. Found 2026-10-06: 20 of 12,980 — 19 in endless' ledger
  (init 9, gomion 7, go-cfgstore, go-tealeaves, h2), 1 in `~/.endless`
  (belongs to endless).
- Move each line: remove it from its segment, append it to the owning
  project's ledger with `project` corrected and `ts` unchanged.
- Mike reviews the diffs; then commit each repo's ledger paths only
  (`CommitLedgerSegment`'s `git commit -o`; ~/.init has unrelated staged
  files that must stay uncommitted).
- Run after this task's code lands, so no new misattributed lines appear.

## Verification (`.endless/tasks/e-2216/verify.sh`)

- Go tests, against a temp DB with two projects: an update to project B's
  task with `--project A --project-root <A>` is stamped B and written to B's
  ledger; A's ledger unchanged. Same for a decision and a question resolve.
- A create with no entity still uses `--project`.
- Parent guard: create/move/update to a cross-project parent is refused.
- Python test: `task update` run from outside any registered project emits
  without `--project`.
- The scan run against the real ledgers (read-only) reports 0 misattributed
  events after the cleanup.
