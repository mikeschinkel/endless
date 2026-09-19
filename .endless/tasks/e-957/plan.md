# E-957: General-purpose `task link`/`unlink` CLI

## Context

Endless task↔decision pairs and other cross-task relationships have become painfully common (E-957, E-958, E-975/E-976, E-977/E-978). Today only `block`/`unblock` and `replace` create typed relations, and the display layer surfaces only those two dep_types — every other linkage gets lost in description text, unlinkable and unqueryable.

E-958 (now confirmed) locks the vocabulary: `link` is the canonical CLI verb (the agent-instinctive choice; see `~/.claude/projects/-Users-mikeschinkel-Projects-endless/memory/feedback_cli_naming_agent_friction.md`). `block` and `replace` survive as thin shortcut wrappers. Six initial dep_types are frozen: `blocks/blocked_by`, `needs` (kept only as a transition synonym → migrated away), `replaces/replaced_by`, `implements/implemented_by`, `informs/informed_by`, `relates_to`.

This plan implements E-957's full punch list. Outcome: any task or decision can be linked to any other with a typed relationship; `task show` displays all relations grouped; `task add` and `decision add` accept at-creation flags; reverse lookup via `task list --related-to`; legacy `block`/`replace` continue to work.

## Decisions resolved up front

1. **Storage direction:** active voice. A row stores `source=actor, target=acted_upon, dep_type=<active_verb>`. So a row with `dep_type='blocks'` reads "source blocks target" — source is the blocker, target is the blocked task. Existing `needs` rows (stored as `source=blocked, target=blocker, dep_type='needs'`) get their source/target swapped AND the dep_type renamed to `'blocks'`. **This also fixes a latent bug in `replaces` rows** — `replace_task` (task_cmd.py:2128-2133) currently stores `source=old, target=new, dep_type='replaces'`, which reads literally as "old replaces new" but the comment and behavior treat it as "old replaced_by new". The label is active but the layout is passive — they disagree. Migration swaps source/target for existing `replaces` rows so the active label matches the layout going forward.

   *Why this over passive storage:* the CLI verb (`task block`, `task replace`), the stored dep_type (`'blocks'`, `'replaces'`), and the row layout all align. Active voice is what agents and humans reach for naturally; reading raw SQL says the same thing the verb implies. Worth the one-time migration cost.

2. **Canonical stored dep_types** (5 stored, all active voice; 4 inverse views computed): `blocks`, `implements`, `informs`, `replaces`, `relates_to` are stored. Their inverses `blocked_by`, `implemented_by`, `informed_by`, `replaced_by` are the same row queried with source/target swapped. `relates_to` is symmetric (no inverse name).
3. **`decision add --about` vs `--decides`:** two flags, two semantics. `--about <task-id>` → `dep_type=informs` (soft: decision provides context). `--decides <task-id>` → `dep_type=implements` with source=task, target=decision (hard: task implements this decision; equivalent to `implements` from the task side).
4. **Legacy CHECK constraint** in `db.py:147-148` (`CHECK (dep_type IN ('blocks', 'needs'))`) must be dropped from any DB that has it. SQLite has no `DROP CHECK`, so the migration uses table-rebuild (CREATE new, INSERT … SELECT, DROP, RENAME) inside a transaction. Migration is idempotent (detects CHECK in `sqlite_master.sql`).
5. **`add_item` returns the new id.** Cleaner than threading through the event payload. Only the CLI layer at cli.py:557 currently ignores the return.
6. **`unlink` without `--as`:** when omitted, query all dep_types between the pair. Zero → "no relation" error. One → remove it. Multiple → error listing them and require `--as`. Never silent delete.
7. **Display order in `task show`:** fixed, action-priority descending, only render groups with rows: `blocked_by`, `blocks`, `implements`, `implemented_by`, `informs`, `informed_by`, `replaces`, `replaced_by`, `relates_to`.

## Critical files

