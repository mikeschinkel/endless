# E-2257 findings: machine vs project scope for every table

Classified against `internal/schema/schema.sql` on main (rebased 2026-10-09;
goose migrations through 00018). Row counts and latest writes are from the
main database on 2026-10-09.

## Settled before this classification (Mike, 2026-10-09)

- **Task ids** come from the machine database's `tasks` mirror, which is the
  canonical allocator. A sync job fills in fields such as title from the
  project databases (already decided under E-2215).
- **Nothing is hard-deleted.** Tasks are marked `removed` (E-1929). Mirror rows
  follow the same rule: marked removed, never deleted.
- **EQ-35:** the machine tables that had no FK to tasks on purpose
  (`session_tasks`, `session_hidden_tasks`, `session_notices`, `rater_claims`,
  `session_statuses`) get plain FKs to the mirror, with no ON DELETE action.
  Their reason for having none (outliving a deleted task) went away with E-1929.
  `rater_claims`' comment still gives that reason; it was copied from the
  retired `triage_claims` table.
- **Cross-project links** (`task_deps` rows whose two ends are in different
  projects) belong to E-2249 / E-2247, not here.
- **Project identity** is derived from the repo's root commit and stored in
  `.endless/config.json` (Mike, relayed to E-2217, which owns it). Endless
  requires a Git repo and one project per repo.

## Criterion

