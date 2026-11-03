# Correct anything the ledger tracks

Add `endless correct`, a user-only command that changes tracked state through the ledger and records why. A correction takes two kinds of entry, so that replay and audit each read only what they need.

## Entries

1. `ledger.row_changed` is the change entry. It names the target, the row's key and the operation (update, insert or delete), and for an update or insert the values written. It also carries `like_kind`: the existing kind this change resembles (for example `task.status_changed`, `task.fields_updated`, `decision.fields_updated`, `project.updated`, `task.dep_created`, `task.dep_deleted`), left out when none fits. The field is a label only: nothing dispatches on it, so a change entry triggers no existing kind's effects. Executor and projector both apply the entry.
2. `ledger.corrected` is the correction entry. It names the entity, the reason, whether gates were skipped, the before values, and `refs`: the byte offset of each of its change entries in the segment file both are written to. An annotation has empty `refs`. It writes no database row, and the projector treats it as a declared no-op.
3. One run holds the ledger lock, appends its change entries and then the correction as adjacent lines, and commits them in one ledger commit. The ledger commit subject names the kind and entity, so `git log` shows corrections.

## Lock and offsets

- `Writer` takes an exclusive file lock per ledger around every append, so all events are serialized, not only corrections. The lock file lives in the gitignored `.endless/tmp/`.
- A correction run holds the lock from before its first change entry until after its correction entry, so no other event can land between them.
- Under the lock, the first offset comes from `os.Stat().Size()` on the segment, and each later offset adds the previous line's length, newline included.
- The run checks the rotation limit once, before its first line, so all its lines share one segment. A segment can go a few lines over its limit, which is harmless.
- Because the lines are adjacent, `refs` is redundant with the file's layout: a damaged offset can be rebuilt by walking back as many lines as `refs` has entries.

## Command

- `endless correct <target> <id> --set col=value ... --reason <text>` updates a row.
- `endless correct <target> --insert col=value ... --reason <text>` inserts a row. It may set the listed columns plus any column the table requires.
- `endless correct <target> <id> --delete --reason <text>` deletes a row, refusing when other rows refer to it.
- With no `--set`, `--insert` or `--delete`, it records an annotation.
- `--reason` is required. `--skip-gates` bypasses gates and is recorded in the correction entry.
- It refuses when `agent_env.present()` is true.
- A `--set` that changes nothing is refused.

## Gates

On by default, and they only refuse. For a task's status: the transition table and the plan-item approval check. For status and phase columns of any entity: the vocabulary check. Existing foreign keys apply even with `--skip-gates`. `--skip-gates` never skips the column list.

## What can be corrected

A target is one table the ledger writes, not an entity type, because one entity type can write several tables: `task` events write tasks, task_content, task_plan_items, task_landings and the reconciliation tables. A table with a composite key takes its key parts joined by `:`.

| Target | Table | Columns |
|---|---|---|
| task | tasks, task_content | title, description, phase, type, status, parent_id, ratings, completed_at, verified_sha, and the content fields task.fields_updated accepts |
| task_dep | task_deps | dep_type |
| project | projects | label, group_name, description, status, language |
| session | sessions | state, task_id, focus_task_id, hidden |
| session_status | session_statuses | headline, summary |
| session_tasks | session_tasks | relation_id |
| note | notes | message, resolved, resolved_at |
| decision | decisions | title, description, text, notes, origin_task_id, rejection_reason, status |
| decision_relation | decision_relations | relation_type |
| task_question | task_questions | answer, status, reason |
| shipped_file | shipped_file_writes | sha256 |
| plan_item | task_plan_items | statement, kind, state, verify, detail, approved_statement, approved_kind, approved_at, post_land, approved_post_land |
| session_hidden_task | session_hidden_tasks | none: insert and delete only |
| session_pinned_task | session_pinned_tasks | none: insert and delete only |
| session_notice | session_notices | notified |
| task_landing | task_landings | merge_commit_sha, base_branch |
| task_reconciliation | task_reconciliations | plan_digest, head |
| task_reconciliation_entry | task_reconciliation_entries | verdict, note, holds_reason |
| task_post_land_entry | task_post_land_entries | verdict, note, holds_reason |

`conversation`, `message` and `project_next` have no table, so they accept annotations only. An unknown target or an unlisted column is refused, naming the columns that are allowed.

## After the change

A corrected field that has a mirror file gets its mirror rewritten. Nothing else follows: no worktree or session change and no epic derivation. Correcting an epic's status prints that the next derivation can overwrite it.