- `/Users/mikeschinkel/Projects/endless/src/endless/task_cmd.py` — relation CRUD and display logic (lines 2119-2302, plus task_next at 715-728, detail_item at 1543-1714, add_item at 1124-1170, show_plan at 551-703)
- `/Users/mikeschinkel/Projects/endless/src/endless/cli.py` — CLI registrations (block/replace/unblock/deps at 696-731, task add at 528-557, task update at 560-589, task list at 362-394, decision add at 777-792)
- `/Users/mikeschinkel/Projects/endless/src/endless/db.py` — schema migration (lines 73-163, particularly 132-153 for task_deps init)
- `/Users/mikeschinkel/Projects/endless/internal/schema/schema.sql` — canonical schema (line 108: `DEFAULT 'blocks'` → `DEFAULT 'blocked_by'`); also `//go:embed`-ed by `/internal/schema/schema.go` so any change ripples to Go consumers
- `/Users/mikeschinkel/Projects/endless/tests/test_relations.py` — new

## Phase 1 — Schema, vocabulary constant, migration

Files: `db.py`, `schema.sql`, `task_cmd.py`

1. `schema.sql:108` — `DEFAULT 'blocks'` is already correct for active-voice storage; **no change needed**. (The default expressed the original intent; data drift introduced `needs`.)
2. `db.py:138-153` — drop the `CHECK` clauses on `dep_type`, `source_type`, `target_type` (the source/target_type CHECKs go too — they're stale once cross-project relations expand; `schema.sql` doesn't have them, so converge). `DEFAULT 'blocks'` stays.
3. `db.py` — add `_migrate_task_deps_v5(conn)` invoked from `_migrate` after `_migrate_v4`. Idempotent. Steps wrapped in `BEGIN`/`COMMIT` with `PRAGMA foreign_keys=OFF` around the rebuild:
   - **Drop CHECK** if present: read `sql` from `sqlite_master` for `task_deps`. If contains `CHECK (dep_type`: rebuild table (CREATE `task_deps_new` matching `schema.sql`, `INSERT INTO task_deps_new SELECT * FROM task_deps`, `DROP TABLE task_deps`, `ALTER TABLE task_deps_new RENAME TO task_deps`).
   - **Migrate `needs`/`blocks` rows to active-voice `'blocks'` with swap:**
     ```sql
     UPDATE task_deps
     SET    source_id = target_id,
            target_id = source_id,
            dep_type  = 'blocks'
     WHERE  dep_type IN ('needs', 'blocks');
     ```
     SQL semantics evaluate all RHS expressions against the original row before any column is written, so source/target swap atomically.
   - **Fix latent `replaces` direction bug** (swap only, no rename — label is already correct):
     ```sql
     UPDATE task_deps
     SET    source_id = target_id,
            target_id = source_id
     WHERE  dep_type = 'replaces';
     ```
   - **Collision safety:** UNIQUE constraint could theoretically fire if two tasks were both linked in opposite directions (e.g. A blocks B AND B blocks A as separate rows). Migration runs in a transaction; if `IntegrityError` raised, abort with a clear error pointing the user to the backup at `~/.endless/backups/`.
4. New constant in `task_cmd.py` near top:
   ```python
   # display_name -> (stored_dep_type, swap_source_target)
   CANONICAL_DEP_TYPES = {
       "blocks":          ("blocks", False),         # source blocks target
       "blocked_by":      ("blocks", True),          # inverse view
       "implements":      ("implements", False),     # source implements target
       "implemented_by":  ("implements", True),
       "informs":         ("informs", False),        # source informs target
       "informed_by":     ("informs", True),
       "replaces":        ("replaces", False),       # source replaces target
       "replaced_by":     ("replaces", True),
       "relates_to":      ("relates_to", False),     # symmetric
   }
   ```

