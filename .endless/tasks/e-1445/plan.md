# E-1445: Auto-create worktree on `task update --text` only when task is claimed

## Context

`endless task update --text` today writes the plan file to a worktree and provisions a worktree (plus a per-worktree sandbox under `~/.cache/endless/sandboxes/`) whenever none exists. The trigger is `_worktree_for_task(task_id) is None` — purely a "does a worktree exist yet?" check, with no consideration of whether the caller intends to start working.

Surfaced during E-1444's plan attach: filing a ticket for triage and attaching its long-form plan provisioned an unwanted worktree + sandbox the user had no intention of working in. The `--no-create-worktree` flag exists as an escape hatch, but defaults backwards — the surprising behavior is the default; the quiet behavior requires explicit opt-out.

An earlier draft of this plan proposed using the main checkout's `.endless/plans/E-NNN.md` as a staging area for plans on unclaimed tasks. That violates the no-direct-commits-to-main discipline (E-1199): plan files on main's filesystem are exactly the kind of churn the rule is there to prevent. This revised plan replaces that staging idea with a DB-authoritative design.

## Approach (decided 2026-05-21 with Mike)

The DB is the sole authoritative store for plan text. `tasks.text` holds the canonical content. Files in `<worktree>/.endless/plans/E-NNN.md` are materializations of the DB content into a worktree's filesystem — they appear when a worktree is born and refresh when `task update --text` is re-run on a task that has a worktree. The main checkout's filesystem never contains a plan file for an unclaimed task.

### Behavior

- `endless task update E-NNN --text <path-or-stdin>` reads content and writes `tasks.text` via a `task.fields_updated` event. **It does not provision a worktree under any condition.** If a worktree already exists for the task, it also mirrors the content to `<worktree>/.endless/plans/E-NNN.md` as a user convenience. If no worktree exists, no file is written anywhere outside the DB / event ledger.
- `create_task_worktree` (called by `task claim` and `task spawn`) reads `tasks.text` from the DB after `git worktree add` succeeds. If the field is non-empty, it writes `<worktree>/.endless/plans/E-NNN.md` as part of worktree initialization. This is the single materialization point.
- `--no-create-worktree` is removed from `task update`. The command no longer creates worktrees at all, so the flag has no meaning. Passing it becomes an unknown-option error.

### Behavior matrix

| Caller intent | Today | After E-1445 |
|---|---|---|
| Triage filing: `task update --text` on a `needs_plan` task, no worktree | Auto-creates worktree + sandbox (surprise) | DB updated; nothing on disk; user unaffected by phantom worktrees |
| Active work: `task update --text` after `task claim` | Worktree exists; file written there | Same — worktree exists, file mirrored there |
| Claim a task that already has plan text in DB | Worktree created; plan file written via legacy code path (depends on caller passing `--text`) | Worktree created; plan file materialized from DB automatically |

### Considered alternatives (rejected)

- **Filesystem staging in main:** the first draft proposed `<main>/.endless/plans/E-NNN.md` for unclaimed tasks, with `create_task_worktree` copying from main into the new worktree. Rejected because it violates E-1199 (no direct commits to main) and creates churn on main from triage workflows — the very problem Mike has been fighting.
- **Flip the default globally** (`--no-create-worktree` becomes default with `--create-worktree` as opt-in for the claim-then-attach case). Rejected — the smart heuristic approach is cleaner because no flag is needed in either common path; the DB-as-source decoupling makes the auto-create entirely unnecessary for `task update --text`.
- **Per-project config toggle.** Rejected as overkill for a single binary question.

## Implementation sites

- `src/endless/task_cmd.py:write_plan_to_worktree` (around line 425) — refactor. The function currently has two branches: "worktree exists, write there" and "no worktree, create then write". The second branch goes away entirely. The function becomes: emit `task.fields_updated` for the text field unconditionally; if a worktree exists, mirror to its plans dir as a convenience. Rename to something like `set_plan_text` to reflect that it's no longer "write to worktree."
- `src/endless/worktree_cmd.py:create_task_worktree` — after `git worktree add` succeeds, query `tasks.text` from the DB and, if non-empty, write `<worktree>/.endless/plans/E-NNN.md`. Read-only DB access from Python is forbidden per the E-894 architecture, so this read happens via a small Go-side helper or via the existing event-bridge mechanism (`endless-event` or `endless-session-query`). Concrete plumbing: add a `get-task-text` subcommand to `endless-event` (or extend `endless-session-query`) and shell out.
- `src/endless/cli.py` — remove the `--no-create-worktree` option from `task update` and any other `task` subcommands that don't create worktrees (audit). The flag stays only on commands that genuinely create worktrees and where opting out is meaningful.
- `src/endless/event_bridge.py` — no change. `task.fields_updated` already handles the `text` field.

## Verification

1. **Triage filing — the bug case:** from any pane (no claim yet), `endless task update E-NNN --text /tmp/plan.md` on a `needs_plan` task. Expect: `tasks.text` set in DB; no entry under `.endless/worktrees/`; no sandbox under `~/.cache/endless/sandboxes/`; `endless worktree list` does not show e-NNN.

2. **Claim-after-triage materializes:** continuing from #1, run `endless task claim E-NNN`. Expect: worktree created at `.endless/worktrees/e-NNN/`; `.endless/worktrees/e-NNN/.endless/plans/E-NNN.md` exists with content matching the earlier `--text`.

3. **Re-attach inside a worktree:** `endless task update E-NNN --text /tmp/plan-v2.md`. Expect: DB updated; worktree's `.endless/plans/E-NNN.md` updated to match.

4. **Drift surfacing:** manually edit the in-worktree plan file (without going through `task update`); then `endless task update --text /tmp/plan-v3.md`. Expect: DB updated; in-worktree file overwritten without warning. The local edit was uncommitted (or would surface via `git status` if the user wanted to preserve it). The decision: DB wins, no diff prompt; rely on git as the safety net.

5. **Removed flag rejection:** `endless task update E-NNN --text /tmp/plan.md --no-create-worktree` fails with Click's standard "No such option: --no-create-worktree" error.

6. **Claim with non-empty DB text but no `--text` flag:** `endless task claim E-NNN` on a task whose `tasks.text` was set earlier by triage. Expect: worktree's plan file is materialized from DB automatically, no `task update --text` call required.

## Related

- E-1199 — "no direct commits to main." This task reinforces it by removing one of the workflows that tempts the violation.
- E-1216 — plan files live in worktrees, never main directly. This task closes a loophole in that rule (plans-on-main as triage staging).
- E-1444 — the triage filing whose plan attach exposed this.
- E-1281 — sandbox provisioning. Less load on the sandbox subsystem once auto-creation from `task update --text` is gone.
- E-894 — Python-side DB reads are forbidden; the new `create_task_worktree → read tasks.text` step needs a Go-side helper or an `endless-event` subcommand.
