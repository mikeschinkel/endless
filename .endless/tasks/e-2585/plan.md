# Plan

Three increments, each a commit boundary on the task branch. They finish the
move E-2063 started. E-2063 made `session_instances` authoritative for
resolution but left `sessions.session_id` as a bridge, holding the UUID of
whichever instance fired last, so every legacy reader kept working. This task
moves those readers, removes the bridge and the columns behind it, then teaches
resume to choose an instance.

The governing sentence is E-2063's: **a fact about the harness conversation
lives on `session_instances`; a fact about the work lives on `sessions`.** The
decision record (why `session_id` is dropped rather than kept, why
`transcript_offset` and `platform` leave `sessions`) is in E-2063's plan, after
its "End of plan" marker.

## Increment A — readers move to the instance, then the bridge goes

Discover every reader and writer of `sessions.session_id`, `sessions.platform`
and `sessions.transcript_offset` at execution time: grep Go under `internal/`
and `cmd/`, and Python under `src/endless/`, including the Python files that
still read SQLite directly. Do not work from a list written now. Each moves to
resolution through `claude_cli_sessions` → `session_instances` → `sessions`
(`monitor.SessionIDForUUID` and `resolveUUID` are the existing door), or to the
integer `sessions.id` where the caller already has it.

Then remove the bridge from `monitor.touchTx`: the writes that keep
`sessions.session_id` equal to the current instance's UUID. Remove the
`sessions.transcript_offset` mirror from `setTranscriptOffset`. Retire
`TouchSession`, the string-platform wrapper over `TouchInstance`, once nothing
calls it.

Test fixtures that INSERT into `sessions` with a `session_id` move to
`TouchInstance`, or to a fixture helper that also writes the instance.

## Increment B — the column moves and the two rebuilds

**`session_messages` re-parents to the instance.** Today `session_id TEXT`
referencing `sessions(session_id)` — the UUID. It becomes
`session_instance_id INTEGER NOT NULL` referencing `session_instances(id)`.
Harness-neutral on purpose: `session_messages` is a shared table, so keying it
on a UUID would bake one harness's identity model into it, which is what the
per-harness tables exist to avoid. A clear gives later rows a new parent and
rewrites no prior row.

Not doable as add-backfill-drop; both halves were tested and both fail:

```
ALTER TABLE session_messages ADD COLUMN x INTEGER NOT NULL REFERENCES ...
  -> Cannot add a NOT NULL column with default value NULL
ALTER TABLE session_messages DROP COLUMN session_id;
  -> unknown column "session_id" in foreign key definition
```

So it is create-copy-drop-rename, which takes `session_messages_ai` and
`session_messages_ad` with it — the two triggers that keep the FTS index in
step. They are recreated verbatim, and the verify script asserts FTS still
returns hits, because a silently-empty external-content index reads as "no
results" rather than as an error.

**`sessions` rebuilds too,** and for the same class of reason: `UNIQUE
(session_id)` is an inline table constraint, so `DROP COLUMN` refuses. Three
columns leave in one copy — `session_id`, `platform`, `transcript_offset`.

**Both rebuilds are goose `.go` migrations** under
`internal/schema/migrations/`, each added as a line in `migrations.Go()` — the
set is explicit, not init()-registered, so a step that is not listed there does
not exist. e-2074 is no longer the model: E-2019 replaced the change-file
runner, and its package doc states when a step must be `.go` — SQLite has no
`DROP COLUMN IF EXISTS`, and a step must tolerate a database already at the
target shape because `schema.sql` is still exec'd directly by tests. Probe each
named object and skip only that, with the reason stated; this is NOT the
baseline's blanket idempotence, which 00001 alone may have.

Three mechanics the rebuilds need, all now documented in the tree:

- **`-- +goose NO TRANSACTION`.** `MigrateContext` sets `PRAGMA foreign_keys=ON`
  before `Up`, and SQLite ignores that pragma inside a transaction — which is
  why `enforceForeignKeys` sets it outside one and says in as many words that "a
  future migration that needs enforcement off for a table rebuild has to declare
  itself `-- +goose NO TRANSACTION` and turn it off for itself." These two are
  that migration. The cost is that neither rebuild is atomic, so each must be
  written to be re-runnable after a partial failure.