**Verification:** `sqlite3 ~/.endless/endless.db ".schema task_deps"` shows no CHECK. `SELECT DISTINCT dep_type FROM task_deps` returns subset of {blocks, implements, informs, replaces, relates_to}. Spot-check a known blocked task: row should have `source_id=blocker, target_id=blocked, dep_type='blocks'`. New migration test (Phase 7) passes.

## Phase 2 — Core `link_tasks` / `unlink_tasks` primitives

Files: `task_cmd.py`, `cli.py`

1. `task_cmd.py` — add `link_tasks(source_id, target_id, dep_type)`:
   - Validate `dep_type in CANONICAL_DEP_TYPES`; resolve to (stored, swap); if swap, swap source/target.
   - Insert. Reuse the UNIQUE-error message pattern from `add_dep`.
2. `task_cmd.py` — add `unlink_tasks(source_id, target_id, dep_type=None)`:
   - If `dep_type`: normalize via swap map; delete with strict `dep_type =` filter.
   - If not: query all rows between the pair (both directions), apply Issue 5 logic.
3. `task_cmd.py` — `add_dep` and `remove_dep` become thin shims that call `link_tasks(..., 'blocked_by')` / `unlink_tasks(..., 'blocked_by')`. The display name `'blocked_by'` resolves via the swap map to stored `'blocks'` with source/target swapped — so existing callers preserve their semantics. Delete the shims after Phase 3 completes.
4. `cli.py` — register `task_link` and `task_unlink` after line 731:
   ```python
   @task_cmd.command("link")
   @click.argument("source_id", type=TASK_ID)
   @click.option("--to", "target_id", type=TASK_ID, required=True)
   @click.option("--as", "dep_type",
                 type=click.Choice(list(CANONICAL_DEP_TYPES)),
                 required=True)
   ```
   `task_unlink` mirrors but `--as` is optional.

**Verification:** `endless task link E-1 --to E-2 --as blocks`; `endless task link E-1 --to E-2 --as relates_to`; `endless task unlink E-1 --to E-2 --as blocks`; `endless task unlink E-1 --to E-2` (errors when ambiguous).

## Phase 3 — Refactor `block`/`replace`/`unblock` into wrappers

Files: `cli.py`, `task_cmd.py`

1. `cli.py:700-703` — `task_block` body becomes `link_tasks(item_id, blocker_id, 'blocked_by')`.
2. `cli.py:720-723` — `task_unblock` body becomes `unlink_tasks(item_id, blocker_id, 'blocked_by')`.
3. `cli.py:710-713` — `task_replace` keeps its compound shape (relation + status change).
4. `task_cmd.py:2119` — `replace_task(old_id, new_id)` calls `link_tasks(old_id, new_id, 'replaced_by')` instead of inline insert. (The display name `'replaced_by'` resolves to stored `'replaces'` with swap=True, so the row ends up `source=new_id, target=old_id, dep_type='replaces'` — reads correctly as "new replaces old".) Obsolete-status update stays. Preserve the specialized "already replaced by" error message; map `link_tasks`'s generic UNIQUE error here.
5. After verifying coverage, delete the `add_dep`/`remove_dep` shims.

**Verification:** existing shell history (`endless task block E-X --by E-Y`, `endless task replace E-X --by E-Y`) continues to work. `endless task deps` output unchanged for fixtures from before the migration.

## Phase 4 — Display: `task show` and `task deps`

Files: `task_cmd.py`

