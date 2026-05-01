# E-787: Add `outcome` field and `task decline` verb

## Context

Endless tasks have terminal statuses (`confirmed`, `assumed`, `declined`, `obsolete`) but no field for capturing **why** a task ended that way. Today the answer lives in commit messages, chat transcripts, or nowhere at all — searchable retrieval is impossible. E-787 adds an `outcome TEXT` column, a `task decline` verb that requires a reason, and `--outcome` flags on the other terminal-status entry points.

The asymmetry — `--reason` is required on `decline` but `--outcome` is optional on `confirm`/`assume`/`replace` — is intentional. Confirming a task usually means "we tested it and it worked" (not informative to log). Declining a task is uncommon and the reason is rarely obvious without explanation; forcing capture exactly there is where the cost/value math lands.

E-787 was previously marked `declined` in error (the `declined` enum value half had shipped, the rest hadn't). Status is now `ready`, work resumes here.

### Decisions already locked with Mike

1. `--reason` on `task decline`, `--outcome` everywhere else. Both write to the same `outcome` column. Pave the cowpath: each verb gets the flag name agents instinctively type for it.
2. Outcome is REQUIRED whenever the resulting status is `declined`, regardless of entry point (`task decline`, `task update --status declined`, `task replace --status declined`). No back doors.
3. `task replace` gains optional `--status <s>` (default `obsolete`) and `--outcome <text>`.
4. `task update --outcome` works standalone or alongside `--status`. Emits an event like every other field update — that's the whole point of the architecture (this was the E-993 lesson).
5. Outcome is mutable via `task update --outcome` (event-sourced, not a frozen field).

### Decisions made by me, flagged here for review

6. **Event shape: extend `task.status_changed` with optional `Outcome`.** *(Revised during Phase 2 implementation. Original Decision #6 had this backwards — see below.)* All dedicated terminal-status verbs (`task decline`, `task confirm`, `task assume`) emit `task.status_changed` with an optional `outcome` field on the payload. The cascade semantics already built into `task.status_changed` are preserved; outcome applies to the root task only (per Decision #8). `update_plan` continues to use `task.fields_updated` for multi-field updates (which can include status); `replace_task` routes its status+outcome write through `update_plan`, so its output is `task.fields_updated`. **Original framing was wrong:** I'd planned to switch `complete_item`/`assume_item` from `task.status_changed` to `task.fields_updated`, but `task.fields_updated` has no cascade support — switching would have silently broken cascade. Extending `task.status_changed` keeps that purpose-built event focused on its purpose and adds outcome with minimal surface area (one struct field, two ~5-line write paths in executor.go and projector.go).

7. **`replace_task` event-emission fix is in scope.** Currently `replace_task` (task_cmd.py:2146-2179) does raw SQL — both the `task_deps` insert and the `UPDATE tasks SET status='obsolete'`. This violates the events-authoritative invariant we just reaffirmed in E-993. Adding `--outcome`/`--status` to it is impossible without going through events, so the status-write half MUST be fixed here. The `task_deps` insert (also raw SQL) is the same bug shape but lives in E-957's typed-link territory; I'm leaving it alone in this plan and will note it as a follow-up.

8. **Cascade + outcome:** when `task confirm --cascade --outcome "x"` is used, the outcome applies to the root task only. Descendants get cascaded confirm with no outcome. Matches existing cascade UX (only the root gets explicit attention).

9. **`task show --outcome` flag honors spec literally.** In default human mode, outcome renders only when `--outcome` is passed. In `--llm` and `--json` modes, outcome always renders when set (those modes already include all fields). Worth revisiting later — a one-line outcome on a terminal task is short enough to always display by default — but defer that to a follow-up.

10. **Validation enforced at the task_cmd impl layer**, not the CLI. `decline_item`, `update_plan`, `replace_task` each call a shared `_require_outcome_for_declined(status, outcome)` helper. Centralizes the rule so all entry points behave identically.

---

## Prerequisites

**E-1020 (precursor, blocking): Add `XDG_CONFIG_HOME` support to Python CLI.**

Currently `src/endless/config.py:6` hardcodes `Path.home() / ".config" / "endless"`. The Go side (`internal/monitor/db.go:21-28`) honors `XDG_CONFIG_HOME`; Python doesn't. This parity gap blocks isolated binary-level testing of Python CLI commands — without it, Phase 5 of E-787 cannot run against a copied DB and would silently mutate `~/.config/endless/endless.db`.

Scope of precursor:
- `src/endless/config.py`: read `XDG_CONFIG_HOME` env var first, fall back to `Path.home() / ".config"`. Mirror the Go-side logic in `internal/monitor/db.go:ConfigDir()`.
- Audit other `Path.home()` references in `src/endless/` to ensure all config-relative paths derive from `CONFIG_DIR` rather than re-deriving from `Path.home()`.
- pytest should still pass (the `isolated_env` fixture overrides `CONFIG_DIR` directly via monkeypatch, bypassing the env-var read path entirely).
- Manual verification: `XDG_CONFIG_HOME=/tmp/cfg-test ./bin/endless task list` should not touch `~/.config/endless/endless.db`.

Estimated size: ~30 lines. E-787 is now formally `blocked_by E-1020`. Also serves as concrete input to E-995's design — it's exactly the kind of isolation primitive E-995 will need to formalize.

### Decisions recorded (linked to E-787)

- E-1021 — Use `--reason` on `task decline` and `--outcome` elsewhere
- E-1022 — Require outcome at every entry point that lands a task in declined
- E-1023 — Use `task.fields_updated` for terminal-status writes; deprecate `task.status_changed` for new code
- E-1024 — Include `replace_task` event-emission fix in E-787 scope
- E-1025 — Apply cascade outcome to root task only, not descendants
- E-1026 — Enforce outcome validation at task_cmd impl layer, not CLI

---

## Implementation

### Phase 1 — Schema + Go projection/executor (must precede any event emission)

**`internal/schema/schema.sql`** (canonical, `//go:embed`-ed)
- Tasks table (lines 74-93): add `outcome TEXT` column. Place after `tier INTEGER` for diff readability.

**`src/endless/db.py`** (note: `_migrate_v5` was taken by E-957's task_deps active-voice migration since planning; using `_migrate_v6`)
- After `_migrate_v5` (the task_deps active-voice migration), add `_migrate_v6(conn)`:
  ```python
  def _migrate_v6(conn: sqlite3.Connection):
      """Schema v6: add outcome column to tasks (E-787)."""
      if _has_table(conn, "tasks") and not _has_column(conn, "tasks", "outcome"):
          conn.execute("ALTER TABLE tasks ADD COLUMN outcome TEXT")
          conn.commit()
  ```
- Wire into `_migrate()` orchestrator: add `_migrate_v6(conn)` after `_migrate_v5(conn)`.

**`internal/events/executor.go:258-262`** — `allowedFields` map: add `"outcome": "outcome"`.

**`internal/events/projector.go:225-229`** — `allowedFields` map: add `"outcome": "outcome"`.

**Critical:** executor REJECTS unknown fields (line 267), projector silently SKIPS them (line 234). Both maps must be updated *before* any event with `outcome` is emitted — otherwise live emit fails.

**`internal/web/data/types.go:68-84`** — add `Outcome string` to `TaskView` struct so the web UI can display it (web rendering itself is not in this plan; just the field availability).

**`internal/events/payload.go`** — `TaskFieldsUpdatedPayload` already supports arbitrary fields via `map[string]any`, no change needed. `TaskStatusChangedPayload` is unchanged (we're not extending it; new code uses `task.fields_updated`).

### Phase 2 — Python task_cmd implementations

**`src/endless/task_cmd.py`**

- New helper near top of the terminal-status section:
  ```python
  def _require_outcome_for_declined(status: str | None, outcome: str | None):
      if status == "declined" and not (outcome and outcome.strip()):
          raise click.ClickException(
              "An outcome is required when declining a task. "
              "Use --reason (on `task decline`) or --outcome to explain why."
          )
  ```

- New `decline_item(item_id: int, reason: str)`:
  - Validates reason is non-empty (handled at click layer too via `required=True`).
  - Emits ONE `task.fields_updated` with `{"status": "declined", "outcome": reason}`.
  - Mirrors structure of `complete_item` for output (cyan bullet, task title display).

- `complete_item` (lines 1314-1365):
  - Add `outcome: str | None = None` param.
  - Switch from `task.status_changed` to `task.fields_updated` with `{"status": "confirmed", **({"outcome": outcome} if outcome else {})}`.
  - Cascade: outcome applies only to root; descendants are cascaded via existing logic without outcome.

- `assume_item` (lines 1368-1419): same shape as `complete_item` change.

- `update_plan` (lines 1452-1551):
  - Add `outcome: str | None = None` param.
  - Add to fields-building block (around line 1538):
    ```python
    if outcome is not None:
        fields["outcome"] = outcome
        changed_names.append("outcome")
    ```
  - After all fields built, BEFORE emit_event, call `_require_outcome_for_declined(fields.get("status"), fields.get("outcome"))`. Note: `fields.get("outcome")` may be the existing DB value's stand-in if the user didn't pass `--outcome`; the rule only fires when user is *setting* status to declined without supplying outcome in this call.
  - Tier-1 auto-advance logic (line 1534) is unrelated; preserve.

- `replace_task` (lines 2146-2179) — fix to be event-sourced:
  - Add `status: str = "obsolete"` and `outcome: str | None = None` params.
  - Validate via `_require_outcome_for_declined(status, outcome)`.
  - Replace the `db.execute("UPDATE tasks SET status = 'obsolete', tier = 0 ...")` (lines 2171-2174) with a call to `update_plan(old_id, status=status, outcome=outcome)`. This routes the status/outcome write through `task.fields_updated` and the existing terminal-status tier-zero logic in the projector handles the `tier=0` automatically (projector.go:246-250 already does this).
  - The `task_deps` INSERT (lines 2158-2162) stays as raw SQL for now. It's a separate event-sourcing gap living in E-957's typed-link territory; flagging as a follow-up rather than expanding scope here.
  - Output message stays.

- `detail_item` (`task show` rendering, around lines 1570-1720):
  - Add `show_outcome: bool = False` param.
  - Modify the SELECT in this function (around line 1580-1584) to include `outcome` column.
  - JSON mode (lines 1593-1623): always include `outcome` when set.
  - LLM mode (lines 1626-1663): always echo `outcome=<value>` when set, after `status` line.
  - Human-readable (lines 1665-1720+): only show outcome if `show_outcome` is True OR if it's set and the task is in a declined status (we owe the user the "why" for declines specifically). Worth confirming with Mike but I'm planning the latter; if pushback, drop to `show_outcome only`.

### Phase 3 — CLI wiring

**`src/endless/cli.py`**

- Status enum at lines 28-29 and 544-545: no change (`declined` and `obsolete` already present).

- `task update` (lines 560-589): add `@click.option("--outcome", default=None, help="Outcome / reason for status (required if status=declined)")`. Pass through to `update_plan`.

- `task confirm` (lines 618-626): add `@click.option("--outcome", default=None, help="Outcome — what was confirmed")`. Pass to `complete_item`.

- `task assume` (lines 629-637): add `@click.option("--outcome", default=None, help="Outcome — what was assumed")`. Pass to `assume_item`.

- New `task decline` command, after `task assume`:
  ```python
  @task_cmd.command("decline")
  @click.argument("item_ids", type=TASK_ID, nargs=-1, required=True)
  @click.option("--reason", required=True, help="Why this task is being declined")
  def task_decline(item_ids, reason):
      """Decline one or more tasks (sets status=declined; reason required)."""
      from endless.task_cmd import decline_item
      for item_id in item_ids:
          decline_item(item_id, reason=reason)
  ```

- `task replace` (lines ~706-713): add `--status` (default `"obsolete"`, choices match terminal statuses) and `--outcome`. Pass both to `replace_task`.

- `task show` (lines 397-418): add `@click.option("--outcome", "show_outcome", is_flag=True, help="Show outcome field")`. Pass to `detail_item`.

### Phase 4 — Tests

**`tests/test_cli.py`** — new test functions following the existing `CliRunner` + `isolated_env` fixture pattern (conftest.py:13-76):

1. `test_task_decline_writes_outcome` — decline E-X with `--reason "x"`, assert status=declined and outcome="x" in DB.
2. `test_task_decline_requires_reason` — invoke without `--reason`, assert click error.
3. `test_task_confirm_with_outcome` — confirm + `--outcome`, assert outcome stored.
4. `test_task_confirm_without_outcome_still_works` — outcome remains NULL, status=confirmed.
5. `test_task_assume_with_outcome` — same shape as #3.
6. `test_task_replace_default_obsolete` — `task replace E-X --by E-Y`, assert E-X status=obsolete.
7. `test_task_replace_with_status_declined_requires_outcome` — `task replace E-X --by E-Y --status declined` (no outcome) — error.
8. `test_task_replace_with_status_declined_and_outcome` — same with `--outcome` — succeeds, status=declined, outcome set.
9. `test_task_update_status_declined_requires_outcome` — `task update E-X --status declined` (no outcome) — error.
10. `test_task_update_outcome_standalone` — `task update E-X --outcome "x"` — succeeds, outcome updated, status untouched.
11. `test_task_show_outcome_flag` — `task show E-X --outcome` displays outcome.
12. `test_task_show_llm_mode_includes_outcome_when_set` — `task show E-X --llm` always includes outcome line when set.
13. `test_event_log_records_outcome` — after decline, read the JSONL event log and assert the `task.fields_updated` event contains both `status: "declined"` and `outcome: "<reason>"`.
14. `test_rebuild_db_preserves_outcome` — set outcome, run `endless rebuild-db`, assert outcome survives.

   **Note:** test #14 invokes a Go subprocess (`endless rebuild-db`), and the in-process `monkeypatch.setattr` does NOT propagate to subprocesses. The test must explicitly set `XDG_CONFIG_HOME` (pointing to the pytest tmp_path's parent) in the `subprocess.run()` env before invoking the binary. This is contingent on Phase 4.5 confirming the Go side honors XDG; do not write this test until then.

### Phase 4.5 — Verify Go binaries respect isolation (spike, BEFORE writing test #14)

Read `cmd/endless-event/main.go:280-354` (`runRebuildDB`) and any other Go entry points that touch the DB. Confirm:

1. DB path comes from `monitor.DBPath()` (which reads `XDG_CONFIG_HOME`), not a hardcoded `~/.config/endless/`.
2. The temp/scratch DB used by `ProjectToTempDB` is derived from the configured root, not from `/tmp` with a fixed name that could collide between concurrent test runs.
3. No fallthrough hardcoded paths anywhere in the rebuild/validate/projection code paths.

If any of these fail, fix them BEFORE writing test #14 — without this verification, a test that subprocess-invokes `endless rebuild-db` could mutate `~/.config/endless/endless.db` despite the pytest fixture's monkeypatching.

This spike also serves as final confirmation that the precursor task (Python XDG support) was complete: invoke `XDG_CONFIG_HOME=/tmp/spike ./bin/endless task list` against an empty config dir and confirm it does NOT touch the production DB.

### Phase 5 — Manual sanity verification against an isolated DB copy

**Prerequisites:**
- Precursor task complete (XDG support in Python CLI).
- Phase 4.5 spike passed.
- `just build` has produced a fresh `./bin/endless`.
- **Do NOT run `just install` yet.** Phase 5 uses `./bin/endless` directly to keep the production binary at `/usr/local/bin/endless` untouched until after verification.

Setup (run from inside the e-787 worktree):
```bash
cd /Users/mikeschinkel/Projects/endless/.endless/worktrees/e-787
mkdir -p .test-isolated/endless
cp ~/.config/endless/endless.db .test-isolated/endless/endless.db
export XDG_CONFIG_HOME="$(pwd)/.test-isolated"
```

Verify isolation FIRST (before doing anything destructive):
```bash
./bin/endless task list | head -3   # should show tasks from the COPY, mtime of prod DB unchanged
stat -f "%Sm" ~/.config/endless/endless.db  # record mtime; check at end that it's unchanged
```

Then run sanity checks (all using `./bin/endless`, never installed `endless`):
- `./bin/endless task add "E-787 sanity test 1"` → returns ID (call it E-A).
- `./bin/endless task decline E-A --reason "outcome verification"` → succeeds.
- `./bin/endless task show E-A --outcome` → shows the reason.
- `./bin/endless task add "E-787 sanity test 2"` → returns E-B.
- `./bin/endless task update E-B --status declined` (no outcome) → fails with `_require_outcome_for_declined` error.
- `./bin/endless task update E-B --status declined --outcome "back-door test"` → succeeds.
- `./bin/endless task add "E-787 sanity test 3"` → returns E-C.
- `./bin/endless task add "E-787 replacement target"` → returns E-D.
- `./bin/endless task replace E-C --by E-D` → status=obsolete (default).
- `./bin/endless task add "E-787 sanity test 4"` → returns E-E.
- `./bin/endless task add "E-787 replacement target 2"` → returns E-F.
- `./bin/endless task replace E-E --by E-F --status declined` (no outcome) → fails.
- `./bin/endless task replace E-E --by E-F --status declined --outcome "replaced because X"` → status=declined.
- `./bin/endless task update E-A --outcome "amended reason"` → succeeds.
- `./bin/endless task show E-A --outcome` → shows the amended reason.
- `./bin/endless rebuild-db` → no errors; re-run `task show --outcome` on E-A, E-B, E-E to confirm outcomes survived.

Verify nothing leaked to prod:
```bash
stat -f "%Sm" ~/.config/endless/endless.db  # compare against recorded mtime — must be identical
```

Cleanup (from inside the e-787 worktree):
```bash
unset XDG_CONFIG_HOME
rm -rf .test-isolated
```

**Only after all checks pass against the copy, run `just install`** to deploy to `/usr/local/bin/`. The first invocation against the production DB will run `_migrate_v6` (the safe, idempotent `ALTER TABLE tasks ADD COLUMN outcome TEXT`).

---

## Critical files (full list)

- `internal/schema/schema.sql` — schema column add
- `src/endless/db.py:160-163, 349+` — migration registration + new `_migrate_v6`
- `internal/events/executor.go:258-262` — allowedFields
- `internal/events/projector.go:225-229` — allowedFields
- `internal/web/data/types.go:68-84` — TaskView struct
- `src/endless/task_cmd.py:1314, 1368, 1452, 2146, 1570` — terminal-status impls + detail rendering + new `decline_item` and `_require_outcome_for_declined`
- `src/endless/cli.py:397, 560, 618, 629, 706` — `task show`, `update`, `confirm`, `assume`, `replace` flag wiring + new `task decline`
- `tests/test_cli.py` — new tests

## Reusable existing helpers

- `_has_table` / `_has_column` (db.py:166, 174) — migration idempotency
- `emit_event` (event_bridge.py:16-26) — event emission signature
- `_resolve_project` (used throughout task_cmd.py) — project name resolution
- `validate_title` (used in `update_plan`) — unrelated, just preserving
- `_write_task_plan_file` (used in `update_plan`) — unrelated, preserving
- `task_id_display` — formatted ID rendering for output

## Out of scope / follow-ups

- Fix `task_deps` INSERT in `replace_task` to be event-sourced. Same bug shape as the status write but belongs in E-957's typed-link work.
- Web UI rendering of `outcome`. Field availability is in scope (TaskView struct); template work isn't.
- Reconsider whether `task show` should always display outcome by default in human mode (current plan: opt-in via `--outcome` per spec, default-on for declined tasks). If Mike wants always-on, simplify.

## Verification (end-to-end)

1. Precursor task (Python XDG_CONFIG_HOME support) shipped first; confirmed working manually.
2. `just build` — confirm no compile errors (Go) and no Python import errors. **Do NOT `just install` yet.**
3. Phase 4.5 spike completed — Go binaries confirmed to honor isolation.
4. `just test` — confirm all new + existing tests pass (including the new tests #1-14, with test #14 honoring XDG via subprocess env).
5. Phase 5 manual sanity checks all pass against the copied DB; prod DB mtime unchanged.
6. Only NOW run `just install` to deploy. The first prod invocation runs `_migrate_v6` against `~/.config/endless/endless.db` — one idempotent `ALTER TABLE ADD COLUMN`.
7. Post-deploy smoke: one `endless task show <some-existing-task>` against prod to confirm the deployed binary works.

## Notes for E-995 (sandbox/fixture pattern design input)

Keep this section updated during implementation. Friction points captured here become concrete design input when E-995 is planned.

Initial observations from PLANNING E-787 (will accumulate more during execution):

1. **Python/Go isolation parity gap.** Go honors `XDG_CONFIG_HOME` (`internal/monitor/db.go:21-28`); Python `config.py:6` hardcodes `Path.home()`. Required filing a precursor task. E-995 should mandate a single source-of-truth for "where is config" that both languages consume — probably a small Go binary command like `endless config-paths` that Python shells out to, or a shared file-based config pointer.

2. **In-process monkeypatch vs subprocess isolation are incompatible.** pytest's `isolated_env` fixture monkeypatches Python module attributes — fine for in-process tests, useless for tests that invoke Go binaries via subprocess. E-995 should provide a unified isolation primitive both Python in-process AND subprocess-launched Go binaries opt into (likely env-var based, since that's the only thing that crosses process boundaries cheaply).

3. **No `endless sandbox` (or `just test-isolated`) for binary-level smoke.** Phase 5 setup/cleanup is hand-rolled bash. A reusable harness (`endless sandbox start <name> [--copy-from prod]`, `endless sandbox stop <name>`) would have made this trivial. Worth scoping into E-995.

4. **Manual cleanup is error-prone.** Phase 5 ends with `unset XDG_CONFIG_HOME` and `rm -rf .endless/worktrees/e-787/.test-isolated`. Easy to forget the unset and leave the shell pointing at a deleted path, leading to confusing errors on the next `endless` command. Auto-cleanup (e.g., trap-on-EXIT in a wrapper script, or a context-manager-style `endless sandbox` command) is safer.

5. **Verification of "no prod leak" is purely manual.** Phase 5 uses `stat` mtime check, which is brittle. E-995 should consider an explicit "this command CANNOT touch prod paths" assertion at sandbox boundaries — e.g., a wrapper that bind-mounts `~/.config/endless` read-only during sandbox runs.

6. **Migration safety against real prod schema vs fresh schema.** Phase 5 catches "does the new migration apply cleanly to a copy of prod?". The pytest fixture only catches "does it apply to a fresh schema?". These are different. E-995 should consider whether the standard test workflow includes a prod-copy migration spike (and a fixture for that) or relies on case-by-case manual setup.

7. **Installed binary is a uv-tool snapshot, not a symlink to src/.** `endless` is installed via `uv tool install` to `/Users/mikeschinkel/.local/share/uv/tools/endless/lib/python3.14/site-packages/endless/` — a snapshotted copy. Source changes in `src/endless/` don't reach the running binary until `uv tool reinstall`. E-995 should either (a) document this deploy-hop explicitly in test workflow guidance or (b) provide a `just dev-install` recipe that swaps the snapshot for a symlink to `src/` for development, restoring the snapshot on cleanup. Hit during E-1020 — the verification path required PYTHONPATH manipulation to test the source code without disturbing the installed binary.
