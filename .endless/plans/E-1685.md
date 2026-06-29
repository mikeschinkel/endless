# Show the focal task's direct dependents in `session next`

## Why
When a session files tasks where one blocks another — e.g. the session for E-100 files E-101 and E-102 with E-102 `blocked_by` E-101 — the user otherwise keeps **E-100's** window open just to remember that E-102 must be spawned after E-101 lands. The fix is to surface E-102 **in the session working its blocker, E-101**. `session next` already shows what *blocks* the focal task (`⊗`); this adds the mirror — the focal task's direct dependents (what it *unblocks* on completion). So while you work E-101, its pane shows E-102 carrying a `⊗` (still blocked by E-101); when E-101 lands the `⊗` clears and E-102 is visibly ready to spawn. Because the dependent rides along with the session still in flight (E-101), the **filing** session (E-100) no longer needs to stay open as a manual reminder — it can be closed.

## What
Include the focal task's **direct dependents** — tasks `T` where `task_deps(source=focal, target=T, dep_type='blocks')` — as rows in `session next`, alongside the existing session_tasks / focal / parent rows. They render like any other row (action icon, type, phase, …) with the `⊗` blocked icon while the focal is still open. When the focal lands and the block clears, `⊗` simply disappears — **no special "now unblocked" highlight needed**; absence of `⊗` is the signal. Show them from the start, even while blocked (do NOT hide until unblocked).

## How — read-time SQL, NOT a write into `session_tasks`
Add a `UNION` to the row set: `tasks t WHERE EXISTS (task_deps WHERE source_type='task' AND source_id=focal AND target_id=t.id AND dep_type='blocks')`. Computed at read time.

Do NOT materialize these into `session_tasks` (the rejected alternative #2), because:
- `session_tasks` is a **projection of the event ledger** (rebuildable from `task.*` events); writing computed dependents in breaks that invariant — those rows have no backing event.
- It corrupts the table's semantic ("tasks this session *touched*") and collides with E-1462's `relation` column (goal/surfaced/revisited — none of which a dependent is).
- It would be a write on a read/poll path (every 2s refresh), needing cleanup when the dependency changes.
- It caches trivially-computable derived data, against compute-over-cache.

(Decision rationale: approach #1 chosen over #2 in the E-1461 design discussion, 2026-06-29.)

## Notes
- Direct (one-hop) dependents only — not the transitive closure.
- Done/terminal dependents are omitted like other done-work (unless `--all`).

## Verification
Deliver `tests/tasks/e-1685-verify.sh` following the established convention (model: `tests/tasks/e-1577-verify.sh`): bash, routes the CLI through the sandbox DB (`--db sandbox`), uses the `section`/`report_pass`/`report_fail`/`summary` + `assert_*` helpers, prints `ALL PASSED` or a `FAILED` list, exits 0 on all-passed / 1 on any failure, runnable from anywhere in the worktree.

Against the sandbox it must verify:
- Create focal F and dependent D with `D blocked_by F`; `session next` for F **includes D as a row** carrying the blocked indicator (`⊗`) **while F is open**.
- D is shown **even while blocked** (not hidden until unblocked).
- After F reaches a landed/terminal state, D's `⊗` clears (D shows actionable).
- A done/terminal dependent is omitted by default, included under `--all`.
- `session next` does NOT write to `session_tasks` (dependents are read-time only): the session's `session_tasks` row count is unchanged after running it.

This needs `session next` to be invocable with an explicit focal task / seeded sandbox session — i.e. headless, without a live tmux pane. Confirm that entry point exists or add it.

The verify-handoff instructions for this task must be exactly these two lines, nothing more:

    esu
    ./tests/tasks/e-1685-verify.sh

If that can't be the whole handoff, the script is incomplete — fix the script, not the handoff.

## Depends on
- **E-1465** (the `session next` read command) — must exist first; this extends its row-set query.