1. Replace `get_deps_for_display` (line 2266) with `get_all_relations(item_id) -> dict[str, list[row]]` keyed by display-name in the fixed Decision-7 order. One SQL: `SELECT td.dep_type, td.source_id, td.target_id, ... FROM task_deps td ... WHERE source_id = ? OR target_id = ?`. Bucket rows in Python via `(stored_dep_type, is_source)` → display-name lookup (inverse of CANONICAL_DEP_TYPES).
2. Rewrite `show_deps` (line 2198) to iterate the dict in fixed order. Render only non-empty groups.
3. Update `detail_item` at line 1656 to use `get_all_relations`.
4. Add a `relations` command in `cli.py` that mirrors `deps`. Update `deps` help text to note `relations` is the new name; keep `deps` working forever.
5. Update `task next` query at `task_cmd.py:721-727`. Today reads "exclude tasks where source is the current task and target is unconfirmed" (using passive `needs` storage). With active `blocks` storage, semantics flip to "exclude tasks where they appear as the *target* of a `blocks` edge from an unconfirmed source":
   ```sql
   AND t.id NOT IN (
       SELECT td.target_id FROM task_deps td
       WHERE td.target_type = 'task' AND td.dep_type = 'blocks'
         AND td.source_id IN (
             SELECT t2.id FROM tasks t2 WHERE t2.status != 'confirmed'
         )
   )
   ```

**Verification:** `endless task show E-957` and `endless task deps E-957` show grouped output with new dep_types. `endless task relations E-957` shows the same. `endless task next` still ranks correctly with respect to blockers.

## Phase 5 — At-creation flags on `task add` and `decision add`

Files: `task_cmd.py`, `cli.py`

1. `task_cmd.py:1170` — `add_item` returns `item_id`.
2. `cli.py:528-557` — add to `task_add`:
   - `--blocks` (multiple, TASK_ID) → `link_tasks(new_id, t, 'blocks')` for each
   - `--blocked-by` (multiple, TASK_ID) → `link_tasks(new_id, t, 'blocked_by')`
   - `--relates-to` (multiple, TASK_ID) → `link_tasks(new_id, t, 'relates_to')`
   - `--implements` (multiple, TASK_ID) → `link_tasks(new_id, t, 'implements')`
3. `cli.py:777-792` — add to `decision_add`:
   - `--about` (multiple, TASK_ID) → `link_tasks(decision_id, t, 'informs')` — stores "decision informs task" (active stored, no swap)
   - `--decides` (multiple, TASK_ID) → `link_tasks(t, decision_id, 'implements')` — stores "task implements decision" (active stored, no swap; note source/target order at the call site reflects who is acting)

**Verification:** `endless task add "T1" && endless task add "T2" --blocked-by E-<T1> && endless task show E-<T2>` shows `blocked_by` group. `endless decision add "D1" --about E-<T1> --decides E-<T2>` creates two relations correctly.

## Phase 6 — Reverse lookup on `task list`

Files: `cli.py:362-394` (`task_list`), `task_cmd.py:551-703` (`show_plan`)

1. Add CLI options `--related-to <id>` and `--rel-type <display_name>` to `task_list`.
2. In `show_plan`, when `related_to` provided: build a candidate-id set with a single SQL — `SELECT source_id, target_id, dep_type FROM task_deps WHERE source_id = ? OR target_id = ?` — and filter out the input id. If `rel_type` provided, normalize via `CANONICAL_DEP_TYPES` and filter rows by stored dep_type plus correct direction.
3. Intersect this id-set with the WHERE clause's existing parameterized query.

**Verification:** `endless task list --related-to E-957`; `endless task list --related-to E-957 --rel-type blocks`; `endless task list --related-to E-957 --rel-type implemented_by`.

## Phase 7 — Tests

New `/Users/mikeschinkel/Projects/endless/tests/test_relations.py`. (Confirm test infra by reading existing `tests/conftest.py` and an existing `test_*.py` file.)