- **`PRAGMA legacy_alter_table=ON` around the rename.** Unchanged from e-2074's
  finding: `tasks_notify_sessions` and `task_landings_notify_sessions` name
  `sessions` from other tables and would be reparsed at the moment it does not
  exist.
- **Recreate what `DROP TABLE` takes with it.** `sessions_task_id_write_once`
  after the `sessions` rename — without it write-once silently stops holding on
  every populated database — and `session_messages_ai` / `session_messages_ad`
  after the `session_messages` rename, or the FTS index stops being maintained
  and reads as "no results" rather than erroring.

**No `Down` on either.** A rollback that recreates the columns without their
values restores the shape while lying about having reversed anything; 00003 sets
that precedent explicitly.

**The ordering hazard, and why it is narrow.** Once these apply, any endless-go
binary that still names a dropped column fails with "no such column" on every
hook event. Hooks run the installed binary, and a binary built in a task
worktree never opens the main database (ED-1601), so the window is inside one
land: the land rebuilds the installed binaries right after main advances.
Branches still open when this lands meet the dropped columns on their next
rebase, and their suites find what to change.

`monitor.ParseTranscript` takes the instance id rather than the uuid. The hook
already resolves the instance once per event for `TouchInstance`, so it is passed
in rather than re-derived — which also makes "which conversation is this"
explicit at the call site.

## Increment C — resume picks an instance

A session now has N uuids, so `claude --resume` needs to be told which. Resume
resolves to an INSTANCE.

- Exactly one open instance: that one, silently. This is every ordinary session.
- Two or more open: refuse. List them ordered by `first_seen_at` ASC with
  ordinal, `first_seen_at`, `last_event_at`, `launch_dir` and message count, and
  exit non-zero. Close nothing.
- `--instance <ordinal|uuid>` selects.

Two rules about ordering, stated because the trap is easy to walk into:

1. **`last_event_at` is displayed, never a selection key.** Recency and
   relevance diverge exactly when the stakes are highest — the week-long task
   cleared midway, where resuming instance 1 to read something makes the
   half-finished conversation look like the one to resume.
2. **The ordinal is computed `first_seen_at` ASC and is therefore stable.**
   Instance 1 is always the earliest conversation, so `--instance 2` means the
   same thing tomorrow. A listing may PRESENT in either direction; the ordinal
   it prints is the ASC-derived one.

`resume` cd's to the instance's `launch_dir` rather than unconditionally to the
task's worktree. `claude --resume` resolves a uuid within the current
directory's harness project dir, so resuming a session launched outside its
worktree cannot find the conversation at all today. `_resume_new_window_pane`
gets the same fix — it passes `-c <worktree>` to `tmux new-window`. Where
`launch_dir` and the worktree disagree, resume says so in the line it already
prints; it does NOT pick a winner, because that is E-1983's open question and
its analysis frames it as exactly that. NULL `launch_dir` (an unbackfillable old
row) falls back to the worktree, which is today's behavior.

`_resolve_resume` stays the one shared resolver for `session resume` and
`session goto --resume`; the instance selection goes inside it so both surfaces
agree by construction.

# Verification

`.endless/tasks/e-NNNN/` for this task, run only through the runner
(`just verify <id>`). Same shape as E-2063's suite: a `verify.toml` whose first
`[[check]]` is a `gotest` runner over the unit tests, plus a `verify.sh` that
sources `_harness.sh` for the end-to-end assertions. Mirror the coverage that
must outlive the land into the durable Go suite: items 1 and 3 below.

1. `session_messages` follows the conversation: rows written before a clear
   keep their instance, rows after get the new one, and no prior row is
   rewritten. FTS returns hits after the rebuild.
2. `sessions.session_id`, `.platform` and `.transcript_offset` are gone, and no
   Go or Python code names them.
3. Every pre-migration `sessions` row keeps its instance through both rebuilds,
   and every pre-migration UUID still resolves through `claude_cli_sessions`.
   Assert this as a law against a copy of a real database, not as a row count.
4. `sessions_task_id_write_once` still refuses a second `task_id` after the
   rebuild.
5. Resume with two open instances exits non-zero, lists them `first_seen_at`
   ASC, and closes nothing: `ended_at` values are identical before and after.
   `--instance` selects by both ordinal and UUID.
6. Resume of an instance whose `launch_dir` differs from the worktree starts
   claude in `launch_dir` and says so; a NULL `launch_dir` falls back to the
   worktree.