A table is **project**-scoped when its rows are shared project history:
written through the ledger and rebuilt from it. Rebuilding a project deletes
and rebuilds its file from its ledger alone (E-2215), so anything in a project
file that replay does not produce is lost on every rebuild. A table is
**machine**-scoped when its rows are local observation or local state, even
when they carry a `project_id` (E-2215's own examples put `errors` there).

## Classification

Rebuilt = produced by ledger replay (`internal/events/projector.go`).

### Project tables

| Table | Rebuilt | Rows / latest | Outbound FKs that cross files |
|---|---|---|---|
| `tasks` | yes | 1573 / today | `project_id → projects` (see Projects); `changed_by_session` (no FK) is a machine session id |
| `task_content` | yes | 3527 / today | none |
| `task_questions` | yes | 36 / Oct 6 | `asked_by_session` (no FK) is a machine session id |
| `task_deps` | yes | 1461 / today | none declared; `target_type='decision'` points into the same project; cross-project rows → E-2247 |
| `task_landings` | yes | 795 / Oct 8 | `session_id → sessions ON DELETE SET NULL` |
| `decisions` | yes | 208 / Oct 8 | `project_id → projects`; `origin_session_id → sessions ON DELETE SET NULL` |
| `decision_relations` | yes | 199 / Oct 8 | none |
| `task_types`, `complexity_levels`, `risk_levels` | seeded from Go enums | 5 / 3 / 3 | none; needed in BOTH files (see Enum tables) |

Views `live_tasks` and `task_tree` read only `tasks`, so they belong in each
project file and need no cross-file version. A cross-project read is a
`UNION ALL` over the attached files' views (E-2215 child 4).

### Machine tables

| Table | Rows / latest | Notes |
|---|---|---|
| `projects` | 72 / Oct 8 | the registry: path, machine-local slug (E-2217), status (`ignored`), label, group, language. View `live_projects` goes with it |
| `sessions` | 1297 / today | `task_id`, `epic_id`, `focus_task_id → tasks` become FKs to the mirror |
| `processes`, `process_kinds` | 459 / today | |
| `session_tasks`, `session_task_relations` | 3861 / today | `task_id` gains a mirror FK (EQ-35) |
| `session_hidden_tasks` | 1482 / Oct 4 | `task_id` gains a mirror FK (EQ-35) |
| `session_notices` | 2777 / today | `task_id` gains a mirror FK (EQ-35) |
| `session_gates`, `gate_kinds` | 137 / **Aug 23** | `epic_id`, `task_id → tasks` become mirror FKs |
| `session_messages`, `session_messages_fts` | 134101 / today | transcripts; the FTS table and its two triggers stay with it |
| `activity` | 106646 / today | hook log, tagged with project_id |
| `jobs` | 11 / today | |
| `rater_claims` | 0 (claims are short-lived) | `task_id` gains a mirror FK (EQ-35) |
| `errors`, `errors_sources`, `error_triage`, `fault_triage_projects` | 38 / Oct 8; 1; 0; 0 | the schema says so outright ("machine-local … never through the ledger"). `task_id`/`fix_task_id` stay plain INTEGERs on purpose: a fault must record even when the task is unknown to the database being written |
| `report_labels`, `report_judgments` | 0; 77 / **Aug 23** | minimizer corpus |
| `minimizer_variants`, `minimizer_champions`, `minimizer_evals`, `minimizer_state` | 183 / today | |
| `notes` | 29 / **Aug 22** | 26 are scan alerts (`sprawl`), 3 typed by hand; written only by Python `notes_cmd`; no `note.*` event was ever written to the ledger |
| `session_statuses` | 10 / **May 14** | dead; E-2062 notes it is being removed separately |
| `goose_db_version` | — | each file gets its own (see follow-up 5) |
| `session_liveness` (TEMP view) | — | per-connection snapshot of live tmux panes; never persisted |

### Never used, or stale

- **Never used:** `project_deps` (0 rows, no writer anywhere; only Python
  `status.py`/`unregister.py` read it), `report_labels` (0 rows),
  `fault_triage_projects` and `error_triage` (0 rows, new in E-2272, so too
  early to judge).
- **Stale:** `session_statuses` (May), `notes` (Aug 22), `session_gates` and
  `report_judgments` (Aug 23).
- **Not in any schema file:** `adhoc_e2191_backlog` (36 rows) and view
  `adhoc_e2191_backlog_v`, made by hand during E-2191.

Recommendation: the one-time migration (E-2215 child 6) should not carry
`project_deps`, `session_statuses` or the `adhoc_e2191_*` objects into the new
layout.

## Projects across the split

- The machine `projects` table stays the registry and the source of
  `projects.id`, which stays a machine-local surrogate integer.
- Each project file needs to know which project it is: a one-row table holding
  the committed identity (root commit) and committed name, written by replay.
  Today `ensureProject` in the projector inserts a `projects` row by name.
- `tasks.project_id` and `decisions.project_id` hold the machine's
  `projects.id`, stamped when the file is built. The project file sits on one
  machine, so the value is stable there. The FK cannot be enforced across
  files; the one-row table makes the column redundant, and it can be kept
  only so cross-project `UNION ALL` reads stay uniform.

## Machine → project FKs: the mirror tables

An inventory of every SQL statement that joins a machine table to a project
table found 28: 21 in Go, 5 trigger bodies, 2 in Python. Only `tasks` is
referenced from the machine side, so **one mirror is needed: `tasks`**.
Nothing on the machine side references `decisions`, `task_content`,
`task_questions`, `task_deps` or `task_landings` by FK, so they need no mirror.

### Fields for the `tasks` mirror

| Field | Why | Statements using it |
|---|---|---|
| `id` | PK; the canonical id allocator | 26 |
| `project_id` | which project file holds the row | 16 |
| `removed` | marked-removed rule; `live_tasks` filter | 18 |
| `status` | session status, jobs, tmux, triage | 16 |
| `title` | session status, tmux tab, session list | 9 |
| `phase` | session status, jobs, tmux | 9 |
| `type_id` | epic walk on claim, tmux, auto-spawn | 8 |
| `complexity_id`, `risk_id` | auto-spawn, rater | 4 each |
| `parent_id` | epic walk on claim; lets a machine-side `task_tree` compute `effective_parent_id` | 3 |
| `created_at`, `updated_at` | auto-spawn, rater, prime, project monitor | 2 each |
| `prime_requested` | prime job | 1 |

Left out, because one statement each uses them and those statements need
project-only tables anyway: `description` (rater context, a Python filing
hint), `verified_sha` (worktree verify state).

### Reads a mirror cannot satisfy

These also need `task_content`, `task_deps`, `task_questions` or
`task_landings`, so they become cross-file reads (E-2215 child 4: ATTACH, or a
machine query for the task set followed by a project query):

- auto-spawn and prime eligibility (`eligibleQuery`, `primeEligibleQuery`):
  open blockers, plan present, no open questions;
- session status (`SessionStatusRows`, `SessionStatusRowsForSession`) and session graph
  (`SessionGraphData`): blockers, supersedes/duplicates, plan presence,
  landed;
- project monitor (`ProjectStatusRows`): landed;
- rater context (`raterContext`): plan and context bodies;
- question routing (`taskQuestions`, `resolveQuestionTarget`);
- worktree verify state (`VerifyStates`): `verified_sha`.

Alternative considered: add derived flags to the mirror (`has_plan`, `landed`,
`open_question_count`, `open_blocker_count`). That would cover auto-spawn,
prime, the project monitor and the per-session status rows, but not the
recursive blocker walks or the rater's content bodies. I recommend against it:
every derived flag is one more value to keep in sync, and stale between
syncs, for reads that ATTACH serves exactly.

### Keeping the mirror current

E-2215 says a reconcile job syncs the mirror, and refreshes it when a project
is rebuilt. Since the mirror is also the id allocator, the executor already
touches it on every task creation. I propose the executor also writes the
mirror fields in the same command, right after the project write commits, with
the reconcile job repairing anything a crash left behind. Without that, `session
status` and the tmux tab show a stale title or status until the next reconcile.
This is a proposal for child 2, not a settled point.

### Notes from the inventory

- `monitor.queryTaskForPanes` and the comment above its caller in
  `sessionstatuscmd` (search "cannot be split across two databases") say pane resolution "cannot be split
  across two databases". With the mirror it no longer has to be: it reads only
  mirror fields.
- `session_liveness` is a per-connection TEMP view over the live tmux snapshot;
  it is machine-scoped and never persisted.
- Project-side Python commands (`task_cmd.py` `next_tasks`, `active_tasks`,
  `landed_list`, `spawn_plan` and others; six `decision_cmd.py` reads) join the
  machine `projects` registry only for `p.name` or `p.path`. The project file's
  one-row identity table supplies the name; the path stays a registry lookup.

## Project → machine references

All three are provenance: "which session did this". None should block a
replayed row, since sessions are never rebuilt from the ledger.

| Column | Today | Proposal |
|---|---|---|
| `task_landings.session_id` | FK `→ sessions ON DELETE SET NULL` | keep the value, drop the constraint (as `task_questions.asked_by_session` already does) |
| `decisions.origin_session_id` | FK `→ sessions ON DELETE SET NULL` | same |
| `tasks.changed_by_session` | plain INTEGER | keep; it is the notify triggers' self-suppression input (see Triggers) |
| `task_questions.asked_by_session` | plain INTEGER, documented as provenance | keep |

## Enum tables

`task_types`, `complexity_levels`, `risk_levels` are needed in every project
file (`tasks` FKs to them) and in the machine file if the mirror carries
`type_id` or ratings. They are seeded from Go enums on connect (ED-1506), so
having them in both is free; both copies come from one seed.
`process_kinds`, `gate_kinds` and `session_task_relations` are machine-only.

## Triggers (beyond the plan's scope, added)

SQLite does not let a trigger in one attached database reference another, so
every trigger that reads or writes across the line must move.

| Trigger | On | Crosses? |
|---|---|---|
| `tasks_updated_at`, `decisions_updated_at` | project tables | no — stays in the project file |
| `sessions_task_id_write_once` | `sessions` | no — stays in the machine file |
| `session_messages_ai`, `session_messages_ad` | `session_messages` | no — machine |
| `tasks_notify_sessions` | `tasks` | **yes** — reads `session_tasks`, `sessions`; writes `session_notices` |
| `task_content_notify_insert/update/delete` | `task_content` | **yes** — same |
| `task_landings_notify_sessions` | `task_landings` | **yes** — same |

Proposal: move the notice fan-out into the Go executor, which already stamps
`changed_by_session` before writing (ED-903: Go is the single writer). It
compares old and new values and writes `session_notices` in the machine file
after the project write commits. The triggers exist so no mutation site can
forget to compare old against new; the executor is now the single site, so one
function carries that rule. A side benefit: a rebuild would no longer fire
fan-out at all (today the scratch DB does, harmlessly, per the trigger's own
comment).

## Indexes

Every index is on a single table and goes with it. None is affected by the
split.

## Mixed-scope tables

No table holds rows of both kinds. Two near cases:

- **`task_deps`** holds `task → decision` rows as well as `task → task`. Both
  ends are in one project, so it stays a project table. Rows whose two ends are
  in different projects belong to E-2247.
- **Session events in the project ledger.** The ledger holds `task.claimed`
  (137), `task.released` (21), `session_tasks.touched` (20),
  `session_status.recorded` (12), `focus.*` (9) and `session_tasks.ordered`
  (1). They describe machine tables and replay ignores them. After the split
  they still sit in the project ledger, harmlessly. Whether to stop writing
  them belongs to E-799.

## Follow-up children this implies (for E-2215)

1. **Mirror tables + reconcile job** (already child 2): the `tasks` mirror as
   the id allocator, the fields listed above, marked-removed only, mirror FKs
   for the tables in EQ-35.
2. **Move the notice fan-out from triggers into the executor.** Must land
   before the split, or the four triggers break.
3. **Drop dead tables:** `project_deps`, `session_statuses` (if E-2062 hasn't
   yet), and the `adhoc_e2191_*` objects; review `report_labels`,
   `session_gates`/`report_judgments` and `notes` for retirement.
4. **Per-project identity row and `project_id` stamping** in the project file,
   consuming E-2217's root-commit identity.
5. **Migrations across N files** (already child 5): each file needs its own
   goose version table, and the migration set must say which file each
   migration applies to.