Cases:
- `test_link_unlink_roundtrip` per dep_type (parametrized)
- `test_inverse_pair_stored_once_queried_both_ways`
- `test_at_creation_flags_task_add` — covers `--blocks`, `--blocked-by`, `--relates-to`, `--implements`
- `test_at_creation_flags_decision_add` — covers `--about`, `--decides`
- `test_task_list_related_to`
- `test_task_show_renders_all_groups_in_fixed_order`
- `test_unique_constraint_friendly_error`
- `test_self_link_rejected`
- `test_unlink_ambiguous_requires_as`
- `test_migration_strips_check_swaps_and_renames` — seed a CHECK-constrained `task_deps` with `'needs'` rows (passive layout: source=blocked, target=blocker) and `'replaces'` rows (passive layout: source=old, target=new), run `_migrate`, assert: CHECK gone, `needs` rows now `dep_type='blocks'` with source=blocker/target=blocked, `replaces` rows still `dep_type='replaces'` but with source=new/target=old
- `test_legacy_block_command_still_works` — golden behavior for `task_block` shim
- `test_replace_command_still_sets_obsolete` — same for `replace_task`

**Run:** `just test` (per project CLAUDE.md).

## Risk callouts

- **CLI muscle memory** — `block`, `replace`, `unblock`, `deps` all keep working as shortcuts. No breaking change.
- **Existing scripts parsing `task deps` output** — output gains new sections (informs, implements, etc.). Grep the repo for any script piping `endless task deps`; update callers in this PR.
- **JSON / LLM exporters** — `detail_item` LLM output (lines 1599-1636) and JSON output (1566-1597) reference dep_types. Confirm both gain the new groups; don't break existing automation reading those formats.
- **`add_item` return type** — public-ish helper changes from `None` to `int`. Audit all callers (`import_json`, bulk-import path, any test fixture); the `_ = add_item(...)` style is fine but a positional-positional return-type assertion in tests would break.
- **Schema embed** — `internal/schema/schema.sql` is `//go:embed`-ed by `/internal/schema/schema.go`. Go-side consumers pick up the changed default value automatically; verify `internal/monitor/db.go:294` (which has its own inline `CREATE TABLE task_deps`) is consistent or scheduled for E-894 migration.
- **Migration irreversibility** — once Phase 1's `UPDATE`s run, there's no rollback short of a backup restore. `_backup_db()` (db.py:39) auto-snapshots before any migration. Document in CHANGELOG. The `replaces` row swap is the higher-stakes one — if any external tooling reads `task_deps` directly and assumed the old (passive) `replaces` layout, it will break. Grep the repo (Python and Go) for any direct `replaces` SELECT before merging.
- **Old `replace_task` UNIQUE error message** — refactoring through `link_tasks` may produce a generic "already linked" message instead of "already replaced by". Preserve a specialized message in `replace_task`.

## Out of scope

- E-980 `--decision <text>` flag on `task add`/`task update` (auto-creates a decision and links it). Builds on this work but lives in its own task.
- E-977/E-978 `implements` link-type dedup decision — this plan ships `implements` as one of the six canonical types; if E-977/E-978 want a more specialized command on top, that's separate.
- Cross-project relation UX (project-prefixed IDs in flags). Schema already supports it; CLI sugar deferred.
- Graph visualization / ASCII tree of relations.
- Migrating `internal/monitor/db.go:294`'s inline CREATE — tracked under E-894.

## End-to-end verification (post-PR)

```
just test                                                   # all tests pass
endless task add "T1"
endless task add "T2" --blocked-by E-<T1> --relates-to E-<T1>
endless task show E-<T2>                                    # shows blocked_by + relates_to groups
endless task link E-<T1> --to E-<T2> --as implements
endless task list --related-to E-<T1> --rel-type implemented_by
endless task unlink E-<T1> --to E-<T2>                      # errors: ambiguous
endless task unlink E-<T1> --to E-<T2> --as implements
endless task block E-<T1> --by E-<T2>                       # legacy shortcut
endless decision add "D1" --about E-<T1> --decides E-<T2>
endless task next                                           # blockers respected
sqlite3 ~/.endless/endless.db "SELECT DISTINCT dep_type FROM task_deps"
                                                            # only canonical 5: blocks, implements, informs, replaces, relates_to
sqlite3 ~/.endless/endless.db "SELECT source_id, target_id, dep_type FROM task_deps LIMIT 5"
                                                            # spot-check: source is the actor in active voice
```
